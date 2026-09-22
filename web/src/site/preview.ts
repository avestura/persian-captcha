/**
 * The interactive challenge previews on the marketing site.
 *
 * Every puzzle on that page is the component the service actually ships:
 * these are the same classes the widget loads inside its iframe, mounted
 * against a fixed set of boards, discs and pieces that `go run ./cmd/preview`
 * rendered once from seed 7. Nothing here re-implements a challenge, so a
 * change to how a puzzle behaves shows up on the marketing page the next time
 * the bundles are built, and a preview that looks wrong is a real bug.
 *
 * What *is* re-implemented is the grader, because the page has no service
 * behind it. `grade` below is a port of the `check` methods in
 * internal/challenge, tolerances and all, and it runs in the browser with the
 * solution sitting in the same bundle.
 *
 * That is the one thing the preview does differently from the product, and it
 * matters enough that the page says so in plain words: the real flow never
 * lets the browser near the answer. The puzzle is generated server-side, the
 * artwork is drawn from a seed the browser never sees, and the submission is
 * graded by /v1/solve. A preview that phoned home for every drag would need a
 * server and a rate limiter to survive a front page, which is not what a
 * marketing page is for.
 */

import { Translator } from '../frame/i18n';
import type { ChallengeSpec, ChallengeView, LocaleBundle, LocaleMeta } from '../frame/types';
import { SliderChallenge } from '../frame/challenges/slider';
import { RotateChallenge } from '../frame/challenges/rotate';
import { ClickOrderChallenge } from '../frame/challenges/clickorder';
import { DragDropChallenge } from '../frame/challenges/dragdrop';
import { AccessibleChallenge } from '../frame/challenges/accessible';
import { el } from '../frame/ui';

// The locale bundles are the service's own, imported from the Go tree rather
// than copied, so a new message never has to be added twice.
import enLocale from '../../../internal/i18n/locales/en.json';
import faLocale from '../../../internal/i18n/locales/fa.json';

// The fixtures live beside the artwork they describe, in site/assets/preview,
// and are refreshed together by `make site-fixtures`. The .spec files are what
// the server would send the browser; the .state files are what it would keep
// to itself.
import sliderSpec from '../../../site/assets/preview/slider_jigsaw.spec.json';
import sliderState from '../../../site/assets/preview/slider_jigsaw.state.json';
import rotateSpec from '../../../site/assets/preview/rotate.spec.json';
import rotateState from '../../../site/assets/preview/rotate.state.json';
import clickSpec from '../../../site/assets/preview/click_order.spec.json';
import clickState from '../../../site/assets/preview/click_order.state.json';
import dragSpec from '../../../site/assets/preview/drag_drop.spec.json';
import dragState from '../../../site/assets/preview/drag_drop.state.json';
import accessSpec from '../../../site/assets/preview/accessible.spec.json';
import accessState from '../../../site/assets/preview/accessible.state.json';

type Kind = 'slider_jigsaw' | 'rotate' | 'click_order' | 'drag_drop' | 'accessible';

const ASSET_BASE = 'assets/preview/';

interface Fixture {
  spec: ChallengeSpec;
  state: PreviewState;
  assets: Record<string, string>;
}

/**
 * The private side of a challenge, as internal/challenge writes it out. Only
 * the fields the grader needs are declared; the artwork parameters that make
 * up the rest are what the Go renderer used and are of no interest here.
 */
interface PreviewState {
  slider?: { targetX: number };
  rotate?: { offsetDeg: number };
  click?: { icons: Array<{ x: number; y: number }>; seq: number[]; radius: number };
  drag?: {
    slots: Array<{ x: number; y: number }>;
    pieces: Array<{ slot: number }>;
    radius: number;
    pieceSize: number;
  };
  access?: { options: Array<{ id: string }>; answerIdx: number };
}

/** The fixtures, in the order the tab strip shows them. */
const FIXTURES: Record<Kind, Fixture> = {
  slider_jigsaw: {
    spec: sliderSpec as unknown as ChallengeSpec,
    state: sliderState as unknown as PreviewState,
    assets: {
      board: ASSET_BASE + 'slider_jigsaw-board.png',
      piece: ASSET_BASE + 'slider_jigsaw-piece.png',
    },
  },
  rotate: {
    spec: rotateSpec as unknown as ChallengeSpec,
    state: rotateState as unknown as PreviewState,
    assets: { disc: ASSET_BASE + 'rotate-disc.png' },
  },
  click_order: {
    spec: clickSpec as unknown as ChallengeSpec,
    state: clickState as unknown as PreviewState,
    assets: { board: ASSET_BASE + 'click_order-board.png' },
  },
  drag_drop: {
    spec: dragSpec as unknown as ChallengeSpec,
    state: dragState as unknown as PreviewState,
    assets: {
      board: ASSET_BASE + 'drag_drop-board.png',
      piece0: ASSET_BASE + 'drag_drop-piece0.png',
      piece1: ASSET_BASE + 'drag_drop-piece1.png',
      piece2: ASSET_BASE + 'drag_drop-piece2.png',
    },
  },
  accessible: {
    spec: accessSpec as unknown as ChallengeSpec,
    state: accessState as unknown as PreviewState,
    assets: {},
  },
};

const BUNDLES: Record<string, LocaleBundle> = {
  en: bundle('en', enLocale),
  fa: bundle('fa', faLocale),
};

function bundle(tag: string, raw: unknown): LocaleBundle {
  const source = raw as { meta: LocaleMeta; messages: Record<string, string> };
  return { tag, meta: source.meta, messages: source.messages };
}

// ---- grading ---------------------------------------------------------------

/**
 * The tolerances for the "normal" difficulty, which is what cmd/preview
 * generated. They are the middle column of the `pick` tables in
 * internal/challenge; easy and hard widen and narrow them respectively, and
 * the site has no control to switch between levels, so only one column is
 * needed here.
 */
const TOLERANCE = {
  sliderPx: 6,
  rotateDeg: 10,
  clickRadiusFactor: 1.3,
  dragRadiusFactor: 0.8,
};

interface Answered {
  correct: boolean;
  /** A short tag matching the `Reason` the Go grader would have returned. */
  reason: string;
}

function grade(kind: Kind, state: PreviewState, answer: unknown): Answered {
  switch (kind) {
    case 'slider_jigsaw': {
      const a = answer as { x: number };
      const d = Math.abs(a.x - state.slider!.targetX);
      return { correct: d <= TOLERANCE.sliderPx, reason: 'offset' };
    }
    case 'rotate': {
      const a = answer as { angle: number };
      // Correct when the visitor's correction cancels the generated offset.
      const d = angularDistance(state.rotate!.offsetDeg + a.angle);
      return { correct: d <= TOLERANCE.rotateDeg, reason: 'angle' };
    }
    case 'click_order': {
      const a = answer as { points: Array<{ x: number; y: number }> };
      const c = state.click!;
      if (a.points.length !== c.seq.length) return { correct: false, reason: 'wrong_count' };
      const tol = c.radius * TOLERANCE.clickRadiusFactor;
      for (let i = 0; i < c.seq.length; i++) {
        const want = c.icons[c.seq[i]];
        if (Math.hypot(a.points[i].x - want.x, a.points[i].y - want.y) > tol) {
          return { correct: false, reason: 'wrong_icon' };
        }
      }
      return { correct: true, reason: 'sequence' };
    }
    case 'drag_drop': {
      const a = answer as { placements: Array<{ id: number; x: number; y: number }> };
      const d = state.drag!;
      if (a.placements.length !== d.pieces.length) {
        return { correct: false, reason: 'wrong_count' };
      }
      const tol = d.radius * TOLERANCE.dragRadiusFactor;
      const half = d.pieceSize / 2;
      for (const placement of a.placements) {
        const slot = d.slots[d.pieces[placement.id].slot];
        if (Math.hypot(placement.x + half - slot.x, placement.y + half - slot.y) > tol) {
          return { correct: false, reason: 'misplaced' };
        }
      }
      return { correct: true, reason: 'placements' };
    }
    case 'accessible': {
      const a = answer as { choice: string | null };
      const want = state.access!.options[state.access!.answerIdx].id;
      return { correct: a.choice === want, reason: 'choice' };
    }
  }
}

/** How far a is from a whole number of turns, in degrees, always in [0, 180]. */
function angularDistance(a: number): number {
  const d = Math.abs(a) % 360;
  return d > 180 ? 360 - d : d;
}

function makeChallenge(kind: Kind, t: Translator): ChallengeView {
  const { spec, assets } = FIXTURES[kind];
  switch (kind) {
    case 'slider_jigsaw':
      return new SliderChallenge(spec, assets, t);
    case 'rotate':
      return new RotateChallenge(spec, assets, t);
    case 'click_order':
      return new ClickOrderChallenge(spec, assets, t);
    case 'drag_drop':
      return new DragDropChallenge(spec, assets, t);
    case 'accessible':
      return new AccessibleChallenge(spec, t);
  }
}

// ---- the panel -------------------------------------------------------------

/**
 * One mounted preview.
 *
 * The DOM it builds mirrors the frame's own panel closely enough that the
 * page is showing the real thing: same classes, same stylesheet, same
 * structure of head, body and footer. What it leaves out is everything that
 * needs a session — the attempt counter, the close button, the proof of work
 * the confirm button waits on.
 */
class Preview {
  private view: ChallengeView | null = null;
  private tag: string;

  constructor(
    private readonly host: HTMLElement,
    private kind: Kind,
    private readonly onKindChange: (kind: Kind) => void,
  ) {
    this.tag = 'en';
  }

  show(kind: Kind): void {
    this.kind = kind;
    this.render();
  }

  private get t(): Translator {
    return new Translator(BUNDLES[this.tag]);
  }

  private render(): void {
    const t = this.t;
    this.destroyView();
    this.host.textContent = '';

    const panel = el('div', 'pc-panel');
    panel.dir = t.dir;
    panel.lang = t.tag;
    panel.setAttribute('role', 'group');
    panel.setAttribute('aria-label', t.t('challenge.title'));

    const head = el('div', 'pc-panel-head');
    head.append(el('h3', 'pc-title', t.t('challenge.title')));

    const tools = el('div', 'pc-tools');
    tools.append(this.localePicker(t));
    const again = el('button', 'pc-icon-button pc-icon-button-refresh');
    again.type = 'button';
    again.title = t.t('challenge.refresh');
    again.setAttribute('aria-label', t.t('challenge.refresh'));
    // A reroll on the real service asks for a new puzzle. There is only one
    // of each here, so this starts the same one over, which is what the
    // visitor wants from a preview anyway.
    again.addEventListener('click', () => this.render());
    tools.append(again);
    head.append(tools);

    const confirm = el('button', 'pc-confirm', t.t('challenge.submit'));
    confirm.type = 'button';
    confirm.disabled = true;

    const body = el('div', 'pc-panel-body');
    const view = makeChallenge(this.kind, t);
    view.onChange = (ready) => {
      confirm.disabled = !ready;
    };
    view.mount(body);
    confirm.disabled = !view.ready();
    this.view = view;

    const foot = el('div', 'pc-panel-foot');
    const notes = el('div', 'pc-notes');
    const live = el('p', 'pc-error');
    live.setAttribute('aria-live', 'polite');
    notes.append(live);

    // The fallback link is the real one: on the service it swaps the puzzle
    // for the accessible question, and here it swaps the tab.
    const other: Kind = this.kind === 'accessible' ? 'slider_jigsaw' : 'accessible';
    const swap = el(
      'button',
      'pc-link',
      this.kind === 'accessible' ? t.t('challenge.useVisual') : t.t('challenge.useAccessible'),
    );
    swap.type = 'button';
    swap.addEventListener('click', () => this.onKindChange(other));
    notes.append(swap);

    confirm.addEventListener('click', () => {
      const outcome = grade(this.kind, FIXTURES[this.kind].state, view.answer());
      if (outcome.correct) {
        this.renderSolved();
        return;
      }
      live.textContent = t.t('challenge.incorrect');
      // The service would also weigh the motion that produced this answer and
      // could refuse a correct one delivered too cleanly. The preview grades
      // the geometry alone; view.trace() is collected all the same, because
      // the components record it whether or not anything reads it.
      void view.trace();
    });

    foot.append(notes, confirm);
    panel.append(head, body, foot);
    this.host.append(panel);
  }

  /** The row the visitor is left with once the puzzle is solved. */
  private renderSolved(): void {
    const t = this.t;
    this.destroyView();
    this.host.textContent = '';

    const row = el('div', 'pc-row pc-row-success');
    const icon = el('span', 'pc-icon pc-icon-success');
    icon.setAttribute('aria-hidden', 'true');
    const label = el('span', 'pc-label', t.t('widget.verified'));
    label.setAttribute('role', 'status');

    const again = el('button', 'pc-link', t.t('challenge.refresh'));
    again.type = 'button';
    again.addEventListener('click', () => this.render());

    const brand = el('span', 'pc-brand', t.t('widget.brand'));
    brand.setAttribute('aria-hidden', 'true');

    row.append(icon, label, again, brand);
    this.host.append(row);
  }

  private localePicker(t: Translator): HTMLElement {
    const select = el('select', 'pc-locale');
    select.setAttribute('aria-label', 'Language');
    for (const [tag, b] of Object.entries(BUNDLES)) {
      const option = el('option');
      option.value = tag;
      option.textContent = b.meta.name;
      option.selected = tag === t.tag;
      select.append(option);
    }
    select.addEventListener('change', () => {
      this.tag = select.value;
      this.render();
    });
    return select;
  }

  private destroyView(): void {
    this.view?.destroy();
    this.view = null;
  }
}

// ---- page wiring -----------------------------------------------------------

function start(): void {
  const host = document.querySelector<HTMLElement>('[data-preview-stage]');
  if (!host) return;

  const tabs = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-preview-tab]'));
  const notes = Array.from(document.querySelectorAll<HTMLElement>('[data-preview-note]'));
  const first = (tabs[0]?.dataset.previewTab as Kind) ?? 'slider_jigsaw';

  const preview = new Preview(host, first, (kind) => select(kind, true));

  function select(kind: Kind, focus: boolean): void {
    for (const tab of tabs) {
      const active = tab.dataset.previewTab === kind;
      tab.setAttribute('aria-selected', String(active));
      // Roving tabindex: the strip is one stop, and the arrow keys move
      // within it.
      tab.tabIndex = active ? 0 : -1;
      if (active && focus) tab.focus();
    }
    for (const note of notes) {
      note.hidden = note.dataset.previewNote !== kind;
    }
    preview.show(kind);
  }

  tabs.forEach((tab, i) => {
    tab.addEventListener('click', () => select(tab.dataset.previewTab as Kind, false));
    tab.addEventListener('keydown', (e) => {
      const delta = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
      let next = -1;
      if (delta !== 0) next = (i + delta + tabs.length) % tabs.length;
      else if (e.key === 'Home') next = 0;
      else if (e.key === 'End') next = tabs.length - 1;
      if (next < 0) return;
      e.preventDefault();
      select(tabs[next].dataset.previewTab as Kind, true);
    });
  });

  select(first, false);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', start);
} else {
  start();
}
