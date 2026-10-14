const KEYS = {
  up: ['KeyW', 'ArrowUp'],
  down: ['KeyS', 'ArrowDown'],
  left: ['KeyA', 'ArrowLeft'],
  right: ['KeyD', 'ArrowRight'],
  sprint: ['ShiftLeft', 'ShiftRight'],
};

export class Input {
  constructor(target = window) {
    this.down = new Set();
    this.pressed = new Set(); // keys pressed since the last consume()
    target.addEventListener('keydown', (e) => {
      if (e.repeat) return;
      this.down.add(e.code);
      this.pressed.add(e.code);
      if (e.code.startsWith('Arrow') || e.code === 'Space') e.preventDefault();
    });
    target.addEventListener('keyup', (e) => this.down.delete(e.code));
    target.addEventListener('blur', () => this.down.clear());
  }

  held(action) {
    return KEYS[action].some((k) => this.down.has(k));
  }

  // Movement vector in screen space: x right, y up. Not normalised.
  axis() {
    return {
      x: (this.held('right') ? 1 : 0) - (this.held('left') ? 1 : 0),
      y: (this.held('up') ? 1 : 0) - (this.held('down') ? 1 : 0),
    };
  }

  consume(code) {
    return this.pressed.delete(code);
  }
}
