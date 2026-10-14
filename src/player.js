import * as THREE from 'three';
import { DEEP_LIMIT, SEA_LEVEL } from './island.js';
import { clamp, lerp } from './random.js';

const RADIUS = 0.5; // collision radius
const WALK_SPEED = 7;
const SPRINT_SPEED = 11;
const WATER_FACTOR = 0.55;
const ACCEL = 40;

export class Player {
  constructor() {
    this.mesh = new THREE.Group();

    const body = new THREE.Mesh(
      new THREE.BoxGeometry(1, 1, 1),
      new THREE.MeshStandardMaterial({ color: 0xff7a3d, roughness: 0.5 }),
    );
    body.castShadow = true;
    const edges = new THREE.LineSegments(
      new THREE.EdgesGeometry(body.geometry),
      new THREE.LineBasicMaterial({ color: 0x7a2a05 }),
    );
    body.add(edges);

    // Two eyes on the +Z face so you can tell where the cube is looking.
    const eyeMat = new THREE.MeshBasicMaterial({ color: 0x1b1b1b });
    for (const sx of [-0.2, 0.2]) {
      const eye = new THREE.Mesh(new THREE.PlaneGeometry(0.14, 0.22), eyeMat);
      eye.position.set(sx, 0.12, 0.501);
      body.add(eye);
    }

    this.body = body;
    this.mesh.add(body);

    this.x = 0;
    this.z = 0;
    this.vx = 0;
    this.vz = 0;
    this.yaw = 0;
    this.bob = 0;
    this.groundY = 0;
    this.inWater = false;
    this.moving = false;
  }

  teleport(x, z, world) {
    this.x = x;
    this.z = z;
    this.vx = this.vz = 0;
    this.groundY = Math.max(world.heightAt(x, z), SEA_LEVEL);
    this.sync(0, 0, world);
  }

  // dir: screen-space {x, y}. On a top-down camera screen-up is world -Z.
  update(dt, dir, sprint, world) {
    const len = Math.hypot(dir.x, dir.y);
    const wx = len > 0 ? dir.x / Math.max(len, 1) : 0;
    const wz = len > 0 ? -dir.y / Math.max(len, 1) : 0;

    const h = world.heightAt(this.x, this.z);
    this.inWater = h < SEA_LEVEL - 0.05;
    const top = (sprint ? SPRINT_SPEED : WALK_SPEED) * (this.inWater ? WATER_FACTOR : 1);

    // Accelerate towards the desired velocity, so starts and stops feel soft.
    const k = Math.min(1, ACCEL * dt / Math.max(top, 1));
    this.vx = lerp(this.vx, wx * top, k);
    this.vz = lerp(this.vz, wz * top, k);

    this.tryMove(this.vx * dt, 0, world);
    this.tryMove(0, this.vz * dt, world);

    const speed = Math.hypot(this.vx, this.vz);
    this.moving = speed > 0.3;
    if (this.moving) {
      const target = Math.atan2(this.vx, this.vz);
      let d = target - this.yaw;
      d = Math.atan2(Math.sin(d), Math.cos(d));
      this.yaw += d * Math.min(1, dt * 14);
      this.bob += dt * speed * (this.inWater ? 1.1 : 1.5);
    }
    this.sync(dt, speed, world);
  }

  tryMove(dx, dz, world) {
    if (dx === 0 && dz === 0) return;
    let nx = this.x + dx;
    let nz = this.z + dz;
    const limit = world.half - 2;
    nx = clamp(nx, -limit, limit);
    nz = clamp(nz, -limit, limit);

    // Too deep to wade: block the move (slide happens because axes are tried separately).
    if (world.heightAt(nx, nz) < DEEP_LIMIT) {
      this.vx = dx !== 0 ? 0 : this.vx;
      this.vz = dz !== 0 ? 0 : this.vz;
      return;
    }
    for (const c of world.colliders) {
      const ddx = nx - c.x;
      const ddz = nz - c.z;
      const min = c.r + RADIUS;
      if (ddx * ddx + ddz * ddz < min * min) {
        this.vx = dx !== 0 ? 0 : this.vx;
        this.vz = dz !== 0 ? 0 : this.vz;
        return;
      }
    }
    this.x = nx;
    this.z = nz;
  }

  // Push the simulated state onto the mesh: height, heading, hop and lean.
  sync(dt, speed, world) {
    const h = world.heightAt(this.x, this.z);
    // In the sea the cube floats on the surface instead of following the sea floor.
    const target = this.inWater ? SEA_LEVEL - 0.2 : h;
    this.groundY = dt > 0 ? lerp(this.groundY, target, Math.min(1, dt * 18)) : target;

    const hop = this.moving && !this.inWater ? Math.abs(Math.sin(this.bob)) * 0.22 : 0;
    const wobble = this.inWater ? Math.sin(performance.now() / 380) * 0.04 : 0;
    this.mesh.position.set(this.x, this.groundY + 0.5 + hop + wobble, this.z);
    this.mesh.rotation.y = this.yaw;
    this.body.rotation.x = clamp(speed * 0.018, 0, 0.2);
    const squash = this.moving ? 1 - 0.06 * Math.cos(this.bob * 2) : 1;
    this.body.scale.set(1 / Math.sqrt(squash), squash, 1 / Math.sqrt(squash));
  }
}
