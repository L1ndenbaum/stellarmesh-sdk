import type { HttpClient } from '../client/contracts.js';
import type { HttpMethod, HttpRequestOptions } from '../request/contracts.js';
import { mergeHeaders } from '../transport/axios-transport.js';
import type {
  HttpApi,
  HttpApiBodyMethod,
  HttpApiDeclarationOptions,
  HttpApiQueryDeclarationOptions,
  HttpApiQueryMethod,
  HttpApiQueryOptions,
  HttpApiRequest,
} from './contracts.js';

function copyParams(
  params: HttpRequestOptions['params'],
): HttpRequestOptions['params'] {
  if (params === undefined) return undefined;
  // 只复制查询容器；不克隆任意 DTO 的嵌套实例或改变其序列化语义。
  return params instanceof URLSearchParams
    ? new URLSearchParams(params)
    : { ...params };
}

function mergeOptions(
  defaults: HttpRequestOptions,
  options: HttpRequestOptions = {},
): HttpRequestOptions {
  // undefined 表示未覆盖；false、0 等显式配置仍优先于声明默认值。
  const overrides = Object.fromEntries(
    Object.entries(options).filter(([, value]) => value !== undefined),
  );
  return {
    ...defaults,
    ...overrides,
    headers: mergeHeaders(defaults.headers, options.headers),
    params: copyParams(options.params ?? defaults.params),
    signal: options.signal,
  };
}

/** 声明只绑定客户端；每次调用通过原请求入口读取当前会话并独立执行。 */
export function createHttpApi(client: Pick<HttpClient, 'request'>): HttpApi {
  function declare<TInput, TResponse, TOptions extends HttpRequestOptions>(
    method: HttpMethod,
    url: string,
    options: HttpApiDeclarationOptions = {},
    query: boolean,
  ): HttpApiRequest<TInput, TResponse, TOptions> {
    const defaults: HttpRequestOptions = {
      ...options,
      headers: mergeHeaders(options.headers),
      params: query ? undefined : copyParams(options.params),
      signal: undefined,
    };
    return async (
      ...[input, callOptions]: Parameters<
        HttpApiRequest<TInput, TResponse, TOptions>
      >
    ) => {
      const merged = mergeOptions(defaults, callOptions);
      if (query) {
        // 普通 DTO 接口无需声明字符串索引签名；实际查询序列化仍交给传输层。
        const params = copyParams(input as HttpRequestOptions['params']);
        return client.request<TResponse>({
          ...merged,
          method,
          url,
          params,
          data: undefined,
        });
      }
      return client.request<TInput | undefined, TResponse>({
        ...merged,
        method,
        url,
        data: input,
      });
    };
  }
  const queryMethod =
    (method: HttpMethod): HttpApiQueryMethod =>
    <TQuery extends object | void, TResponse>(
      url: string,
      options?: HttpApiQueryDeclarationOptions,
    ) =>
      declare<TQuery, TResponse, HttpApiQueryOptions>(
        method,
        url,
        options,
        true,
      );
  const bodyMethod =
    (method: HttpMethod): HttpApiBodyMethod =>
    <TBody, TResponse>(url: string, options?: HttpApiDeclarationOptions) =>
      declare<TBody, TResponse, HttpRequestOptions>(
        method,
        url,
        options,
        false,
      );
  return {
    get: queryMethod('GET'),
    head: queryMethod('HEAD'),
    delete: queryMethod('DELETE'),
    post: bodyMethod('POST'),
    put: bodyMethod('PUT'),
    patch: bodyMethod('PATCH'),
  };
}
