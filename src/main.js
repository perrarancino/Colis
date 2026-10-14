import * as THREE from 'three';
import { createIsland } from './island.js';
import { createOcean } from './ocean.js';
import { Player } from './player.js';
import { Input } from './input.js';
import { createMinimap } from './minimap.js';
import { hashString, clamp } from './random.js';

const SEA_COLOR = 0x2a8fb5;

// ---- renderer / scene ------------------------------------------------------
const renderer = new THREE.WebGLRenderer({ antialias: true });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
document.body.appendChild(renderer.domElement);

const scene = new THREE.Scene();
scene.background = new THREE.Color(SEA_COLOR);
scene.fog = new THREE.Fog(SEA_COLOR, 90, 230);

const camera = new THREE.PerspectiveCamera(40, 1, 0.5, 600);
let zoom = 26; // camera height above the player
const CAMERA_TILT = 9; // how far "south" of the player the camera sits (top-down, slightly angled)

scene.add(new THREE.HemisphereLight(0xdff3ff, 0x3b5a3a, 0.85));
const sun = new THREE.DirectionalLight(0xfff1d6, 2.2);
sun.position.set(-30, 50, 20);
sun.castShadow = true;
sun.shadow.mapSize.set(2048, 2048);
Object.assign(sun.shadow.camera, { left: -32, right: 32, top: 32, bottom: -32, near: 1, far: 140 });
sun.shadow.bias = -0.0004;
scene.add(sun, sun.target);

const ocean = createOcean();
scene.add(ocean);

// ---- world -----------------------------------------------------------------
const player = new Player();
scene.add(player.mesh);
const minimap = createMinimap();
const input = new Input();
const hud = document.getElementById('hud');

let world = null;

function parseSeed(text) {
  if (!text) return null;
  return /^-?\d+$/.test(text) ? Number(text) >>> 0 : hashString(text);
}

function loadWorld(seedText) {
  const seed = parseSeed(seedText) ?? (Math.random() * 2 ** 32) >>> 0;
  if (world) {
    scene.remove(world.group);
    world.dispose();
  }
  world = createIsland(seed);
  scene.add(world.group);
  player.teleport(world.spawn.x, world.spawn.z, world);
  minimap.build(world);
  camera.position.set(player.x, zoom, player.z + CAMERA_TILT);
  camera.lookAt(player.x, 0, player.z);

  const url = new URL(location.href);
  url.searchParams.set('seed', String(seed));
  history.replaceState(null, '', url);
  renderHud(seed);
}

function renderHud(seed) {
  hud.innerHTML =
    `<b>Colis</b><br>seed <span style="font-variant-numeric:tabular-nums">${seed}</span><br>` +
    `<span class="dim">WASD / arrows — move · Shift — sprint<br>wheel — zoom · R — new island</span>`;
}

loadWorld(new URL(location.href).searchParams.get('seed'));

// ---- events ----------------------------------------------------------------
function resize() {
  renderer.setSize(innerWidth, innerHeight);
  camera.aspect = innerWidth / innerHeight;
  camera.updateProjectionMatrix();
}
addEventListener('resize', resize);
resize();

addEventListener(
  'wheel',
  (e) => {
    zoom = clamp(zoom * (e.deltaY > 0 ? 1.08 : 0.92), 12, 60);
  },
  { passive: true },
);

// ---- main loop ---------------------------------------------------------------
const clock = new THREE.Clock();
const camTarget = new THREE.Vector3(player.x, 0, player.z);
const look = new THREE.Vector3();

function frame() {
  const dt = Math.min(clock.getDelta(), 0.05);
  const t = clock.elapsedTime;

  if (input.consume('KeyR')) loadWorld(null);

  player.update(dt, input.axis(), input.held('sprint'), world);

  // Smooth follow camera, looking straight down at the player from above.
  camTarget.x += (player.x - camTarget.x) * Math.min(1, dt * 6);
  camTarget.z += (player.z - camTarget.z) * Math.min(1, dt * 6);
  camera.position.set(camTarget.x, zoom, camTarget.z + CAMERA_TILT * (zoom / 26));
  look.set(camTarget.x, 0, camTarget.z);
  camera.lookAt(look);

  // Shadows and sea follow the player.
  sun.position.set(camTarget.x - 30, 50, camTarget.z + 20);
  sun.target.position.set(camTarget.x, 0, camTarget.z);
  ocean.userData.update(t, camTarget.x, camTarget.z);

  minimap.draw(player);
  renderer.render(scene, camera);
  requestAnimationFrame(frame);
}
frame();
