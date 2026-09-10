import type { ResponseType } from '../response/contracts.js';

export const HttpMethod = {
  GET: 'GET',
  HEAD: 'HEAD',
  POST: 'POST',
  PUT: 'PUT',
  PATCH: 'PATCH',
  DELETE: 'DELETE',
} as const;

export type HttpMethod = (typeof HttpMethod)[keyof typeof HttpMethod];

export type HttpHeaders = Readonly<Record<string, string>>;

export interface HttpProgress {
  loaded: number;
  total?: number;
}

export interface HttpRequestOptions {
  params?: Readonly<Record<string, unknown>> | URLSearchParams;
  headers?: HttpHeaders;
  timeout?: number;
  maxRetries?: number;
  /** 写请求由调用方确认幂等性和请求体可重放后开启。 */
  retryable?: boolean;
  signal?: AbortSignal;
  auth?: boolean;
  /** false 仅关闭认证恢复重放和未授权通知，仍正常注入凭证。 */
  authRecovery?: boolean;
  responseMode?: 'transformed' | 'raw';
  responseType?: ResponseType;
  onUploadProgress?(progress: HttpProgress): void;
  onDownloadProgress?(progress: HttpProgress): void;
}

export interface HttpRequest<TBody = unknown> extends HttpRequestOptions {
  method: HttpMethod;
  url: string;
  data?: TBody;
}
