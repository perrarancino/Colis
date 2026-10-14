import * as THREE from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import { Noise2D } from './noise.js';
import { mulberry32, smoothstep } from './random.js';

export const SEA_LEVEL = 0;
export const DEEP_LIMIT = -1.3; // below this the cube cannot wade any further
export const WORLD_SIZE = 240; // terrain extent (square, centred on the origin)
const GRID = 200; // terrain cells per side
const ISLAND_RADIUS = 62;
const HEIGHT_SCALE = 11;

// [exclusive upper height, colour] — first entry whose limit is above the sample wins.
const PALETTE = [
  [-3.0, 0x143f5c], // sea floor
  [-1.0, 0x1f6f8e],
  [0.0, 0x4fb0b8], // shallows
  [0.55, 0xe8d79f], // beach
  [1.6, 0xa6cf5a], // meadow
  [4.0, 0x6bb04a], // grass
  [5.6, 0x4a9441], // dense grass
  [7.2, 0x8c8e93], // rock
  [Infinity, 0xf3f6f8], // snow
].map(([limit, hex]) => ({ limit, color: new THREE.Color(hex) }));

export function colorForHeight(h, out = new THREE.Color()) {
  for (const entry of PALETTE) {
    if (h < entry.limit) return out.copy(entry.color);
  }
  return out.copy(PALETTE[PALETTE.length - 1].color);
}

export function createIsland(seed) {
  const warpNoise = new Noise2D(seed ^ 0x9e3779b1);
  const baseNoise = new Noise2D(seed ^ 0x85ebca6b);
  const ridgeNoise = new Noise2D(seed ^ 0xc2b2ae35);
  const forestNoise = new Noise2D(seed ^ 0x27d4eb2f);

  // World-space terrain height. Positive above sea level.
  function heightAt(x, z) {
    // Warp the sample position so the coastline gets bays and peninsulas.
    const wx = x + warpNoise.fbm(x * 0.018 + 40, z * 0.018, 3) * 26;
    const wz = z + warpNoise.fbm(x * 0.018, z * 0.018 + 90, 3) * 26;
    const land = 1 - Math.hypot(wx, wz) / ISLAND_RADIUS; // 1 in the middle, 0 at the shore

    const base = baseNoise.fbm(x * 0.025, z * 0.025, 5);
    const ridge = 1 - Math.abs(ridgeNoise.fbm(x * 0.03 + 7, z * 0.03 - 3, 4));
    const hills = smoothstep(0.25, 0.75, land) * ridge * ridge * 0.55;

    const n = land * 1.15 + base * 0.32 + hills - 0.12;
    if (n >= 0) return n * HEIGHT_SCALE;
    return Math.max(n * HEIGHT_SCALE * 0.75, -5.5); // gentle slope down to the sea floor
  }

  const half = WORLD_SIZE / 2;
  const cell = WORLD_SIZE / GRID;
  const stride = GRID + 1;
  const heights = new Float32Array(stride * stride);
  for (let j = 0; j <= GRID; j++) {
    for (let i = 0; i <= GRID; i++) {
      heights[j * stride + i] = heightAt(i * cell - half, j * cell - half);
    }
  }

  const terrain = buildTerrain(heights, seed);
  const rand = mulberry32(seed ^ 0xdeadbeef);
  const { group: props, colliders } = scatterProps(heightAt, forestNoise, rand);

  const group = new THREE.Group();
  group.add(terrain, props);

  return {
    seed,
    group,
    heightAt,
    colliders,
    spawn: findSpawn(heightAt),
    half,
    dispose() {
      group.traverse((o) => {
        if (o.geometry) o.geometry.dispose();
        if (o.material) o.material.dispose?.();
      });
    },
  };
}

function buildTerrain(heights, seed) {
  const pos = new Float32Array(GRID * GRID * 6 * 3);
  const col = new Float32Array(GRID * GRID * 6 * 3);
  const half = WORLD_SIZE / 2;
  const cell = WORLD_SIZE / GRID;
  const stride = GRID + 1;
  const rand = mulberry32(seed ^ 0x1234abcd);
  const c = new THREE.Color();
  let p = 0;

  const put = (i, j, color) => {
    pos[p] = i * cell - half;
    pos[p + 1] = heights[j * stride + i];
    pos[p + 2] = j * cell - half;
    col[p] = color.r;
    col[p + 1] = color.g;
    col[p + 2] = color.b;
    p += 3;
  };

  for (let j = 0; j < GRID; j++) {
    for (let i = 0; i < GRID; i++) {
      // Two triangles per cell; the diagonal alternates for a less grid-like look.
      const tris = (i + j) & 1
        ? [[i, j, i, j + 1, i + 1, j], [i + 1, j, i, j + 1, i + 1, j + 1]]
        : [[i, j, i, j + 1, i + 1, j + 1], [i, j, i + 1, j + 1, i + 1, j]];
      for (const t of tris) {
        const avg = (heights[t[1] * stride + t[0]] + heights[t[3] * stride + t[2]] + heights[t[5] * stride + t[4]]) / 3;
        colorForHeight(avg, c).multiplyScalar(0.94 + rand() * 0.12); // per-face tint keeps the low-poly feel
        put(t[0], t[1], c);
        put(t[2], t[3], c);
        put(t[4], t[5], c);
      }
    }
  }

  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.BufferAttribute(pos, 3));
  geo.setAttribute('color', new THREE.BufferAttribute(col, 3));
  geo.computeVertexNormals(); // non-indexed => flat shading
  const mesh = new THREE.Mesh(geo, new THREE.MeshStandardMaterial({ vertexColors: true, roughness: 0.95, metalness: 0 }));
  mesh.receiveShadow = true;
  return mesh;
}

function scatterProps(heightAt, forestNoise, rand) {
  const trees = [];
  const rocks = [];
  const colliders = [];

  for (let n = 0; n < 9000; n++) {
    const x = (rand() - 0.5) * ISLAND_RADIUS * 2.4;
    const z = (rand() - 0.5) * ISLAND_RADIUS * 2.4;
    const h = heightAt(x, z);
    const density = smoothstep(-0.2, 0.5, forestNoise.fbm(x * 0.05, z * 0.05, 3));

    if (h > 1.0 && h < 6 && rand() < 0.12 + density * 0.55) {
      if (colliders.some((c) => (c.x - x) ** 2 + (c.z - z) ** 2 < 2.2 * 2.2)) continue;
      const s = 0.8 + rand() * 0.9;
      trees.push({ x, y: h, z, s, rot: rand() * Math.PI * 2 });
      colliders.push({ x, z, r: 0.45 * s });
    } else if (h > 0.2 && h < 8 && rand() < 0.012) {
      const s = 0.5 + rand() * 1.2;
      rocks.push({ x, y: h, z, s, rot: rand() * Math.PI * 2 });
      colliders.push({ x, z, r: 0.75 * s });
    }
  }

  const group = new THREE.Group();
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const up = new THREE.Vector3(0, 1, 0);
  const scale = new THREE.Vector3();
  const position = new THREE.Vector3();

  if (trees.length) {
    const trunkGeo = new THREE.CylinderGeometry(0.16, 0.22, 1.0, 6).translate(0, 0.5, 0);
    const leaves = mergeGeometries([
      new THREE.ConeGeometry(1.0, 1.7, 7).translate(0, 1.6, 0),
      new THREE.ConeGeometry(0.75, 1.4, 7).translate(0, 2.5, 0),
    ]);
    const trunks = new THREE.InstancedMesh(trunkGeo, new THREE.MeshStandardMaterial({ color: 0x6b4a2f, flatShading: true }), trees.length);
    const foliage = new THREE.InstancedMesh(leaves, new THREE.MeshStandardMaterial({ color: 0xffffff, flatShading: true }), trees.length);
    const tint = new THREE.Color();
    trees.forEach((t, i) => {
      q.setFromAxisAngle(up, t.rot);
      m.compose(position.set(t.x, t.y - 0.05, t.z), q, scale.set(t.s, t.s, t.s));
      trunks.setMatrixAt(i, m);
      foliage.setMatrixAt(i, m);
      foliage.setColorAt(i, tint.setHSL(0.31 + (rand() - 0.5) * 0.05, 0.45, 0.27 + rand() * 0.1));
    });
    trunks.castShadow = foliage.castShadow = true;
    group.add(trunks, foliage);
  }

  if (rocks.length) {
    const mesh = new THREE.InstancedMesh(
      new THREE.DodecahedronGeometry(0.8, 0),
      new THREE.MeshStandardMaterial({ color: 0x8a8d92, flatShading: true, roughness: 1 }),
      rocks.length,
    );
    rocks.forEach((r, i) => {
      q.setFromAxisAngle(up, r.rot);
      m.compose(position.set(r.x, r.y + 0.1 * r.s, r.z), q, scale.set(r.s * 1.2, r.s * 0.8, r.s));
      mesh.setMatrixAt(i, m);
    });
    mesh.castShadow = mesh.receiveShadow = true;
    group.add(mesh);
  }

  return { group, colliders };
}

// Walk inwards along rays from the open sea until we hit the first stretch of beach.
function findSpawn(heightAt) {
  for (let a = 0; a < 360; a += 15) {
    const dx = Math.cos((a * Math.PI) / 180);
    const dz = Math.sin((a * Math.PI) / 180);
    for (let r = ISLAND_RADIUS * 1.4; r > 0; r -= 0.5) {
      const h = heightAt(dx * r, dz * r);
      if (h > 0.45 && h < 1.1) return { x: dx * r, z: dz * r };
    }
  }
  return { x: 0, z: 0 };
}
