import type { HttpRequestOptions } from '../request/contracts.js';

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

export interface HttpApiQueryMethod {
  <TQuery extends object | void, TResponse>(
    url: string,
    options?: HttpApiQueryDeclarationOptions,
  ): HttpApiRequest<TQuery, TResponse, HttpApiQueryOptions>;
}

export interface HttpApiBodyMethod {
  <TBody, TResponse>(
    url: string,
    options?: HttpApiDeclarationOptions,
  ): HttpApiRequest<TBody, TResponse>;
}

export interface HttpApi {
  get: HttpApiQueryMethod;
  head: HttpApiQueryMethod;
  delete: HttpApiQueryMethod;
  post: HttpApiBodyMethod;
  put: HttpApiBodyMethod;
  patch: HttpApiBodyMethod;
}
