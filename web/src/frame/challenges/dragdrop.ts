import type { ChallengeSpec, ChallengeView, DragPieceSpec, Trace } from '../types';
import type { Translator } from '../i18n';
import { TraceRecorder } from '../telemetry';
import { announce, arrowStep, clamp, el, localPoint, onDrag } from '../ui';

interface PieceState {
  spec: DragPieceSpec;
  node: HTMLImageElement;
  x: number;
  y: number;
  placed: boolean;
}

/**
 * Drag each loose shape into the matching outline.
 *
 * This produces the richest interaction data of the four challenges, because
 * each piece contributes a separate gesture with its own approach and
 * release. It is also the slowest to complete, which is why it is only one of
 * the kinds in rotation rather than the default.
 *
 * Keyboard operation is a pick-up-and-move model: Tab to a piece, Enter or
 * Space to lift it, arrow keys to move, Enter again to drop.
 */
export class DragDropChallenge implements ChallengeView {
  onChange?: (ready: boolean) => void;

  private readonly recorder = new TraceRecorder('pointer');
  private readonly cleanups: Array<() => void> = [];
  private readonly pieces: PieceState[] = [];

  private stage!: HTMLElement;
  private board!: HTMLImageElement;
  private progress!: HTMLElement;
  private live!: HTMLElement;
  private carried: PieceState | null = null;

  constructor(
    private readonly spec: ChallengeSpec,
    private readonly assets: Record<string, string>,
    private readonly t: Translator,
  ) {}

  mount(parent: HTMLElement): void {
    const dd = this.spec.dragDrop!;

    const prompt = el('p', 'pc-prompt', this.t.t('drag.prompt'));

    this.stage = el('div', 'pc-stage pc-stage-drag');
    this.stage.style.aspectRatio = `${this.spec.width} / ${this.spec.height}`;

    this.board = el('img', 'pc-board');
    this.board.src = this.assets.board;
    this.board.alt = this.t.t('drag.prompt');
    this.board.draggable = false;
    this.stage.append(this.board);

    for (const pieceSpec of dd.pieces) {
      const node = el('img', 'pc-drag-piece');
      node.src = this.assets[pieceSpec.asset];
      node.draggable = false;
      node.tabIndex = 0;
      node.style.width = `${(pieceSpec.size / this.spec.width) * 100}%`;
      node.setAttribute('role', 'button');

      const state: PieceState = {
        spec: pieceSpec,
        node,
        x: pieceSpec.startX,
        y: pieceSpec.startY,
        placed: false,
      };
      this.pieces.push(state);
      this.stage.append(node);
      this.position(state);
      this.bindPiece(state);
    }

    this.progress = el('p', 'pc-progress');
    this.live = el('div', 'pc-sr-only');
    this.live.setAttribute('aria-live', 'polite');
    const hint = el('p', 'pc-hint', this.t.t('drag.hint'));

    parent.append(prompt, this.stage, this.progress, hint, this.live);
    this.updateProgress();
  }

  private bindPiece(state: PieceState): void {
    let grabDX = 0;
    let grabDY = 0;

    this.cleanups.push(
      onDrag(state.node, {
        start: (e) => {
          this.recorder.setMode(e.pointerType === 'touch' ? 'touch' : 'pointer');
          this.recorder.begin();
          if (state.placed) this.recorder.correction();
          const p = localPoint(e, this.board, this.spec.width, this.spec.height);
          grabDX = p.x - state.x;
          grabDY = p.y - state.y;
          state.node.classList.add('pc-dragging');
          // Bring the carried piece above the others so it is never hidden
          // behind one already sitting in its slot.
          state.node.style.zIndex = '3';
        },
        move: (e) => {
          const p = localPoint(e, this.board, this.spec.width, this.spec.height);
          this.moveTo(state, p.x - grabDX, p.y - grabDY);
          this.recorder.add(p.x, p.y);
        },
        end: () => {
          state.node.classList.remove('pc-dragging');
          state.node.style.zIndex = '';
          this.recorder.end();
          this.settle(state);
        },
      }),
    );

    const onKey = (e: KeyboardEvent) => this.handleKey(e, state);
    state.node.addEventListener('keydown', onKey);
    this.cleanups.push(() => state.node.removeEventListener('keydown', onKey));
  }

  private handleKey(e: KeyboardEvent, state: PieceState): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (this.carried === state) {
        this.carried = null;
        state.node.classList.remove('pc-carried');
        this.settle(state);
        announce(this.live, this.t.t('drag.dropped', { shape: this.t.shape(state.spec.shape) }));
      } else {
        this.carried?.node.classList.remove('pc-carried');
        this.carried = state;
        state.node.classList.add('pc-carried');
        this.recorder.setMode('keyboard');
        this.recorder.begin();
        announce(this.live, this.t.t('drag.picked', { shape: this.t.shape(state.spec.shape) }));
      }
      return;
    }
    const step = arrowStep(e.key, false);
    if (!step || this.carried !== state) return;
    e.preventDefault();

    const distance = e.shiftKey ? 10 : 2;
    this.moveTo(state, state.x + step.dx * distance, state.y + step.dy * distance);
    this.recorder.add(state.x, state.y);
    announce(
      this.live,
      this.t.t('drag.pieceAria', {
        shape: this.t.shape(state.spec.shape),
        x: Math.round(state.x),
        y: Math.round(state.y),
      }),
    );
  }

  private moveTo(state: PieceState, x: number, y: number): void {
    state.x = clamp(x, 0, this.spec.width - state.spec.size);
    state.y = clamp(y, 0, this.spec.height - state.spec.size);
    this.position(state);
  }

  private position(state: PieceState): void {
    state.node.style.left = `${(state.x / this.spec.width) * 100}%`;
    state.node.style.top = `${(state.y / this.spec.height) * 100}%`;
    state.node.setAttribute(
      'aria-label',
      this.t.t('drag.pieceAria', {
        shape: this.t.shape(state.spec.shape),
        x: Math.round(state.x),
        y: Math.round(state.y),
      }),
    );
  }

  /** A piece counts as placed once it has left the starting tray. */
  private settle(state: PieceState): void {
    const trayY = this.spec.dragDrop!.trayY;
    const wasPlaced = state.placed;
    state.placed = state.y + state.spec.size / 2 < trayY;
    state.node.classList.toggle('pc-placed', state.placed);
    if (wasPlaced !== state.placed) this.updateProgress();
    this.onChange?.(this.ready());
  }

  private updateProgress(): void {
    const done = this.pieces.filter((p) => p.placed).length;
    const text = this.t.t('drag.progress', { done, total: this.pieces.length });
    this.progress.textContent = text;
    announce(this.live, text);
  }

  destroy(): void {
    for (const off of this.cleanups) off();
    this.cleanups.length = 0;
  }

  ready(): boolean {
    return this.pieces.length > 0 && this.pieces.every((p) => p.placed);
  }

  answer(): unknown {
    return {
      placements: this.pieces.map((p) => ({
        id: p.spec.id,
        x: Math.round(p.x * 10) / 10,
        y: Math.round(p.y * 10) / 10,
      })),
    };
  }

  trace(): Trace {
    return this.recorder.trace();
  }
}
