import type { ApiError, FlowResponse, PowSpec, SessionResponse, Signals, Trace } from './types';

/** Raised for any non-success reply, carrying the machine-readable code. */
export class RequestFailed extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, status: number, message?: string) {
    super(message || code);
    this.code = code;
    this.status = status;
  }
}

/**
 * The API client.
 *
 * Every call here is same-origin: this code runs inside the iframe the
 * captcha service itself served, so there is no CORS handshake and no
 * credential to attach.
 */
export class Api {
  constructor(private readonly base: string) {}

  private async post<T>(path: string, body: unknown): Promise<T> {
    let res: Response;
    try {
      res = await fetch(this.base + path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        // The service sets no cookies; sending none keeps the request simple
        // and avoids any third-party cookie question entirely.
        credentials: 'omit',
        cache: 'no-store',
      });
    } catch {
      throw new RequestFailed('network', 0);
    }

    let payload: unknown = null;
    try {
      payload = await res.json();
    } catch {
      // Fall through: a body-less error is still an error worth reporting.
    }
    if (!res.ok) {
      const err = (payload || {}) as ApiError;
      throw new RequestFailed(err.error || 'error', res.status, err.message);
    }
    return payload as T;
  }

  start(sitekey: string, locale: string, origin: string): Promise<SessionResponse> {
    return this.post<SessionResponse>('/session', { sitekey, locale, origin });
  }

  assess(sid: string, nonce: string, signals: Signals): Promise<FlowResponse> {
    return this.post<FlowResponse>('/assess', { sid, nonce, signals });
  }

  newChallenge(sid: string, kind?: string): Promise<FlowResponse> {
    return this.post<FlowResponse>('/challenge', { sid, kind: kind ?? '' });
  }

  solve(sid: string, nonce: string, answer: unknown, trace: Trace): Promise<FlowResponse> {
    return this.post<FlowResponse>('/solve', { sid, nonce, answer, trace });
  }
}

/**
 * Runs the proof of work in a worker and resolves with the winning nonce.
 *
 * The search is off the main thread because even a modest difficulty is a few
 * hundred thousand hashes: on the main thread that would freeze the widget,
 * and on a slow phone it would freeze the host page with it.
 */
export function solvePow(base: string, spec: PowSpec, timeoutMs = 25_000): Promise<string> {
  return new Promise((resolve, reject) => {
    let worker: Worker;
    try {
      worker = new Worker(`${base}/pow.js`);
    } catch {
      reject(new RequestFailed('unsupported', 0));
      return;
    }
    const timer = window.setTimeout(() => {
      worker.terminate();
      reject(new RequestFailed('pow_timeout', 0));
    }, timeoutMs);

    worker.onmessage = (event: MessageEvent) => {
      window.clearTimeout(timer);
      worker.terminate();
      const data = event.data as { nonce?: string; error?: string };
      if (data && typeof data.nonce === 'string') {
        resolve(data.nonce);
      } else {
        reject(new RequestFailed(data?.error || 'pow_failed', 0));
      }
    };
    worker.onerror = () => {
      window.clearTimeout(timer);
      worker.terminate();
      reject(new RequestFailed('pow_failed', 0));
    };
    worker.postMessage({ prefix: spec.prefix, bits: spec.bits, alg: spec.alg });
  });
}
