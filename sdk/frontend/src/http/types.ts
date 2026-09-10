import type { AuthSession } from './auth.js';
import type { HttpClientError } from './errors.js';

export type HttpMethod = 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export type ResponseType = 'json' | 'text' | 'blob' | 'arraybuffer';

export type HttpHeaders = Readonly<Record<string, string>>;

export interface HttpProgress {
  loaded: number;
  total?: number;
}

export interface HttpResponse<T> {
  data: T;
  status: number;
  headers: Record<string, string>;
}

export interface ResponseContext {
  status: number;
  headers: Readonly<Record<string, string>>;
}

/** 支持同步或异步转换；metadata 与普通请求均等待最终数据。 */
export type ResponseTransform = (
  data: unknown,
  context: ResponseContext,
) => unknown;

export type AuthSessionEpoch = string | number;

export interface AuthSessionContext {
  readonly epoch: AuthSessionEpoch;
}

export interface AuthSessionOptions {
  /** 登录、退出、切换账号时更换且不可复用；正常 Token 刷新不更换。 */
  getSessionEpoch(): AuthSessionEpoch;
  getAccessToken(): string | null | Promise<string | null>;
  /** 按 epoch 条件保存后返回新 Token；null 确认失效，异常仅使本次操作失败。 */
  refreshSession?(context: AuthSessionContext): Promise<string | null>;
  shouldRefresh?(error: HttpClientError): boolean;
  /** 项目在实际清理凭证时也必须检查 epoch，避免异步清理影响新会话。 */
  onUnauthorized?(
    error: HttpClientError,
    context: AuthSessionContext,
  ): void | Promise<void>;
}

export interface AuthBindingOptions {
  trustedOrigins?: readonly string[];
}

export interface RetryOptions {
  maxRetries?: number;
  baseDelayMs?: number;
  maxDelayMs?: number;
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

export interface HttpBodyMethod {
  <TRequest, TResponse>(
    url: string,
    body: TRequest,
    options?: HttpRequestOptions,
  ): Promise<TResponse>;
  <TResponse = unknown>(
    url: string,
    body?: undefined,
    options?: HttpRequestOptions,
  ): Promise<TResponse>;
}

export interface HttpClient {
  request<TRequest, TResponse>(
    request: HttpRequest<TRequest>,
  ): Promise<TResponse>;
  request<TResponse = unknown>(request: HttpRequest<never>): Promise<TResponse>;
  requestWithMetadata<TRequest, TResponse>(
    request: HttpRequest<TRequest>,
  ): Promise<HttpResponse<TResponse>>;
  requestWithMetadata<TResponse = unknown>(
    request: HttpRequest<never>,
  ): Promise<HttpResponse<TResponse>>;
  get<TResponse = unknown>(
    url: string,
    options?: HttpRequestOptions,
  ): Promise<TResponse>;
  head<TResponse = void>(
    url: string,
    options?: HttpRequestOptions,
  ): Promise<TResponse>;
  delete<TResponse = unknown>(
    url: string,
    options?: HttpRequestOptions,
  ): Promise<TResponse>;
  post: HttpBodyMethod;
  put: HttpBodyMethod;
  patch: HttpBodyMethod;
}

export interface ConfigurableHttpClient extends HttpClient {
  withBaseURL(baseURL: string): ConfigurableHttpClient;
  withHeaders(headers: HttpHeaders): ConfigurableHttpClient;
  withTimeout(timeout: number): ConfigurableHttpClient;
  withMaxRetries(maxRetries: number): ConfigurableHttpClient;
  withRetry(options: RetryOptions): ConfigurableHttpClient;
  withAuth(
    session: AuthSession,
    options?: AuthBindingOptions,
  ): ConfigurableHttpClient;
  withResponseTransform(transform: ResponseTransform): ConfigurableHttpClient;
}
