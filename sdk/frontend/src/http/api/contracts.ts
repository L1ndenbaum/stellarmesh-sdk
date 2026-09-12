import type { AuthBindingOptions, AuthSession } from '../auth/contracts.js';
import type { HttpResponse, ResponseTransform } from '../response/contracts.js';
import type { RetryOptions } from '../retry/contracts.js';
import type {
  HttpHeaders,
  HttpMethod,
  HttpRequestOptions,
} from '../request/contracts.js';

/** 取消信号只属于单次调用，不能绑定到可复用的接口声明。 */
export interface HttpApiDeclarationOptions
  extends Omit<HttpRequestOptions, 'signal'> {
  signal?: never;
}

/** 查询参数由调用输入提供，配置中不再提供第二个来源。 */
export interface HttpApiQueryOptions
  extends Omit<HttpRequestOptions, 'params'> {
  params?: never;
}

export interface HttpApiQueryDeclarationOptions
  extends HttpApiDeclarationOptions {
  params?: never;
}

export type HttpApiRequest<TInput, TResponse, TOptions = HttpRequestOptions> = (
  ...args: [TInput] extends [void]
    ? [input?: undefined, options?: TOptions]
    : [input: TInput, options?: TOptions]
) => Promise<TResponse>;

export interface HttpApiQueryMethod<TMetadata extends boolean = false> {
  <TQuery extends object | void, TResponse>(
    url: string,
    options?: HttpApiQueryDeclarationOptions,
  ): HttpApiRequest<
    TQuery,
    HttpApiResult<TResponse, TMetadata>,
    HttpApiQueryOptions
  >;
}

export interface HttpApiBodyMethod<TMetadata extends boolean = false> {
  <TBody, TResponse>(
    url: string,
    options?: HttpApiDeclarationOptions,
  ): HttpApiRequest<TBody, HttpApiResult<TResponse, TMetadata>>;
}

/** TResponse 始终表示转换后的数据，不代表业务信封或 HTTP 包装。 */
export type HttpApiResult<
  TResponse,
  TMetadata extends boolean,
> = TMetadata extends true ? HttpResponse<TResponse> : TResponse;

export interface HttpApiRequestDescriptor {
  method: HttpMethod;
  url: string;
  data?: unknown;
  params?: HttpRequestOptions['params'];
  headers?: HttpHeaders;
  signal?: never;
}

export interface HttpApiRequestMethod<TMetadata extends boolean = false> {
  <TInput, TResponse>(
    resolve: (input: TInput) => HttpApiRequestDescriptor,
    options?: HttpApiQueryDeclarationOptions,
  ): HttpApiRequest<
    TInput,
    HttpApiResult<TResponse, TMetadata>,
    HttpApiQueryOptions
  >;
}

export interface HttpApi<TMetadata extends boolean = false> {
  get: HttpApiQueryMethod<TMetadata>;
  head: HttpApiQueryMethod<TMetadata>;
  delete: HttpApiQueryMethod<TMetadata>;
  post: HttpApiBodyMethod<TMetadata>;
  put: HttpApiBodyMethod<TMetadata>;
  patch: HttpApiBodyMethod<TMetadata>;
  request: HttpApiRequestMethod<TMetadata>;
  withMetadata(): HttpApi<true>;
  withBaseURL(baseURL: string): HttpApi<TMetadata>;
  withHeaders(headers: HttpHeaders): HttpApi<TMetadata>;
  withTimeout(timeout: number): HttpApi<TMetadata>;
  withMaxRetries(maxRetries: number): HttpApi<TMetadata>;
  withRetry(options: RetryOptions): HttpApi<TMetadata>;
  withAuth(
    session: AuthSession,
    options?: AuthBindingOptions,
  ): HttpApi<TMetadata>;
  withResponseTransform(transform: ResponseTransform): HttpApi<TMetadata>;
}
