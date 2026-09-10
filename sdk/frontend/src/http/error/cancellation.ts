import { HttpClientError } from './http-client-error.js';

export function canceledError(signal?: AbortSignal): HttpClientError {
  return new HttpClientError('请求已取消', {
    kind: 'canceled',
    cause: signal?.reason,
  });
}

export function throwIfCanceled(signal?: AbortSignal): void {
  if (signal?.aborted) throw canceledError(signal);
}

/** 取消单个等待者，不取消并发请求共享的刷新任务。 */
export function abortable<T>(
  promise: Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  if (!signal) return promise;
  return new Promise<T>((resolve, reject) => {
    const abort = () => reject(canceledError(signal));
    const cleanup = () => signal.removeEventListener('abort', abort);
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
    promise.then(
      (value) => {
        cleanup();
        resolve(value);
      },
      (error) => {
        cleanup();
        reject(error);
      },
    );
  });
}
