import type { ChallengeSpec, ChallengeView, Trace } from '../types';
import type { Translator } from '../i18n';
import { TraceRecorder } from '../telemetry';
import { arrowStep, clamp, el, onDrag } from '../ui';

/**
 * The rotation challenge: turn a disc back to upright.
 *
 * The disc can be turned either by dragging it directly, which is the natural
 * gesture, or with a slider, which is what makes it operable from a keyboard
 * and with assistive technology. Both feed the same angle.
 */
export class RotateChallenge implements ChallengeView {
  onChange?: (ready: boolean) => void;

  private readonly recorder = new TraceRecorder('pointer');
  private readonly cleanups: Array<() => void> = [];
  /** Total clockwise correction applied, in degrees. */
  private angle = 0;
  private touched = false;
  private lastPointerAngle: number | null = null;
  private lastDirection = 0;

  private disc!: HTMLImageElement;
  private handle!: HTMLElement;
  private fill!: HTMLElement;
  private track!: HTMLElement;

  constructor(
    private readonly spec: ChallengeSpec,
    private readonly assets: Record<string, string>,
    private readonly t: Translator,
  ) {}

  mount(parent: HTMLElement): void {
    const prompt = el('p', 'pc-prompt', this.t.t('rotate.prompt'));
    const stage = el('div', 'pc-stage pc-stage-disc');
    stage.style.aspectRatio = '1 / 1';

    this.disc = el('img', 'pc-disc');
    this.disc.src = this.assets.disc;
    this.disc.alt = this.t.t('rotate.prompt');
    this.disc.draggable = false;
    stage.append(this.disc);

    this.track = el('div', 'pc-track');
    this.fill = el('div', 'pc-track-fill');
    this.handle = el('div', 'pc-handle');
    this.handle.tabIndex = 0;
    this.handle.setAttribute('role', 'slider');
    this.handle.setAttribute('aria-label', this.t.t('rotate.aria'));
    this.handle.setAttribute('aria-valuemin', '0');
    this.handle.setAttribute('aria-valuemax', '360');
    this.track.append(this.fill, this.handle);

    const hint = el('p', 'pc-hint', this.t.t('rotate.hint'));
    parent.append(prompt, stage, this.track, hint);

    this.apply(0);
    this.bindDisc();
    this.bindTrack();
  }

  /** Dragging anywhere on the disc turns it, like a physical dial. */
  private bindDisc(): void {
    const angleAt = (e: PointerEvent): number => {
      const rect = this.disc.getBoundingClientRect();
      const cx = rect.left + rect.width / 2;
      const cy = rect.top + rect.height / 2;
      return (Math.atan2(e.clientY - cy, e.clientX - cx) * 180) / Math.PI;
    };

    this.cleanups.push(
      onDrag(this.disc, {
        start: (e) => {
          this.recorder.setMode(e.pointerType === 'touch' ? 'touch' : 'pointer');
          this.recorder.begin();
          this.lastPointerAngle = angleAt(e);
          if (this.touched) this.recorder.correction();
        },
        move: (e) => {
          const current = angleAt(e);
          if (this.lastPointerAngle !== null) {
            let delta = current - this.lastPointerAngle;
            // Unwrap across the discontinuity at +/-180 degrees, so a drag
            // through the bottom of the dial does not jump a half turn.
            if (delta > 180) delta -= 360;
            if (delta < -180) delta += 360;
            if (delta !== 0) {
              const direction = Math.sign(delta);
              if (this.lastDirection !== 0 && direction !== this.lastDirection) {
                this.recorder.correction();
              }
              this.lastDirection = direction;
            }
            this.apply(this.angle + delta);
          }
          this.lastPointerAngle = current;

          const rect = this.disc.getBoundingClientRect();
          this.recorder.add(e.clientX - rect.left, e.clientY - rect.top);
        },
        end: () => {
          this.lastPointerAngle = null;
          this.recorder.end();
        },
      }),
    );
  }

  private bindTrack(): void {
    this.cleanups.push(
      onDrag(this.handle, {
        start: (e) => {
          this.recorder.setMode(e.pointerType === 'touch' ? 'touch' : 'pointer');
          this.recorder.begin();
        },
        move: (e) => {
          const rect = this.track.getBoundingClientRect();
          let ratio = rect.width ? (e.clientX - rect.left) / rect.width : 0;
          if (this.t.rtl) ratio = 1 - ratio;
          this.apply(clamp(ratio, 0, 1) * 360);
          this.recorder.add(e.clientX - rect.left, e.clientY - rect.top);
        },
        end: () => this.recorder.end(),
      }),
    );

    const step = this.spec.rotate?.stepDeg ?? 2;
    const onKey = (e: KeyboardEvent) => {
      const dir = arrowStep(e.key, this.t.rtl);
      let delta = 0;
      if (dir && dir.dx !== 0) delta = dir.dx * step * (e.shiftKey ? 5 : 1);
      else if (dir && dir.dy !== 0) delta = -dir.dy * step * (e.shiftKey ? 5 : 1);
      else if (e.key === 'Home') delta = -this.angle;
      else return;

      e.preventDefault();
      this.recorder.setMode('keyboard');
      this.recorder.begin();
      this.apply(this.angle + delta);
      this.recorder.add(this.angle, 0);
      this.recorder.end();
    };
    this.handle.addEventListener('keydown', onKey);
    this.cleanups.push(() => this.handle.removeEventListener('keydown', onKey));
  }

  private apply(deg: number): void {
    // Kept in [0, 360) so the value shown to assistive technology is stable
    // however many turns the visitor made getting there.
    const next = ((deg % 360) + 360) % 360;
    if (Math.abs(next - this.angle) > 0.01 && !this.touched) {
      this.touched = true;
      this.onChange?.(true);
    }
    this.angle = next;
    this.disc.style.transform = `rotate(${next}deg)`;
    const ratio = next / 360;
    this.handle.style.left = this.t.rtl
      ? `calc(${(1 - ratio) * 100}% - ${(1 - ratio) * 44}px)`
      : `calc(${ratio * 100}% - ${ratio * 44}px)`;
    this.fill.style.width = `${ratio * 100}%`;
    this.handle.setAttribute('aria-valuenow', String(Math.round(next)));
    this.handle.setAttribute('aria-valuetext', this.t.num(Math.round(next)));
  }

  destroy(): void {
    for (const off of this.cleanups) off();
    this.cleanups.length = 0;
  }

  ready(): boolean {
    return this.touched;
  }

  answer(): unknown {
    return { angle: Math.round(this.angle * 10) / 10 };
  }

  trace(): Trace {
    return this.recorder.trace();
  }
}
