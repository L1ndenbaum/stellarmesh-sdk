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
export interface AuthOptions {
  getAccessToken(): string | null | Promise<string | null>;
  /** 成功时持久化新会话并返回 access token；null 表示会话无法恢复。 */
  refreshSession?(): Promise<string | null>;
  onUnauthorized?(error: unknown): void | Promise<void>;
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
  withAuth(options: AuthOptions): ConfigurableHttpClient;
  withResponseTransform(transform: ResponseTransform): ConfigurableHttpClient;
}
