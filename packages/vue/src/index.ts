/**
 * Vue 3 wrapper for the Persian Captcha widget.
 *
 * Like the React wrapper, this only loads the loader script, gives it an
 * element and forwards the callbacks as emitted events. The challenge itself
 * lives in the service's iframe and is never reachable from the host app.
 */

import { defineComponent, h, onBeforeUnmount, onMounted, ref, type PropType } from 'vue';

interface PersianCaptchaApi {
  render(target: HTMLElement, options: Record<string, unknown>): number;
  reset(id?: number): void;
  getResponse(id?: number): string;
  execute(id?: number): void;
  remove(id: number): void;
}

declare global {
  interface Window {
    PersianCaptcha?: PersianCaptchaApi;
  }
}

/** Tracks in-flight script loads so two widgets do not each add a tag. */
const loading = new Map<string, Promise<void>>();

function loadScript(baseUrl: string): Promise<void> {
  const src = `${baseUrl.replace(/\/$/, '')}/v1/widget.js?render=explicit`;
  if (window.PersianCaptcha) return Promise.resolve();

  const existing = loading.get(src);
  if (existing) return existing;

  const pending = new Promise<void>((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`could not load ${src}`));
    document.head.append(script);
  });
  loading.set(src, pending);
  return pending;
}

export const PersianCaptcha = defineComponent({
  name: 'PersianCaptcha',

  props: {
    /** The public site key for this site. */
    sitekey: { type: String, required: true },
    /** Base URL of the captcha service. */
    baseUrl: { type: String, required: true },
    /** Force a language; otherwise the site's configured default is used. */
    locale: { type: String, default: undefined },
    theme: { type: String as PropType<'light' | 'dark' | 'auto'>, default: undefined },
    /** Name of the hidden form field the token is written to. */
    fieldName: { type: String, default: undefined },
  },

  emits: {
    verify: (token: string) => typeof token === 'string',
    expire: () => true,
    error: (code: string) => typeof code === 'string',
  },

  setup(props, { emit, expose }) {
    const container = ref<HTMLDivElement | null>(null);
    const widgetId = ref<number | null>(null);
    const failed = ref(false);

    onMounted(async () => {
      try {
        await loadScript(props.baseUrl);
      } catch {
        failed.value = true;
        emit('error', 'script_load_failed');
        return;
      }
      if (!container.value || !window.PersianCaptcha) return;

      widgetId.value = window.PersianCaptcha.render(container.value, {
        sitekey: props.sitekey,
        locale: props.locale,
        theme: props.theme,
        fieldName: props.fieldName,
        callback: (token: string) => emit('verify', token),
        'expired-callback': () => emit('expire'),
        'error-callback': (code: string) => emit('error', code),
      });
    });

    onBeforeUnmount(() => {
      if (widgetId.value !== null) {
        window.PersianCaptcha?.remove(widgetId.value);
        widgetId.value = null;
      }
    });

    expose({
      /** Clears the widget and returns it to its resting state. */
      reset: () => {
        if (widgetId.value !== null) window.PersianCaptcha?.reset(widgetId.value);
      },
      /** The current token, or an empty string. */
      getResponse: () =>
        widgetId.value !== null ? (window.PersianCaptcha?.getResponse(widgetId.value) ?? '') : '',
      /** Starts verification programmatically. */
      execute: () => {
        if (widgetId.value !== null) window.PersianCaptcha?.execute(widgetId.value);
      },
    });

    return () =>
      failed.value
        ? // Rendering nothing would leave a form that silently cannot be
          // submitted, which is worse than an honest message.
          h('div', { role: 'alert' }, 'The verification widget could not be loaded.')
        : h('div', { ref: container });
  },
});

export default PersianCaptcha;
