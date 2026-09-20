import type {
  Boot,
  ChallengeSpec,
  ChallengeView,
  FlowResponse,
  LocaleBundle,
  PowSpec,
} from './types';
import { Translator } from './i18n';
import { Api, RequestFailed, solvePow } from './net';
import { collectSignals, watchEnvironment } from './telemetry';
import { announce, clear, el } from './ui';
import { SliderChallenge } from './challenges/slider';
import { RotateChallenge } from './challenges/rotate';
import { ClickOrderChallenge } from './challenges/clickorder';
import { DragDropChallenge } from './challenges/dragdrop';
import { AccessibleChallenge } from './challenges/accessible';

type View = 'idle' | 'busy' | 'challenge' | 'success' | 'failed' | 'blocked';

/**
 * The width the challenge panel asks the host for, matching the max-width in
 * frame.css. The host narrows it on a small viewport.
 */
const PANEL_WIDTH = 420;

/**
 * The frame application.
 *
 * Everything the visitor interacts with lives here, inside an iframe on the
 * captcha service's own origin. The host page cannot reach into this document
 * to read the challenge or synthesise the events that solve it; it can only
 * exchange the small set of messages defined at the bottom of this file.
 */
class Frame {
  private readonly api: Api;
  private t: Translator;
  private readonly root: HTMLElement;
  private readonly live: HTMLElement;

  private view: View = 'idle';
  private sid = '';
  private pow: PowSpec | null = null;
  /** Started as soon as a proof of work arrives, so confirming feels instant. */
  private powWork: Promise<string> | null = null;
  private challenge: ChallengeView | null = null;
  private challengeSpec: ChallengeSpec | null = null;
  private assets: Record<string, string> = {};
  private attemptsLeft = 0;
  private canFallBack = false;
  private expiryTimer = 0;
  private busyLabel = '';
  private failureMessage = '';

  constructor(private readonly boot: Boot) {
    this.api = new Api(boot.base);
    this.t = new Translator(boot.locale);
    this.root = document.getElementById('pc-root') as HTMLElement;
    this.live = el('div', 'pc-sr-only');
    this.live.setAttribute('aria-live', 'assertive');
    document.body.append(this.live);
  }

  start(): void {
    watchEnvironment();
    this.applyLocaleToDocument();
    this.render();
    this.watchSize();
    this.listenToParent();
    this.post({ t: 'ready' });
  }

  // ---- rendering -----------------------------------------------------------

  private render(): void {
    clear(this.root);
    switch (this.view) {
      case 'idle':
        this.root.append(this.renderCheckbox(false));
        break;
      case 'busy':
        this.root.append(this.renderCheckbox(true));
        break;
      case 'success':
        this.root.append(this.renderStatus('success', this.t.t('widget.verified')));
        break;
      case 'failed':
        this.root.append(this.renderStatus('failed', this.failureMessage || this.t.t('widget.error')));
        break;
      case 'blocked':
        this.root.append(this.renderStatus('blocked', this.t.t('widget.blocked')));
        break;
      case 'challenge':
        this.root.append(this.renderPanel());
        break;
    }
  }

  /** The resting state: a checkbox row, the shape every visitor recognises. */
  private renderCheckbox(busy: boolean): HTMLElement {
    const row = el('div', 'pc-row');

    const button = el('button', 'pc-check');
    button.type = 'button';
    button.setAttribute('role', 'checkbox');
    button.setAttribute('aria-checked', 'false');
    button.setAttribute('aria-label', this.t.t('widget.checkbox'));
    button.disabled = busy;
    if (busy) button.classList.add('pc-check-busy');
    button.addEventListener('click', () => void this.begin());

    const label = el('span', 'pc-label', busy ? this.busyLabel : this.t.t('widget.checkbox'));

    row.append(button, label, this.renderBrand());
    return row;
  }

  private renderStatus(kind: string, message: string): HTMLElement {
    const row = el('div', `pc-row pc-row-${kind}`);
    const icon = el('span', `pc-icon pc-icon-${kind}`);
    icon.setAttribute('aria-hidden', 'true');
    const label = el('span', 'pc-label', message);

    row.append(icon, label);
    if (kind !== 'success') {
      const retry = el('button', 'pc-link', this.t.t('widget.retry'));
      retry.type = 'button';
      retry.addEventListener('click', () => this.resetToIdle());
      row.append(retry);
    }
    row.append(this.renderBrand());
    announce(this.live, message);
    return row;
  }

  private renderBrand(): HTMLElement {
    const brand = el('span', 'pc-brand');
    brand.setAttribute('aria-hidden', 'true');
    brand.textContent = this.t.t('widget.brand');
    return brand;
  }

  /** The expanded challenge panel. */
  private renderPanel(): HTMLElement {
    const panel = el('div', 'pc-panel');
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-modal', 'true');
    panel.setAttribute('aria-label', this.t.t('challenge.title'));

    const header = el('div', 'pc-panel-head');
    header.append(el('h1', 'pc-title', this.t.t('challenge.title')));

    const tools = el('div', 'pc-tools');
    tools.append(this.renderLocalePicker());
    tools.append(
      this.iconButton('refresh', this.t.t('challenge.refresh'), () => void this.reroll()),
    );
    tools.append(this.iconButton('close', this.t.t('challenge.close'), () => this.resetToIdle()));
    header.append(tools);

    // The confirm button is built first so the challenge can enable it as
    // soon as the visitor has produced something submittable.
    const confirm = el('button', 'pc-confirm', this.t.t('challenge.submit'));
    confirm.type = 'button';
    confirm.disabled = true;
    confirm.addEventListener('click', () => void this.submit());

    const body = el('div', 'pc-panel-body');
    if (this.challengeSpec) {
      this.challenge = makeChallenge(this.challengeSpec, this.assets, this.t);
      this.challenge.onChange = (ready) => {
        confirm.disabled = !ready;
      };
      this.challenge.mount(body);
      confirm.disabled = !this.challenge.ready();
    }

    const footer = el('div', 'pc-panel-foot');

    const notes = el('div', 'pc-notes');
    if (this.failureMessage) {
      notes.append(el('p', 'pc-error', this.failureMessage));
    }
    if (this.attemptsLeft > 0) {
      notes.append(
        el(
          'p',
          'pc-attempts',
          this.attemptsLeft === 1
            ? this.t.t('widget.lastAttempt')
            : this.t.t('widget.attemptsLeft', { n: this.attemptsLeft }),
        ),
      );
    }
    if (this.canFallBack) {
      const fallback = el('button', 'pc-link', this.t.t('challenge.useAccessible'));
      fallback.type = 'button';
      fallback.addEventListener('click', () => void this.reroll('accessible'));
      notes.append(fallback);
    }

    footer.append(notes, confirm);
    panel.append(header, body, footer);
    return panel;
  }

  private iconButton(icon: string, label: string, onClick: () => void): HTMLElement {
    const button = el('button', `pc-icon-button pc-icon-button-${icon}`);
    button.type = 'button';
    button.title = label;
    button.setAttribute('aria-label', label);
    button.addEventListener('click', onClick);
    return button;
  }

  /**
   * A language picker inside the widget. The embedding page chooses a
   * default, but a visitor who cannot read it needs a way out that does not
   * depend on the host site having thought about it.
   */
  private renderLocalePicker(): HTMLElement {
    if (this.boot.locales.length < 2) return el('span');
    const select = el('select', 'pc-locale');
    select.setAttribute('aria-label', 'Language / زبان');
    for (const choice of this.boot.locales) {
      const option = el('option');
      option.value = choice.tag;
      option.textContent = choice.name;
      if (choice.tag === this.t.tag) option.selected = true;
      select.append(option);
    }
    select.addEventListener('change', () => void this.switchLocale(select.value));
    return select;
  }

  // ---- flow ----------------------------------------------------------------

  private async begin(): Promise<void> {
    if (this.view === 'busy') return;
    this.setBusy(this.t.t('widget.working'));
    try {
      const started = await this.api.start(this.boot.sitekey, this.t.tag, this.boot.parentOrigin);
      this.sid = started.sid;
      const nonce = await solvePow(this.boot.base, started.pow);

      this.setBusy(this.t.t('widget.verifying'));
      const result = await this.api.assess(this.sid, nonce, collectSignals());
      this.handleFlow(result);
    } catch (err) {
      this.handleError(err);
    }
  }

  private async submit(): Promise<void> {
    if (!this.challenge || !this.powWork) return;
    const answer = this.challenge.answer();
    const trace = this.challenge.trace();

    this.setBusy(this.t.t('challenge.checking'));
    try {
      const nonce = await this.powWork;
      const result = await this.api.solve(this.sid, nonce, answer, trace);
      this.handleFlow(result);
    } catch (err) {
      this.handleError(err);
    }
  }

  private async reroll(kind?: string): Promise<void> {
    this.setBusy(this.t.t('challenge.loading'));
    try {
      this.handleFlow(await this.api.newChallenge(this.sid, kind));
    } catch (err) {
      this.handleError(err);
    }
  }

  private handleFlow(result: FlowResponse): void {
    this.attemptsLeft = result.attemptsLeft ?? 0;
    this.canFallBack = result.canFallBack;

    switch (result.status) {
      case 'pass':
        this.succeed(result.token ?? '', result.expiresIn ?? 0);
        return;
      case 'blocked':
        this.teardownChallenge();
        this.view = 'blocked';
        this.post({ t: 'collapse' });
        this.post({ t: 'error', code: 'blocked' });
        this.render();
        return;
      case 'challenge':
      case 'retry':
        this.failureMessage =
          result.status === 'retry' ? this.t.t('challenge.incorrect') : '';
        this.openChallenge(result);
        return;
    }
  }

  private openChallenge(result: FlowResponse): void {
    if (!result.challenge) {
      this.handleError(new RequestFailed('error', 0));
      return;
    }
    this.teardownChallenge();
    this.challengeSpec = result.challenge;
    this.assets = result.assets ?? {};
    this.pow = result.pow ?? null;

    // The next proof of work is started now, while the visitor reads the
    // prompt and solves the puzzle. By the time they press confirm it is
    // almost always already done, so the work costs them no waiting.
    this.powWork = this.pow
      ? solvePow(this.boot.base, this.pow).catch(() => {
          throw new RequestFailed('pow_failed', 0);
        })
      : null;

    const wasOpen = this.view === 'challenge';
    this.view = 'challenge';
    if (!wasOpen) this.post({ t: 'expand' });
    this.render();
    announce(this.live, this.t.t('challenge.title'));
  }

  private succeed(token: string, expiresIn: number): void {
    this.teardownChallenge();
    this.view = 'success';
    this.post({ t: 'collapse' });
    this.render();
    this.post({ t: 'token', token, expiresIn });

    window.clearTimeout(this.expiryTimer);
    if (expiresIn > 0) {
      // Tell the host when the token goes stale, so a long-lived form does
      // not submit something the backend will reject.
      this.expiryTimer = window.setTimeout(
        () => {
          this.post({ t: 'expired' });
          this.resetToIdle();
        },
        Math.max(1000, expiresIn * 1000 - 2000),
      );
    }
  }

  private handleError(err: unknown): void {
    this.teardownChallenge();
    const code = err instanceof RequestFailed ? err.code : 'error';

    switch (code) {
      case 'session':
        this.failureMessage = this.t.t('widget.expired');
        break;
      case 'rate_limited':
        this.failureMessage = this.t.t('error.rate_limited');
        break;
      case 'origin':
        this.failureMessage = this.t.t('error.origin');
        break;
      case 'network':
        this.failureMessage = this.t.t('widget.offline');
        break;
      case 'unsupported':
        this.failureMessage = this.t.t('error.unsupported');
        break;
      default:
        this.failureMessage = this.t.t('widget.error');
    }

    this.view = 'failed';
    this.post({ t: 'collapse' });
    this.post({ t: 'error', code });
    this.render();
  }

  private setBusy(label: string): void {
    this.busyLabel = label;
    if (this.view === 'challenge') {
      // Keep the panel on screen and just disable its controls, rather than
      // replacing a puzzle the visitor is looking at with a spinner.
      this.root.querySelectorAll('button, select, input').forEach((node) => {
        (node as HTMLButtonElement).disabled = true;
      });
      const confirm = this.root.querySelector('.pc-confirm');
      if (confirm) confirm.textContent = label;
      return;
    }
    this.view = 'busy';
    this.render();
  }

  private resetToIdle(): void {
    this.teardownChallenge();
    window.clearTimeout(this.expiryTimer);
    this.sid = '';
    this.pow = null;
    this.powWork = null;
    this.failureMessage = '';
    this.attemptsLeft = 0;
    this.view = 'idle';
    this.post({ t: 'collapse' });
    this.render();
  }

  private teardownChallenge(): void {
    this.challenge?.destroy();
    this.challenge = null;
    this.challengeSpec = null;
  }

  // ---- locale --------------------------------------------------------------

  private async switchLocale(tag: string): Promise<void> {
    if (tag === this.t.tag) return;
    try {
      const res = await fetch(`${this.boot.base}/locale/${encodeURIComponent(tag)}`, {
        credentials: 'omit',
      });
      if (!res.ok) return;
      const bundle = (await res.json()) as LocaleBundle;
      this.t = new Translator(bundle);
      this.applyLocaleToDocument();
      // The challenge itself is unchanged: the artwork carries no text, so
      // only the surrounding prose needs rebuilding.
      this.render();
    } catch {
      // A failed switch leaves the current language in place, which is a
      // perfectly usable outcome.
    }
  }

  private applyLocaleToDocument(): void {
    document.documentElement.lang = this.t.tag;
    document.documentElement.dir = this.t.dir;
  }

  // ---- parent messaging ----------------------------------------------------

  private post(message: Record<string, unknown>): void {
    const target = this.boot.parentOrigin || '*';
    try {
      window.parent.postMessage({ source: 'persian-captcha', ...message }, target);
    } catch {
      // A parent that has navigated away is not an error worth surfacing.
    }
  }

  private listenToParent(): void {
    window.addEventListener('message', (event: MessageEvent) => {
      // Only the page that embedded this frame may drive it.
      if (this.boot.parentOrigin && event.origin !== this.boot.parentOrigin) return;
      const data = event.data as { source?: string; t?: string };
      if (!data || data.source !== 'persian-captcha-host') return;

      switch (data.t) {
        case 'reset':
          this.resetToIdle();
          break;
        case 'execute':
          if (this.view === 'idle') void this.begin();
          break;
      }
    });
  }

  /**
   * Reports the size the frame wants to be, so the host can size the iframe.
   *
   * The width for a challenge is declared rather than measured. Measuring
   * would be circular: the panel is laid out inside the iframe, so it can
   * never ask to be wider than the iframe already is, and the overlay would
   * stay stuck at the resting width. The host caps whatever is asked for to
   * the viewport, which is what makes this safe on a phone.
   */
  private watchSize(): void {
    let lastW = 0;
    let lastH = 0;
    const report = () => {
      const rect = this.root.getBoundingClientRect();
      const w = this.view === 'challenge' ? PANEL_WIDTH : Math.ceil(rect.width);
      const h = Math.ceil(rect.height);
      if (w === lastW && h === lastH) return;
      lastW = w;
      lastH = h;
      this.post({ t: 'size', w, h });
    };
    new ResizeObserver(report).observe(this.root);
    window.addEventListener('load', report);
  }
}

/** Builds the view for a challenge kind. */
function makeChallenge(
  spec: ChallengeSpec,
  assets: Record<string, string>,
  t: Translator,
): ChallengeView {
  switch (spec.kind) {
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

declare global {
  interface Window {
    __PC_BOOT__?: Boot;
  }
}

const boot = window.__PC_BOOT__;
if (boot) {
  new Frame(boot).start();
}
