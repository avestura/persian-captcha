import type { ChallengeSpec, ChallengeView, Trace, TracePoint } from '../types';
import type { Translator } from '../i18n';
import { TraceRecorder } from '../telemetry';
import { announce, arrowStep, clamp, clear, el, localPoint } from '../ui';

/**
 * Click the named icons in the stated order.
 *
 * The prompt names shapes, not positions: the server sends identifiers such
 * as "crescent" and this code renders the localised word. The artwork itself
 * contains no text, which is why the same challenge works unchanged in
 * Persian, Arabic or any other script without the server drawing a glyph.
 *
 * Keyboard operation moves a crosshair rather than tabbing between targets,
 * because the browser is not told where the icons are; only the image knows.
 * That makes this challenge workable without a mouse but not usable without
 * sight, which is what the accessible fallback is for.
 */
export class ClickOrderChallenge implements ChallengeView {
  onChange?: (ready: boolean) => void;

  private readonly recorder = new TraceRecorder('pointer');
  private readonly cleanups: Array<() => void> = [];
  private readonly picks: TracePoint[] = [];

  private stage!: HTMLElement;
  private board!: HTMLImageElement;
  private markers!: HTMLElement;
  private crosshair!: HTMLElement;
  private progress!: HTMLElement;
  private live!: HTMLElement;

  /** Crosshair position in image coordinates, for keyboard selection. */
  private cursorX: number;
  private cursorY: number;
  private keyboardMode = false;

  constructor(
    private readonly spec: ChallengeSpec,
    private readonly assets: Record<string, string>,
    private readonly t: Translator,
  ) {
    this.cursorX = spec.width / 2;
    this.cursorY = spec.height / 2;
  }

  private get total(): number {
    return this.spec.clickOrder?.sequence.length ?? 0;
  }

  mount(parent: HTMLElement): void {
    const names = (this.spec.clickOrder?.sequence ?? []).map((s) => this.t.shape(s));
    const prompt = el('p', 'pc-prompt');
    prompt.textContent = this.t.t('click.prompt', { list: this.t.list(names) });

    this.stage = el('div', 'pc-stage pc-stage-click');
    this.stage.style.aspectRatio = `${this.spec.width} / ${this.spec.height}`;
    this.stage.tabIndex = 0;
    this.stage.setAttribute('role', 'application');
    this.stage.setAttribute('aria-label', this.t.t('click.boardAria'));

    this.board = el('img', 'pc-board');
    this.board.src = this.assets.board;
    this.board.alt = '';
    this.board.draggable = false;

    this.markers = el('div', 'pc-markers');
    this.crosshair = el('div', 'pc-crosshair');
    this.crosshair.hidden = true;

    this.stage.append(this.board, this.markers, this.crosshair);

    this.progress = el('p', 'pc-progress');
    this.live = el('div', 'pc-sr-only');
    this.live.setAttribute('aria-live', 'polite');

    const reset = el('button', 'pc-link', this.t.t('click.reset'));
    reset.type = 'button';
    reset.addEventListener('click', () => this.reset());

    const hint = el('p', 'pc-hint', this.t.t('click.hint'));

    parent.append(prompt, this.stage, this.progress, hint, reset, this.live);
    this.updateProgress();
    this.bind();
  }

  private bind(): void {
    const onPointerDown = (e: PointerEvent) => {
      this.recorder.setMode(e.pointerType === 'touch' ? 'touch' : 'pointer');
      this.recorder.begin();
    };
    const onPointerMove = (e: PointerEvent) => {
      const p = localPoint(e, this.board, this.spec.width, this.spec.height);
      this.recorder.add(p.x, p.y);
    };
    const onClick = (e: MouseEvent) => {
      const p = localPoint(e, this.board, this.spec.width, this.spec.height);
      this.select(p.x, p.y);
    };
    const onKey = (e: KeyboardEvent) => this.handleKey(e);

    this.stage.addEventListener('pointerdown', onPointerDown);
    this.stage.addEventListener('pointermove', onPointerMove, { passive: true });
    this.stage.addEventListener('click', onClick);
    this.stage.addEventListener('keydown', onKey);

    this.cleanups.push(() => {
      this.stage.removeEventListener('pointerdown', onPointerDown);
      this.stage.removeEventListener('pointermove', onPointerMove);
      this.stage.removeEventListener('click', onClick);
      this.stage.removeEventListener('keydown', onKey);
    });
  }

  private handleKey(e: KeyboardEvent): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!this.keyboardMode) return;
      this.recorder.setMode('keyboard');
      this.select(this.cursorX, this.cursorY);
      return;
    }
    if (e.key === 'Backspace') {
      e.preventDefault();
      this.undo();
      return;
    }
    const step = arrowStep(e.key, false);
    if (!step) return;
    e.preventDefault();

    if (!this.keyboardMode) {
      this.keyboardMode = true;
      this.crosshair.hidden = false;
      this.recorder.setMode('keyboard');
      this.recorder.begin();
    }
    const distance = e.shiftKey ? 16 : 4;
    this.cursorX = clamp(this.cursorX + step.dx * distance, 0, this.spec.width);
    this.cursorY = clamp(this.cursorY + step.dy * distance, 0, this.spec.height);
    this.recorder.add(this.cursorX, this.cursorY);
    this.placeCrosshair();
  }

  private placeCrosshair(): void {
    this.crosshair.style.left = `${(this.cursorX / this.spec.width) * 100}%`;
    this.crosshair.style.top = `${(this.cursorY / this.spec.height) * 100}%`;
    // The position is announced through the live region rather than by
    // relabelling the board, so a screen reader reads the movement instead of
    // re-announcing the whole control.
    announce(
      this.live,
      this.t.t('click.crosshairAria', {
        x: Math.round(this.cursorX),
        y: Math.round(this.cursorY),
      }),
    );
  }

  private select(x: number, y: number): void {
    if (this.picks.length >= this.total) return;
    this.recorder.add(x, y);
    this.picks.push({ x, y, t: 0 });

    const marker = el('span', 'pc-marker', this.t.num(this.picks.length));
    marker.style.left = `${(x / this.spec.width) * 100}%`;
    marker.style.top = `${(y / this.spec.height) * 100}%`;
    this.markers.append(marker);

    this.updateProgress();
    if (this.picks.length === this.total) this.recorder.end();
    this.onChange?.(this.ready());
  }

  private undo(): void {
    if (this.picks.length === 0) return;
    this.picks.pop();
    this.markers.lastElementChild?.remove();
    this.recorder.correction();
    this.updateProgress();
    this.onChange?.(this.ready());
  }

  private reset(): void {
    this.picks.length = 0;
    clear(this.markers);
    this.recorder.correction();
    this.updateProgress();
    this.onChange?.(false);
  }

  private updateProgress(): void {
    const text = this.t.t('click.progress', { done: this.picks.length, total: this.total });
    this.progress.textContent = text;
    announce(this.live, text);
  }

  destroy(): void {
    for (const off of this.cleanups) off();
    this.cleanups.length = 0;
  }

  ready(): boolean {
    return this.picks.length === this.total && this.total > 0;
  }

  answer(): unknown {
    return { points: this.picks.map((p) => ({ x: p.x, y: p.y })) };
  }

  trace(): Trace {
    return this.recorder.trace();
  }
}
