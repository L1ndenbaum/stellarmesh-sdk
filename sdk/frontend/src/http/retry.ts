import { canceledError, type HttpClientError } from './errors.js';
import type { HttpRequest, RetryOptions } from './types.js';

export type RetryPolicy = Required<RetryOptions>;

export const defaultRetry: RetryPolicy = {
  maxRetries: 0,
  baseDelayMs: 300,
  maxDelayMs: 10_000,
};

export function validateNumber(
  value: number,
  name: string,
  integer = false,
): void {
  if (
    !Number.isFinite(value) ||
    value < 0 ||
    (integer && !Number.isInteger(value))
  ) {
    throw new RangeError(`${name} 必须为非负${integer ? '整数' : '有限数值'}`);
  }
}

export function retryDelay(
  error: HttpClientError,
  request: HttpRequest,
  used: number,
  policy: RetryPolicy,
): number | null {
  const limit = request.maxRetries ?? policy.maxRetries;
  const allowed =
    request.retryable ??
    (request.method === 'GET' || request.method === 'HEAD');
  if (!allowed || used >= limit) return null;
  if (!(
    error.kind === 'network' ||
    error.kind === 'timeout' ||
    (error.kind === 'http' &&
      [408, 429, 502, 503, 504].includes(error.status ?? 0))
  ))
    return null;
  const after = error.headers?.['retry-after'];
  let minimum = 0;
  if (after) {
    const seconds = /^\d+(\.\d+)?$/.test(after.trim()) ? Number(after) : NaN;
    const delay = Number.isFinite(seconds)
      ? seconds * 1000
      : Date.parse(after) - Date.now();
    if (Number.isFinite(delay)) minimum = Math.max(0, delay);
    if (minimum > policy.maxDelayMs) return null;
  }
  const ceiling = Math.min(
    policy.maxDelayMs,
    policy.baseDelayMs * 2 ** Math.min(used, 30),
  );
  return Math.max(minimum, Math.random() * ceiling);
}

export function waitForRetry(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const cleanup = () => signal?.removeEventListener('abort', abort);
    const timer = setTimeout(() => {
      cleanup();
      resolve();
    }, ms);
    const abort = () => {
      clearTimeout(timer);
      cleanup();
      reject(canceledError(signal));
    };
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
  });
}
