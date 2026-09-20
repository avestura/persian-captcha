/**
 * The host-page loader.
 *
 * This is the only code that runs on the embedding site, and it is
 * deliberately thin: it creates an iframe pointing at the captcha service,
 * relays a handful of messages, and keeps a hidden form field up to date.
 * None of the challenge logic lives here, because anything on the host page
 * is code an attacker controlling that page can read and drive.
 *
 * The public API mirrors reCAPTCHA and hCaptcha closely enough that an
 * existing integration usually needs only its script tag and site key
 * changed.
 */

interface RenderOptions {
  sitekey?: string;
  locale?: string;
  theme?: 'light' | 'dark' | 'auto';
  /** Name of the hidden input added to the surrounding form. */
  fieldName?: string;
  callback?: (token: string) => void;
  'expired-callback'?: () => void;
  'error-callback'?: (code: string) => void;
}

interface Instance {
  id: number;
  container: HTMLElement;
  placeholder: HTMLElement;
  frame: HTMLIFrameElement;
  backdrop: HTMLElement | null;
  field: HTMLInputElement;
  options: RenderOptions;
  token: string;
  expanded: boolean;
  /** Last size the frame reported, used to fit the expanded overlay. */
  lastW: number;
  lastH: number;
  /** Bound once per instance so it can be removed again. */
  escapeHandler?: (e: KeyboardEvent) => void;
}

const NAMESPACE = 'persian-captcha';
const HOST_NAMESPACE = 'persian-captcha-host';
const DEFAULT_FIELD = 'pcaptcha-response';

/** Resting size of the widget, matching the frame's own layout. */
const IDLE_WIDTH = 302;
const IDLE_HEIGHT = 76;

const instances = new Map<number, Instance>();
let nextId = 1;
let origin = '';
let styleInjected = false;

// ---- bootstrapping ---------------------------------------------------------

/**
 * Works out where the service lives from this script's own URL, so a host page
 * only ever has to write the src once.
 */
function resolveOrigin(): string {
  const current = document.currentScript as HTMLScriptElement | null;
  const src = current?.src;
  if (src) {
    try {
      return new URL(src).origin;
    } catch {
      /* fall through */
    }
  }
  // If the script was inlined or moved, fall back to any tag that looks like
  // ours rather than guessing the current page's origin.
  const found = document.querySelector<HTMLScriptElement>('script[src*="/v1/widget.js"]');
  if (found) {
    try {
      return new URL(found.src).origin;
    } catch {
      /* fall through */
    }
  }
  return window.location.origin;
}

function scriptParams(): URLSearchParams {
  const current = document.currentScript as HTMLScriptElement | null;
  if (!current?.src) return new URLSearchParams();
  try {
    return new URL(current.src).searchParams;
  } catch {
    return new URLSearchParams();
  }
}

/**
 * The host-side stylesheet. It is injected rather than served as a file so a
 * host page needs exactly one network request to use the widget, and it is
 * scoped tightly enough not to collide with a site's own rules.
 */
function injectStyle(): void {
  if (styleInjected) return;
  styleInjected = true;
  const css = `
.pcaptcha{position:relative;display:inline-block;max-width:100%}
.pcaptcha-placeholder{display:block;width:${IDLE_WIDTH}px;height:${IDLE_HEIGHT}px;max-width:100%}
.pcaptcha-frame{position:absolute;inset:0;width:100%;height:100%;border:0;
 color-scheme:light dark;background:transparent}
.pcaptcha-expanded .pcaptcha-frame{position:fixed;inset:auto;top:50%;left:50%;
 transform:translate(-50%,-50%);width:min(420px,calc(100vw - 24px));
 height:min(560px,calc(100vh - 24px));z-index:2147483647;
 border-radius:14px;box-shadow:0 24px 64px rgba(0,0,0,.35)}
.pcaptcha-backdrop{position:fixed;inset:0;background:rgba(15,18,28,.55);
 z-index:2147483646;-webkit-backdrop-filter:blur(2px);backdrop-filter:blur(2px)}
@media (prefers-reduced-motion:no-preference){
 .pcaptcha-backdrop{animation:pcaptcha-fade .15s ease-out}
 @keyframes pcaptcha-fade{from{opacity:0}to{opacity:1}}}
`;
  const style = document.createElement('style');
  style.setAttribute('data-persian-captcha', '');
  style.textContent = css;
  document.head.append(style);
}

// ---- rendering -------------------------------------------------------------

function render(target: HTMLElement | string, options: RenderOptions = {}): number {
  const container = typeof target === 'string' ? document.getElementById(target) : target;
  if (!container) throw new Error('persian-captcha: render target not found');

  const dataset = container.dataset;
  const opts: RenderOptions = {
    sitekey: options.sitekey ?? dataset.sitekey,
    locale: options.locale ?? dataset.locale,
    theme: options.theme ?? (dataset.theme as RenderOptions['theme']),
    fieldName: options.fieldName ?? dataset.fieldName ?? DEFAULT_FIELD,
    callback: options.callback ?? namedFunction(dataset.callback),
    'expired-callback': options['expired-callback'] ?? namedFunction(dataset.expiredCallback),
    'error-callback': options['error-callback'] ?? namedFunction(dataset.errorCallback),
  };
  if (!opts.sitekey) throw new Error('persian-captcha: a sitekey is required');

  injectStyle();
  container.classList.add('pcaptcha');
  container.innerHTML = '';

  // The placeholder reserves layout space so expanding the challenge into a
  // centred overlay does not shift the page underneath it.
  const placeholder = document.createElement('span');
  placeholder.className = 'pcaptcha-placeholder';

  const frame = document.createElement('iframe');
  frame.className = 'pcaptcha-frame';
  frame.title = 'captcha';
  frame.setAttribute('scrolling', 'no');
  // The frame needs scripts and same-origin (it talks to its own API) but
  // nothing else: no top-level navigation, no downloads, no popups.
  frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms');
  frame.setAttribute('allow', 'clipboard-write');
  frame.src = frameUrl(opts);

  const field = document.createElement('input');
  field.type = 'hidden';
  field.name = opts.fieldName || DEFAULT_FIELD;

  container.append(placeholder, frame, field);

  const id = nextId++;
  instances.set(id, {
    id,
    container,
    placeholder,
    frame,
    backdrop: null,
    field,
    options: opts,
    token: '',
    expanded: false,
    lastW: IDLE_WIDTH,
    lastH: IDLE_HEIGHT,
  });
  container.dataset.pcaptchaId = String(id);
  return id;
}

function frameUrl(opts: RenderOptions): string {
  const url = new URL(`${origin}/v1/frame`);
  url.searchParams.set('sitekey', opts.sitekey!);
  url.searchParams.set('origin', window.location.origin);
  if (opts.locale) url.searchParams.set('locale', opts.locale);
  if (opts.theme) url.searchParams.set('theme', opts.theme);
  return url.toString();
}

/** Resolves a global function named in a data attribute. */
function namedFunction<T>(name: string | undefined): T | undefined {
  if (!name) return undefined;
  const fn = (window as unknown as Record<string, unknown>)[name];
  return typeof fn === 'function' ? (fn as T) : undefined;
}

// ---- messaging -------------------------------------------------------------

window.addEventListener('message', (event: MessageEvent) => {
  // Two checks, both necessary: the origin proves the message came from the
  // captcha service, and the source window proves it came from a frame this
  // script created rather than any other frame on the page.
  if (event.origin !== origin) return;
  const data = event.data as { source?: string; t?: string; [k: string]: unknown };
  if (!data || data.source !== NAMESPACE) return;

  const instance = findBySource(event.source);
  if (!instance) return;

  switch (data.t) {
    case 'size':
      instance.lastW = Number(data.w) || IDLE_WIDTH;
      instance.lastH = Number(data.h) || IDLE_HEIGHT;
      if (instance.expanded) {
        fitExpanded(instance);
      } else {
        instance.placeholder.style.width = `${instance.lastW}px`;
        instance.placeholder.style.height = `${instance.lastH}px`;
      }
      break;
    case 'expand':
      expand(instance);
      break;
    case 'collapse':
      collapse(instance);
      break;
    case 'token':
      instance.token = String(data.token || '');
      instance.field.value = instance.token;
      instance.options.callback?.(instance.token);
      break;
    case 'expired':
      instance.token = '';
      instance.field.value = '';
      instance.options['expired-callback']?.();
      break;
    case 'error':
      instance.token = '';
      instance.field.value = '';
      instance.options['error-callback']?.(String(data.code || 'error'));
      break;
  }
});

// A rotated phone or a resized window must not leave the overlay hanging off
// the screen.
window.addEventListener('resize', () => {
  for (const instance of instances.values()) {
    if (instance.expanded) fitExpanded(instance);
  }
});

function findBySource(source: MessageEventSource | null): Instance | undefined {
  for (const instance of instances.values()) {
    if (instance.frame.contentWindow === source) return instance;
  }
  return undefined;
}

/**
 * Sizes the overlay to whatever the frame says it needs, capped to the
 * viewport. Without this the challenge would sit inside a fixed-height box
 * with dead space below it, since a click-order board and a four-option
 * question are nothing like the same height.
 */
function fitExpanded(instance: Instance): void {
  const margin = 24;
  const width = Math.min(instance.lastW, window.innerWidth - margin);
  const height = Math.min(instance.lastH, window.innerHeight - margin);
  instance.frame.style.width = `${Math.max(width, 240)}px`;
  instance.frame.style.height = `${Math.max(height, 120)}px`;
}

function expand(instance: Instance): void {
  if (instance.expanded) return;
  instance.expanded = true;
  instance.container.classList.add('pcaptcha-expanded');
  fitExpanded(instance);

  const backdrop = document.createElement('div');
  backdrop.className = 'pcaptcha-backdrop';
  // Clicking the backdrop cancels, which is the behaviour people expect from
  // anything that looks like a modal.
  backdrop.addEventListener('click', () => post(instance, { t: 'reset' }));
  document.body.append(backdrop);
  instance.backdrop = backdrop;

  if (!instance.escapeHandler) {
    instance.escapeHandler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && instance.expanded) post(instance, { t: 'reset' });
    };
  }
  document.addEventListener('keydown', instance.escapeHandler);
}

function collapse(instance: Instance): void {
  if (!instance.expanded) return;
  instance.expanded = false;
  instance.container.classList.remove('pcaptcha-expanded');
  // Clear the inline size so the resting iframe goes back to filling its
  // placeholder rather than keeping the overlay's dimensions.
  instance.frame.style.width = '';
  instance.frame.style.height = '';
  instance.backdrop?.remove();
  instance.backdrop = null;
  if (instance.escapeHandler) {
    document.removeEventListener('keydown', instance.escapeHandler);
  }
}

function post(instance: Instance, message: Record<string, unknown>): void {
  instance.frame.contentWindow?.postMessage({ source: HOST_NAMESPACE, ...message }, origin);
}

// ---- public API ------------------------------------------------------------

const api = {
  /** Renders a widget into an element, returning its id. */
  render,

  /** Clears a widget and returns it to its resting state. */
  reset(id?: number): void {
    for (const instance of pick(id)) {
      instance.token = '';
      instance.field.value = '';
      post(instance, { t: 'reset' });
    }
  },

  /** The current token, or an empty string when there is none. */
  getResponse(id?: number): string {
    return pick(id)[0]?.token ?? '';
  },

  /** Starts verification programmatically, for an invisible integration. */
  execute(id?: number): void {
    for (const instance of pick(id)) post(instance, { t: 'execute' });
  },

  /** Removes a widget from the page entirely. */
  remove(id: number): void {
    const instance = instances.get(id);
    if (!instance) return;
    collapse(instance);
    instance.container.innerHTML = '';
    instance.container.classList.remove('pcaptcha');
    delete instance.container.dataset.pcaptchaId;
    instances.delete(id);
  },
};

function pick(id?: number): Instance[] {
  if (id === undefined) return [...instances.values()];
  const instance = instances.get(id);
  return instance ? [instance] : [];
}

// ---- automatic rendering ---------------------------------------------------

function renderAll(): void {
  document.querySelectorAll<HTMLElement>('.pcaptcha,[data-pcaptcha]').forEach((node) => {
    if (node.dataset.pcaptchaId) return;
    try {
      render(node);
    } catch (err) {
      console.error('persian-captcha:', err);
    }
  });
}

function boot(): void {
  origin = resolveOrigin();
  const params = scriptParams();

  (window as unknown as Record<string, unknown>).PersianCaptcha = api;
  (window as unknown as Record<string, unknown>).pcaptcha = api;

  if (params.get('render') !== 'explicit') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', renderAll);
    } else {
      renderAll();
    }
  }

  const onload = params.get('onload');
  if (onload) {
    const fn = namedFunction<() => void>(onload);
    // The callback may be defined after this script runs, so defer a turn.
    if (fn) window.setTimeout(fn, 0);
    else window.setTimeout(() => namedFunction<() => void>(onload)?.(), 0);
  }
}

boot();

export {};
