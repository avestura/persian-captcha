import type { ChallengeSpec, ChallengeView, Trace } from '../types';
import type { Translator } from '../i18n';
import { TraceRecorder } from '../telemetry';
import { arrowStep, clamp, el, localPoint, onDrag } from '../ui';

/**
 * The jigsaw slider: a puzzle piece is dragged along a track until it drops
 * into the notch cut out of the background.
 *
 * Note that the track is not mirrored in right-to-left locales, even though
 * the rest of the widget is. The piece moves left to right in the image
 * regardless of language, and a mirrored track would mean dragging the handle
 * leftwards to send the piece rightwards. Everything else about the panel
 * mirrors; the physical gesture stays aligned with what it moves.
 */
export class SliderChallenge implements ChallengeView {
  onChange?: (ready: boolean) => void;

  private readonly recorder = new TraceRecorder('pointer');
  private readonly cleanups: Array<() => void> = [];
  private position = 0;
  private moved = false;

  private board!: HTMLImageElement;
  private piece!: HTMLImageElement;
  private handle!: HTMLElement;
  private fill!: HTMLElement;
  private track!: HTMLElement;

  constructor(
    private readonly spec: ChallengeSpec,
    private readonly assets: Record<string, string>,
    private readonly t: Translator,
  ) {}

  mount(parent: HTMLElement): void {
    const s = this.spec.slider!;

    const prompt = el('p', 'pc-prompt', this.t.t('slider.prompt'));
    const stage = el('div', 'pc-stage');
    stage.style.aspectRatio = `${this.spec.width} / ${this.spec.height}`;

    this.board = el('img', 'pc-board');
    this.board.src = this.assets.board;
    this.board.alt = this.t.t('slider.prompt');
    this.board.draggable = false;

    this.piece = el('img', 'pc-piece');
    this.piece.src = this.assets.piece;
    this.piece.alt = '';
    this.piece.draggable = false;
    // Positions are percentages of the board so the piece tracks the image
    // when a narrow viewport scales it down.
    this.piece.style.top = `${(s.pieceTop / this.spec.height) * 100}%`;
    this.piece.style.width = `${(s.pieceSize / this.spec.width) * 100}%`;

    stage.append(this.board, this.piece);

    this.track = el('div', 'pc-track');
    this.fill = el('div', 'pc-track-fill');
    this.handle = el('div', 'pc-handle');
    this.handle.tabIndex = 0;
    this.handle.setAttribute('role', 'slider');
    this.handle.setAttribute('aria-label', this.t.t('slider.aria'));
    this.handle.setAttribute('aria-valuemin', '0');
    this.handle.setAttribute('aria-valuemax', String(s.travelMax));
    this.handle.setAttribute('aria-orientation', 'horizontal');
    this.track.append(this.fill, this.handle);

    const hint = el('p', 'pc-hint', this.t.t('slider.hint'));

    parent.append(prompt, stage, this.track, hint);
    this.apply(0);
    this.bind();
  }

  private bind(): void {
    const s = this.spec.slider!;

    this.cleanups.push(
      onDrag(this.handle, {
        start: (e) => {
          this.recorder.setMode(e.pointerType === 'touch' ? 'touch' : 'pointer');
          this.recorder.begin();
          if (this.moved) this.recorder.correction();
        },
        move: (e) => {
          const rect = this.track.getBoundingClientRect();
          const ratio = rect.width ? (e.clientX - rect.left) / rect.width : 0;
          this.apply(clamp(ratio, 0, 1) * s.travelMax);
          // The trace records the pointer against the board, not the derived
          // slider value, so vertical wobble survives: hands drift, scripts
          // that drive a one-dimensional slider do not.
          const p = localPoint(e, this.board, this.spec.width, this.spec.height);
          this.recorder.add(p.x, p.y);
        },
        end: () => this.recorder.end(),
      }),
    );

    const onKey = (e: KeyboardEvent) => {
      const step = arrowStep(e.key, false);
      let delta = 0;
      if (step && step.dx !== 0) delta = step.dx * (e.shiftKey ? 10 : 1);
      else if (e.key === 'Home') delta = -s.travelMax;
      else if (e.key === 'End') delta = s.travelMax;
      else if (e.key === 'PageUp') delta = 10;
      else if (e.key === 'PageDown') delta = -10;
      else return;

      e.preventDefault();
      this.recorder.setMode('keyboard');
      this.recorder.begin();
      this.apply(this.position + delta);
      this.recorder.add(this.position, s.pieceTop);
      this.recorder.end();
    };
    this.handle.addEventListener('keydown', onKey);
    this.cleanups.push(() => this.handle.removeEventListener('keydown', onKey));
  }

  /** Moves the piece and the handle to a track position in image pixels. */
  private apply(x: number): void {
    const s = this.spec.slider!;
    const next = clamp(Math.round(x * 100) / 100, 0, s.travelMax);
    if (next !== this.position) {
      this.position = next;
      if (!this.moved) {
        this.moved = true;
        this.onChange?.(true);
      }
    }
    const ratio = s.travelMax > 0 ? this.position / s.travelMax : 0;
    this.piece.style.left = `${(this.position / this.spec.width) * 100}%`;
    this.handle.style.insetInlineStart = '';
    this.handle.style.left = `calc(${ratio * 100}% - ${ratio * 44}px)`;
    this.fill.style.width = `${ratio * 100}%`;
    this.handle.setAttribute('aria-valuenow', String(Math.round(this.position)));
  }

  destroy(): void {
    for (const off of this.cleanups) off();
    this.cleanups.length = 0;
  }

  ready(): boolean {
    return this.moved;
  }

  answer(): unknown {
    return { x: this.position };
  }

  trace(): Trace {
    return this.recorder.trace();
  }
}
