/**
 * React wrapper for the Persian Captcha widget.
 *
 * The wrapper is thin on purpose. All it does is load the loader script, hand
 * it a container element, and translate the callbacks into props. Every piece
 * of challenge logic stays inside the service's iframe, where the host page
 * cannot reach it, and that is as true for a React host as for any other.
 */

import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react';

/** The global the loader script installs. */
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

export interface PersianCaptchaProps {
  /** The public site key for this site. */
  sitekey: string;
  /** Base URL of the captcha service, for example https://captcha.example.com. */
  baseUrl: string;
  /** Force a language; otherwise the site's configured default is used. */
  locale?: string;
  theme?: 'light' | 'dark' | 'auto';
  /** Name of the hidden form field the token is written to. */
  fieldName?: string;
  /** Called with the response token once the visitor is verified. */
  onVerify?: (token: string) => void;
  /** Called when the token passes its expiry and is no longer redeemable. */
  onExpire?: () => void;
  /** Called with a machine-readable code when verification fails. */
  onError?: (code: string) => void;
  className?: string;
}

export interface PersianCaptchaHandle {
  /** Clears the widget and returns it to its resting state. */
  reset(): void;
  /** The current token, or an empty string. */
  getResponse(): string;
  /** Starts verification programmatically. */
  execute(): void;
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

export const PersianCaptcha = forwardRef<PersianCaptchaHandle, PersianCaptchaProps>(
  function PersianCaptcha(props, ref) {
    const {
      sitekey,
      baseUrl,
      locale,
      theme,
      fieldName,
      onVerify,
      onExpire,
      onError,
      className,
    } = props;

    const container = useRef<HTMLDivElement>(null);
    const widgetId = useRef<number | null>(null);
    const [failed, setFailed] = useState(false);

    // Callbacks are held in refs so that a parent re-render with a new inline
    // arrow function does not tear down and rebuild the widget, which would
    // throw away a challenge the visitor is part way through.
    const handlers = useRef({ onVerify, onExpire, onError });
    handlers.current = { onVerify, onExpire, onError };

    useEffect(() => {
      let cancelled = false;

      loadScript(baseUrl)
        .then(() => {
          if (cancelled || !container.current || !window.PersianCaptcha) return;
          widgetId.current = window.PersianCaptcha.render(container.current, {
            sitekey,
            locale,
            theme,
            fieldName,
            callback: (token: string) => handlers.current.onVerify?.(token),
            'expired-callback': () => handlers.current.onExpire?.(),
            'error-callback': (code: string) => handlers.current.onError?.(code),
          });
        })
        .catch(() => {
          if (cancelled) return;
          setFailed(true);
          handlers.current.onError?.('script_load_failed');
        });

      return () => {
        cancelled = true;
        if (widgetId.current !== null) {
          window.PersianCaptcha?.remove(widgetId.current);
          widgetId.current = null;
        }
      };
      // Only the props that define the widget itself belong here; changing
      // one of them genuinely does require a fresh widget.
    }, [sitekey, baseUrl, locale, theme, fieldName]);

    useImperativeHandle(
      ref,
      () => ({
        reset: () => {
          if (widgetId.current !== null) window.PersianCaptcha?.reset(widgetId.current);
        },
        getResponse: () =>
          widgetId.current !== null ? (window.PersianCaptcha?.getResponse(widgetId.current) ?? '') : '',
        execute: () => {
          if (widgetId.current !== null) window.PersianCaptcha?.execute(widgetId.current);
        },
      }),
      [],
    );

    if (failed) {
      // Rendering nothing would leave a form that silently cannot be
      // submitted, which is worse than an honest message.
      return (
        <div className={className} role="alert">
          The verification widget could not be loaded.
        </div>
      );
    }
    return <div ref={container} className={className} />;
  },
);

export default PersianCaptcha;
