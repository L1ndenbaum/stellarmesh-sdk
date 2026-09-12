import { getAuthCoordinator } from '../auth/session.js';
import { createExecutor } from '../client/client.js';
import type { ClientOptions } from '../client/contracts.js';
import { throwIfCanceled } from '../error/cancellation.js';
import { defaultRetry, validateNumber } from '../retry/policy.js';
import { HttpMethod, type HttpRequestOptions } from '../request/contracts.js';
import { mergeHeaders, normalizeError } from '../transport/axios-transport.js';
import type {
  HttpApi,
  HttpApiBodyMethod,
  HttpApiDeclarationOptions,
  HttpApiQueryDeclarationOptions,
  HttpApiQueryMethod,
  HttpApiQueryOptions,
  HttpApiRequest,
  HttpApiRequestDescriptor,
  HttpApiResult,
} from './contracts.js';

function copyParams(
  params: HttpRequestOptions['params'],
): HttpRequestOptions['params'] {
  if (params === undefined) return undefined;
  // 只复制查询容器，不改变嵌套 DTO 的序列化语义。
  return params instanceof URLSearchParams
    ? new URLSearchParams(params)
    : { ...params };
}

function createApi<TMetadata extends boolean>(
  options: ClientOptions,
  metadata: TMetadata,
): HttpApi<TMetadata> {
  const execute = createExecutor(options);
  const derive = (changes: Partial<ClientOptions>) =>
    createApi({ ...options, ...changes }, metadata);

  function declare<TInput, TResponse, TOptions extends HttpRequestOptions>(
    resolve: (input: TInput) => HttpApiRequestDescriptor,
    config: HttpApiDeclarationOptions = {},
    allowParams = false,
  ): HttpApiRequest<TInput, HttpApiResult<TResponse, TMetadata>, TOptions> {
    const defaults: HttpRequestOptions = {
      ...config,
      headers: mergeHeaders(config.headers),
      params: allowParams ? copyParams(config.params) : undefined,
      signal: undefined,
    };
    return async (
      ...[input, invocation]: Parameters<
        HttpApiRequest<TInput, HttpApiResult<TResponse, TMetadata>, TOptions>
      >
    ) => {
      const callOptions: HttpRequestOptions = invocation ?? {};
      try {
        throwIfCanceled(callOptions.signal);
        // 每个逻辑调用只映射一次，刷新重放和传输重试复用同一份请求。
        const descriptor = resolve(input as TInput);
        // JS 调用者也不能因遗漏 URL 或误用异步映射而向 baseURL 意外发请求。
        if (
          !descriptor ||
          typeof descriptor.url !== 'string' ||
          !Object.values(HttpMethod).includes(descriptor.method)
        ) {
          throw new TypeError('请求映射必须同步返回有效的 method 和 url');
        }
        const overrides = Object.fromEntries(
          Object.entries(callOptions).filter(
            ([, value]) => value !== undefined,
          ),
        );
        const response = await execute({
          ...defaults,
          ...overrides,
          method: descriptor.method,
          url: descriptor.url,
          data: descriptor.data,
          params: copyParams(
            allowParams
              ? (callOptions.params ?? defaults.params)
              : descriptor.params,
          ),
          headers: mergeHeaders(
            defaults.headers,
            descriptor.headers,
            callOptions.headers,
          ),
          signal: callOptions.signal,
        });
        // 返回模式由不可变声明入口确定，类型参数描述转换后的 data。
        return (metadata ? response : response.data) as HttpApiResult<
          TResponse,
          TMetadata
        >;
      } catch (cause) {
        throw normalizeError(cause);
      }
    };
  }
  const queryMethod =
    (method: HttpMethod): HttpApiQueryMethod<TMetadata> =>
    <TQuery extends object | void, TResponse>(
      url: string,
      config?: HttpApiQueryDeclarationOptions,
    ) =>
      declare<TQuery, TResponse, HttpApiQueryOptions>(
        // 普通查询 DTO 接口无需字符串索引签名，序列化仍由传输层负责。
        (input) => ({
          method,
          url,
          params: input as HttpRequestOptions['params'],
        }),
        config,
      );
  const bodyMethod =
    (method: HttpMethod): HttpApiBodyMethod<TMetadata> =>
    <TBody, TResponse>(url: string, config?: HttpApiDeclarationOptions) =>
      declare<TBody, TResponse, HttpRequestOptions>(
        (data) => ({ method, url, data }),
        config,
        true,
      );
  return {
    get: queryMethod('GET'),
    head: queryMethod('HEAD'),
    delete: queryMethod('DELETE'),
    post: bodyMethod('POST'),
    put: bodyMethod('PUT'),
    patch: bodyMethod('PATCH'),
    request: (resolve, config) => declare(resolve, config),
    withMetadata: () => createApi(options, true),
    withBaseURL: (baseURL) => derive({ baseURL }),
    withHeaders: (headers) =>
      derive({ headers: mergeHeaders(options.headers, headers) }),
    withTimeout: (timeout) => {
      validateNumber(timeout, 'timeout');
      return derive({ timeout });
    },
    withMaxRetries: (maxRetries) => {
      validateNumber(maxRetries, 'maxRetries', true);
      return derive({ retry: { ...options.retry, maxRetries } });
    },
    withRetry: (config) => {
      const retry = { ...options.retry, ...config };
      validateNumber(retry.maxRetries, 'maxRetries', true);
      validateNumber(retry.baseDelayMs, 'baseDelayMs');
      validateNumber(retry.maxDelayMs, 'maxDelayMs');
      return derive({ retry });
    },
    withAuth: (session, binding = {}) => {
      getAuthCoordinator(session);
      if (
        binding.withCredentials !== undefined &&
        typeof binding.withCredentials !== 'boolean'
      ) {
        throw new TypeError('withCredentials 必须为布尔值');
      }
      return derive({
        auth: {
          session,
          binding: {
            withCredentials: binding.withCredentials ?? false,
            trustedOrigins: binding.trustedOrigins
              ? [...binding.trustedOrigins]
              : undefined,
          },
        },
      });
    },
    withResponseTransform: (transform) => derive({ transform }),
    withErrorCodeExtractor: (extractor) => {
      if (typeof extractor !== 'function') {
        throw new TypeError('withErrorCodeExtractor 需要同步提取函数');
      }
      return derive({ errorCodeExtractor: extractor });
    },
  };
}

/** 根入口不绑定用户会话，配置派生与接口声明均不会发送请求。 */
export const http: HttpApi = createApi(
  { timeout: 0, retry: defaultRetry },
  false,
);
