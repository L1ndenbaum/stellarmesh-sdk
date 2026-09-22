/** 稳定错误类别；业务码另见 apiCode，HTTP 状态另见 status。 */
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

/** 错误诊断信息；cause 保留原始故障，不应直接记录可能含敏感数据的响应体。 */
export interface HttpClientErrorOptions {
  kind: HttpErrorKind;
  status?: number;
  apiCode?: string | number;
  data?: unknown;
  headers?: Readonly<Record<string, string>>;
  cause?: unknown;
}

/** HTTP、业务及生命周期统一错误；无响应的故障可能没有 status 或 headers。 */
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

/** 缩窄未知异常，读取 kind 后区分取消、会话变化和请求失败。 */
export function isHttpClientError(error: unknown): error is HttpClientError {
  return error instanceof HttpClientError;
}
