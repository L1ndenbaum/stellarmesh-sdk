export type HttpErrorKind =
  | 'http'
  | 'business'
  | 'network'
  | 'timeout'
  | 'canceled'
  | 'response-format'
  | 'auth'
  | 'unknown';
export interface HttpClientErrorOptions {
  kind: HttpErrorKind;
  status?: number;
  apiCode?: string | number;
  data?: unknown;
  headers?: Readonly<Record<string, string>>;
  cause?: unknown;
}
export class HttpClientError extends Error {
  readonly kind: HttpErrorKind;
  readonly status?: number;
  readonly apiCode?: string | number;
  readonly data?: unknown;
  readonly headers?: Readonly<Record<string, string>>;
  constructor(message: string, options: HttpClientErrorOptions) {
    super(message, { cause: options.cause });
    this.name = 'HttpClientError';
    this.kind = options.kind;
    this.status = options.status;
    this.apiCode = options.apiCode;
    this.data = options.data;
    this.headers = options.headers;
  }
}
export function isHttpClientError(error: unknown): error is HttpClientError {
  return error instanceof HttpClientError;
}
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
