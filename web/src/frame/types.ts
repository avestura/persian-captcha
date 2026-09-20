/**
 * Wire types shared with the Go service.
 *
 * These mirror the structs in internal/api and internal/challenge. They are
 * hand-written rather than generated, so any change on the Go side needs a
 * matching change here; the shapes are small enough that this is cheaper than
 * a code generation step in the build.
 */

export interface LocaleMeta {
  name: string;
  dir: 'ltr' | 'rtl';
  /** "latn" for 0-9, "arabext" for the Persian digits. */
  digits: string;
  font?: string;
}

export interface LocaleBundle {
  tag: string;
  meta: LocaleMeta;
  messages: Record<string, string>;
}

export interface LocaleChoice {
  tag: string;
  name: string;
  dir: string;
}

/** The object the frame document is bootstrapped with. */
export interface Boot {
  sitekey: string;
  base: string;
  theme: 'light' | 'dark' | 'auto';
  parentOrigin: string;
  locale: LocaleBundle;
  locales: LocaleChoice[];
}

export interface PowSpec {
  alg: string;
  prefix: string;
  bits: number;
}

export interface SessionResponse {
  sid: string;
  pow: PowSpec;
  locale: string;
  ttl: number;
}

export interface SliderSpec {
  pieceSize: number;
  pieceTop: number;
  travelMax: number;
}

export interface RotateSpec {
  radius: number;
  stepDeg: number;
}

export interface ClickOrderSpec {
  sequence: string[];
  radius: number;
}

export interface DragPieceSpec {
  id: number;
  shape: string;
  asset: string;
  startX: number;
  startY: number;
  size: number;
}

export interface DragDropSpec {
  pieces: DragPieceSpec[];
  trayY: number;
}

export interface AccessibleOption {
  id: string;
  labelKey?: string;
  number?: number;
}

export interface AccessibleSpec {
  promptKey: string;
  args?: Record<string, number>;
  options: AccessibleOption[];
}

export type ChallengeKind =
  | 'slider_jigsaw'
  | 'rotate'
  | 'click_order'
  | 'drag_drop'
  | 'accessible';

export interface ChallengeSpec {
  kind: ChallengeKind;
  width: number;
  height: number;
  slider?: SliderSpec;
  rotate?: RotateSpec;
  clickOrder?: ClickOrderSpec;
  dragDrop?: DragDropSpec;
  accessible?: AccessibleSpec;
}

export type FlowStatus = 'pass' | 'challenge' | 'retry' | 'blocked';

export interface FlowResponse {
  status: FlowStatus;
  token?: string;
  expiresIn?: number;
  challenge?: ChallengeSpec;
  assets?: Record<string, string>;
  pow?: PowSpec;
  attemptsLeft?: number;
  reason?: string;
  canFallBack: boolean;
}

export interface ApiError {
  error: string;
  message?: string;
}

/** One recorded pointer position, in challenge-image coordinates. */
export interface TracePoint {
  x: number;
  y: number;
  t: number;
}

export interface Trace {
  mode: 'pointer' | 'touch' | 'keyboard';
  points: TracePoint[];
  startT: number;
  endT: number;
  corrections: number;
}

export interface Signals {
  webdriver: boolean;
  touch: boolean;
  cores: number;
  dpr: number;
  sw: number;
  sh: number;
  vw: number;
  vh: number;
  langs: number;
  moves: number;
  dwell: number;
}

/**
 * A challenge implementation. The host state machine creates one, mounts it,
 * and calls `answer()` when the visitor confirms.
 */
export interface ChallengeView {
  /** Builds the DOM for the challenge inside `parent`. */
  mount(parent: HTMLElement): void;
  /** Removes listeners and DOM. */
  destroy(): void;
  /** The answer payload posted to /v1/solve. */
  answer(): unknown;
  /** The recorded interaction, posted alongside the answer. */
  trace(): Trace;
  /** False while the visitor has not yet produced a submittable answer. */
  ready(): boolean;
  /** Called when the confirm button should re-evaluate its enabled state. */
  onChange?: (ready: boolean) => void;
}
