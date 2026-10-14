import * as THREE from 'three';

const SIZE = 700;
const SEGMENTS = 110;

// A big translucent plane at sea level whose vertices ripple on the CPU.
// Flat shading is derived in the fragment shader, so no normal updates needed.
export function createOcean() {
  const geo = new THREE.PlaneGeometry(SIZE, SIZE, SEGMENTS, SEGMENTS);
  geo.rotateX(-Math.PI / 2);
  const base = geo.attributes.position.array.slice();
  const mat = new THREE.MeshStandardMaterial({
    color: 0x2b9ec4,
    transparent: true,
    opacity: 0.72,
    roughness: 0.25,
    metalness: 0.05,
    flatShading: true,
  });
  const mesh = new THREE.Mesh(geo, mat);
  mesh.receiveShadow = true;

  const pos = geo.attributes.position;
  mesh.userData.update = (time, centerX, centerZ) => {
    // Keep the grid centred on the player (snapped to the grid step) so the sea never ends.
    const step = SIZE / SEGMENTS;
    mesh.position.x = Math.round(centerX / step) * step;
    mesh.position.z = Math.round(centerZ / step) * step;
    for (let i = 0; i < pos.count; i++) {
      const x = base[i * 3] + mesh.position.x;
      const z = base[i * 3 + 2] + mesh.position.z;
      pos.setY(
        i,
        Math.sin(x * 0.35 + time * 1.3) * 0.07 +
          Math.sin(z * 0.28 + time * 1.0) * 0.07 +
          Math.sin((x + z) * 0.12 + time * 0.6) * 0.1,
      );
    }
    pos.needsUpdate = true;
  };
  return mesh;
}
