import type { AuthBindingOptions, AuthSession } from '../auth/contracts';
import type { ErrorCodeExtractor } from '../error/contracts';
import type { HttpResponse, ResponseTransform } from '../response/contracts';
import type { RetryOptions } from '../retry/contracts';
import type { SseApi } from '../sse/contracts';
import type {
  HttpHeaders,
  HttpMethod,
  HttpRequestOptions,
} from '../request/contracts';

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

/** 查询声明选项；输入提供 params，signal 留给每次调用。 */
export interface HttpApiQueryDeclarationOptions
  extends HttpApiDeclarationOptions {
  params?: never;
}

/** 可复用请求函数；调用才发送请求，void 输入省略时配置仍放第二个参数。 */
export type HttpApiRequest<TInput, TResponse, TOptions = HttpRequestOptions> = (
  ...args: [TInput] extends [void]
    ? [input?: undefined, options?: TOptions]
    : [input: TInput, options?: TOptions]
) => Promise<TResponse>;

/** 声明查询方法，TQuery 映射 URL 参数，TResponse 描述转换后的数据。 */
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

/** 声明请求体方法，配置中的 params 可补充 URL 查询参数。 */
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

/** 单次调用解析出的请求；调用方负责路径编码，SDK 不推断路径模板。 */
export interface HttpApiRequestDescriptor {
  method: HttpMethod;
  url: string;
  data?: unknown;
  params?: HttpRequestOptions['params'];
  headers?: HttpHeaders;
  signal?: never;
}

/** 同步映射一次；普通重试和认证重放复用映射结果。 */
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

/**
 * 不可变的声明入口；withXxx 返回新实例，不修改已有声明。
 * @remarks 配置优先级为调用、声明、客户端、默认值；认证头最后覆盖同名头。
 * @example
 * const requestUsers = http.withBaseURL('/api').get<void, string[]>('/users');
 * const users = await requestUsers();
 */
export interface HttpApi<TMetadata extends boolean = false> {
  /** Fetch 事件流声明入口；不使用普通重试、成功响应转换或 metadata 包装。 */
  readonly sse: SseApi;
  /** 声明 GET；调用输入是唯一的查询参数来源。 */
  get: HttpApiQueryMethod<TMetadata>;
  /** 声明 HEAD；输入作为查询参数，响应没有业务请求体。 */
  head: HttpApiQueryMethod<TMetadata>;
  /** 声明 DELETE；输入作为查询参数，默认不参与普通重试。 */
  delete: HttpApiQueryMethod<TMetadata>;
  /** 声明 POST；调用输入作为请求体，不立即发出请求。 */
  post: HttpApiBodyMethod<TMetadata>;
  /** 声明 PUT；调用输入作为请求体，写重试需显式确认安全。 */
  put: HttpApiBodyMethod<TMetadata>;
  /** 声明 PATCH；调用输入作为请求体。 */
  patch: HttpApiBodyMethod<TMetadata>;
  /** 声明动态 URL／方法映射，每次逻辑调用同步执行一次。 */
  request: HttpApiRequestMethod<TMetadata>;
  /** 保留转换后的 data、HTTP status 和 headers；不决定是否解包业务信封。 */
  withMetadata(): HttpApi<true>;
  /** 派生基地址；默认认证信任范围为该地址的 origin。 */
  withBaseURL(baseURL: string): HttpApi<TMetadata>;
  /** 按名称大小写不敏感合并默认头；参数中的同名字段覆盖已有值。 */
  withHeaders(headers: HttpHeaders): HttpApi<TMetadata>;
  /** 默认超时，单位毫秒，0 为不限；SSE 仅计时到收到响应头。 */
  withTimeout(timeout: number): HttpApi<TMetadata>;
  /** 普通传输最多额外重试次数，非负整数，默认 0；不影响 SSE。 */
  withMaxRetries(maxRetries: number): HttpApi<TMetadata>;
  /** 派生普通请求的退避策略；认证恢复最多一次且独立计数。 */
  withRetry(options: RetryOptions): HttpApi<TMetadata>;
  /** 绑定业务会话；共享同一 AuthSession 的 HTTP／SSE 协调同一次刷新。 */
  withAuth(
    session: AuthSession,
    options?: AuthBindingOptions,
  ): HttpApi<TMetadata>;
  /** 转换 HTTP 成功响应；响应泛型描述转换结果，不自动校验 DTO。 */
  withResponseTransform(transform: ResponseTransform): HttpApi<TMetadata>;
  /** 配置同步 HTTP／业务错误码提取；非法回调结果停止恢复和重试。 */
  withErrorCodeExtractor(extractor: ErrorCodeExtractor): HttpApi<TMetadata>;
}
