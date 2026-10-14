import * as THREE from 'three';
import { colorForHeight, WORLD_SIZE } from './island.js';

const PX = 150; // canvas size in CSS pixels

// Small corner map: coastline from the heightmap plus a dot for the player.
export function createMinimap() {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = PX;
  Object.assign(canvas.style, {
    position: 'fixed',
    right: '14px',
    top: '12px',
    width: PX + 'px',
    height: PX + 'px',
    borderRadius: '10px',
    border: '2px solid rgba(255,255,255,.55)',
    boxShadow: '0 2px 10px rgba(0,0,0,.35)',
    background: '#143f5c',
  });
  document.body.appendChild(canvas);
  const ctx = canvas.getContext('2d');
  const terrain = document.createElement('canvas');
  terrain.width = terrain.height = PX;

  const c = new THREE.Color();

  return {
    build(world) {
      const tctx = terrain.getContext('2d');
      const img = tctx.createImageData(PX, PX);
      for (let y = 0; y < PX; y++) {
        for (let x = 0; x < PX; x++) {
          const wx = (x / PX - 0.5) * WORLD_SIZE;
          const wz = (y / PX - 0.5) * WORLD_SIZE;
          colorForHeight(world.heightAt(wx, wz), c);
          const o = (y * PX + x) * 4;
          img.data[o] = c.r * 255;
          img.data[o + 1] = c.g * 255;
          img.data[o + 2] = c.b * 255;
          img.data[o + 3] = 255;
        }
      }
      tctx.putImageData(img, 0, 0);
    },
    draw(player) {
      ctx.drawImage(terrain, 0, 0);
      const px = (player.x / WORLD_SIZE + 0.5) * PX;
      const py = (player.z / WORLD_SIZE + 0.5) * PX;
      ctx.fillStyle = '#ff7a3d';
      ctx.strokeStyle = '#fff';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.rect(px - 3, py - 3, 6, 6);
      ctx.fill();
      ctx.stroke();
    },
  };
}
