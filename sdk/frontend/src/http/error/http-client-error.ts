export const HttpErrorKind = {
  HTTP: 'http',
  BUSINESS: 'business',
  NETWORK: 'network',
  TIMEOUT: 'timeout',
  CANCELED: 'canceled',
  RESPONSE_FORMAT: 'response-format',
  AUTH: 'auth',
  SESSION_CHANGED: 'session-changed',
  UNKNOWN: 'unknown',
} as const;

export type HttpErrorKind = (typeof HttpErrorKind)[keyof typeof HttpErrorKind];

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
