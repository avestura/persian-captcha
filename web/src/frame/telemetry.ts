import type { Signals, Trace, TracePoint } from './types';

/**
 * Collection of the two things the server scores: the passive environment,
 * and the motion the visitor produced.
 *
 * The environment set is deliberately narrow. There is no canvas, WebGL or
 * audio fingerprinting here, no font probing and nothing that could build a
 * stable identifier for a person across sites. Everything collected is
 * discarded with the session a few minutes later.
 */

let pointerMoves = 0;
const loadedAt = performance.now();

/** Starts the ambient counters. Called once when the frame boots. */
export function watchEnvironment(): void {
  window.addEventListener(
    'pointermove',
    () => {
      // Capped so a page that moves the pointer constantly cannot make this
      // number meaningless, and so the counter cannot overflow.
      if (pointerMoves < 10_000) pointerMoves++;
    },
    { passive: true },
  );
}

/** Snapshots the environment for the opening assessment. */
export function collectSignals(): Signals {
  const nav = navigator as Navigator & { webdriver?: boolean; hardwareConcurrency?: number };
  return {
    webdriver: nav.webdriver === true,
    touch: (navigator.maxTouchPoints ?? 0) > 0 || 'ontouchstart' in window,
    cores: nav.hardwareConcurrency ?? 0,
    dpr: window.devicePixelRatio || 0,
    sw: screen?.width ?? 0,
    sh: screen?.height ?? 0,
    vw: window.innerWidth || 0,
    vh: window.innerHeight || 0,
    langs: navigator.languages?.length ?? 0,
    moves: pointerMoves,
    dwell: Math.round(performance.now() - loadedAt),
  };
}

/** How many samples are kept. The server caps at 600; stay under it. */
const MAX_POINTS = 550;

/**
 * Records an interaction as a series of timestamped positions.
 *
 * The recorder does not thin or smooth the data. The value of the trace is
 * precisely in its irregularity: the jitter, the overshoot and the uneven
 * frame timing are what distinguish a hand from a script, and any tidying
 * here would erase the signal the server is looking for.
 */
export class TraceRecorder {
  private points: TracePoint[] = [];
  private corrections = 0;
  private startedAt = 0;
  private endedAt = 0;
  private origin = performance.now();

  constructor(private mode: Trace['mode'] = 'pointer') {}

  /** Switches the declared input mode, for example on a touch start. */
  setMode(mode: Trace['mode']): void {
    this.mode = mode;
  }

  /** Marks the beginning of an interaction. */
  begin(): void {
    if (this.startedAt === 0) this.startedAt = performance.now() - this.origin;
  }

  /** Records a position. Coordinates are in challenge-image pixels. */
  add(x: number, y: number): void {
    if (this.points.length >= MAX_POINTS) return;
    this.points.push({
      x: round2(x),
      y: round2(y),
      t: Math.round(performance.now() - this.origin),
    });
  }

  /** Records that the visitor let go and grabbed again, or reversed. */
  correction(): void {
    this.corrections++;
  }

  /** Marks the end of an interaction. */
  end(): void {
    this.endedAt = performance.now() - this.origin;
  }

  /** Clears everything, for a fresh challenge in the same session. */
  reset(): void {
    this.points = [];
    this.corrections = 0;
    this.startedAt = 0;
    this.endedAt = 0;
    this.origin = performance.now();
  }

  trace(): Trace {
    return {
      mode: this.mode,
      points: this.points,
      startT: Math.round(this.startedAt),
      endT: Math.round(this.endedAt || performance.now() - this.origin),
      corrections: this.corrections,
    };
  }

  get sampleCount(): number {
    return this.points.length;
  }
}

/**
 * Keeps two decimals. Pointer events on high-DPI screens carry fractional
 * coordinates, and that fractional part is itself a signal, so it must
 * survive the round trip.
 */
function round2(v: number): number {
  return Math.round(v * 100) / 100;
}
