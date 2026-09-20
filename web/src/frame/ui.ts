/** Small DOM helpers. The frame builds its UI imperatively; there is no
 * framework, because the whole bundle has to stay small enough that loading
 * it never delays a host page. */

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

export function clear(node: HTMLElement): void {
  while (node.firstChild) node.removeChild(node.firstChild);
}

/** Announces a message to assistive technology without moving focus. */
export function announce(region: HTMLElement, message: string): void {
  // Clearing first guarantees the message is re-announced even when it is
  // identical to the previous one, which matters for repeated "not quite"
  // feedback.
  region.textContent = '';
  window.setTimeout(() => {
    region.textContent = message;
  }, 30);
}

export function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

/**
 * Converts a pointer event into coordinates within an element, corrected for
 * any CSS scaling. The challenge images are laid out at their natural size,
 * but a narrow phone viewport scales them down, and the server grades in
 * image pixels.
 */
export function localPoint(
  event: { clientX: number; clientY: number },
  target: HTMLElement,
  naturalWidth: number,
  naturalHeight: number,
): { x: number; y: number } {
  const rect = target.getBoundingClientRect();
  const scaleX = rect.width ? naturalWidth / rect.width : 1;
  const scaleY = rect.height ? naturalHeight / rect.height : 1;
  return {
    x: (event.clientX - rect.left) * scaleX,
    y: (event.clientY - rect.top) * scaleY,
  };
}

/**
 * Tracks a drag from pointerdown to pointerup using pointer capture, which
 * keeps the gesture alive when the pointer leaves the element and handles
 * mouse, touch and pen through one code path.
 */
export function onDrag(
  handle: HTMLElement,
  handlers: {
    start?: (e: PointerEvent) => void;
    move: (e: PointerEvent) => void;
    end?: (e: PointerEvent) => void;
  },
): () => void {
  let active = false;

  const down = (e: PointerEvent) => {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    active = true;
    handle.setPointerCapture(e.pointerId);
    handlers.start?.(e);
    // Stops the browser from starting a text selection or a scroll gesture
    // in the middle of the drag.
    e.preventDefault();
  };
  const move = (e: PointerEvent) => {
    if (!active) return;
    handlers.move(e);
    e.preventDefault();
  };
  const up = (e: PointerEvent) => {
    if (!active) return;
    active = false;
    if (handle.hasPointerCapture(e.pointerId)) handle.releasePointerCapture(e.pointerId);
    handlers.end?.(e);
  };

  handle.addEventListener('pointerdown', down);
  handle.addEventListener('pointermove', move);
  handle.addEventListener('pointerup', up);
  handle.addEventListener('pointercancel', up);

  return () => {
    handle.removeEventListener('pointerdown', down);
    handle.removeEventListener('pointermove', move);
    handle.removeEventListener('pointerup', up);
    handle.removeEventListener('pointercancel', up);
  };
}

/**
 * Maps an arrow key to a step, honouring writing direction: in a right-to-left
 * locale the "forward" arrow is the left one.
 */
export function arrowStep(key: string, rtl: boolean): { dx: number; dy: number } | null {
  const flip = rtl ? -1 : 1;
  switch (key) {
    case 'ArrowLeft':
      return { dx: -1 * flip, dy: 0 };
    case 'ArrowRight':
      return { dx: 1 * flip, dy: 0 };
    case 'ArrowUp':
      return { dx: 0, dy: -1 };
    case 'ArrowDown':
      return { dx: 0, dy: 1 };
    default:
      return null;
  }
}

/** Loads an image and resolves once it is decodable, or rejects on error. */
export function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.decoding = 'async';
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error(`could not load ${src}`));
    img.src = src;
  });
}
