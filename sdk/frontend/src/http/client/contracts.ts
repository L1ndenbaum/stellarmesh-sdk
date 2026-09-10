import type { AuthBindingOptions, AuthSession } from '../auth/contracts.js';
import type {
  HttpHeaders,
  HttpRequest,
  HttpRequestOptions,
} from '../request/contracts.js';
import type { HttpResponse, ResponseTransform } from '../response/contracts.js';
import type { RetryOptions } from '../retry/contracts.js';

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
