import type { ClientOptions } from '../client/contracts.js';
import { createRequestAuth } from '../auth/request.js';
import { abortable, throwIfCanceled } from '../error/cancellation.js';
import { extractErrorCode } from '../error/extract-error-code.js';
import { HttpClientError } from '../error/http-client-error.js';
import type { HttpRequest } from '../request/contracts.js';
import { validateNumber } from '../retry/policy.js';
import {
  createTransport,
  mergeHeaders,
  normalizeError,
} from '../transport/axios-transport.js';
import type {
  SseApi,
  SseDeclarationOptions,
  SseMessage,
  SseOptions,
  SseRequest,
  SseRequestDescriptor,
} from './contracts.js';
import { createSseParser } from './parser.js';

function copyParams(params: SseOptions['params']): SseOptions['params'] {
  return params instanceof URLSearchParams
    ? new URLSearchParams(params)
    : params
      ? { ...params }
      : undefined;
}

async function responseError(
  response: Response,
  signal?: AbortSignal,
): Promise<HttpClientError> {
  let data: unknown;
  try {
    data = await abortable(response.text(), signal);
  } catch (cause) {
    throwIfCanceled(signal);
    throw new HttpClientError('读取 HTTP 错误响应失败', {
      kind: 'network',
      status: response.status,
      headers: Object.fromEntries(response.headers),
      cause,
    });
  }
  if (typeof data === 'string' && data) {
    try {
      data = JSON.parse(data) as unknown;
    } catch {
      /* 文本错误保留原文与 HTTP 状态。 */
    }
  }
  const object =
    data && typeof data === 'object'
      ? (data as Record<string, unknown>)
      : undefined;
  return new HttpClientError(
    typeof object?.message === 'string'
      ? object.message
      : `HTTP ${response.status}`,
    {
      kind: 'http',
      status: response.status,
      headers: Object.fromEntries(response.headers),
      data,
      apiCode:
        typeof object?.code === 'string' || typeof object?.code === 'number'
          ? object.code
          : undefined,
    },
  );
}

export function createSseApi(options: ClientOptions): SseApi {
  const transport = createTransport(options.baseURL);

  async function* execute(request: HttpRequest): AsyncGenerator<SseMessage> {
    throwIfCanceled(request.signal);
    const timeout = request.timeout ?? options.timeout;
    validateNumber(timeout, 'timeout');
    const auth = createRequestAuth(options, request);
    // 复用 Axios 的 URL 拼接和 params 编码，保留 baseURL 中的路径前缀。
    const url = transport.getUri({ url: request.url, params: request.params });
    const body =
      request.data === undefined ? undefined : JSON.stringify(request.data);
    const baseHeaders = mergeHeaders(
      { Accept: 'text/event-stream' },
      body === undefined ? undefined : { 'Content-Type': 'application/json' },
      options.headers,
      request.headers,
    );
    const controller = new AbortController();
    const cancel = () => controller.abort(request.signal?.reason);
    request.signal?.addEventListener('abort', cancel, { once: true });
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    let response: Response | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      if (request.signal?.aborted) cancel();
      let authHeaders = await auth.headers();
      while (true) {
        auth.checkActive();
        let timedOut = false;
        if (timeout > 0)
          timer = setTimeout(() => {
            timedOut = true;
            controller.abort();
          }, timeout);
        try {
          response = await fetch(url, {
            method: request.method,
            body,
            headers: mergeHeaders(baseHeaders, authHeaders),
            signal: controller.signal,
            credentials: auth.withCredentials ? 'include' : 'same-origin',
            redirect: 'error',
            cache: 'no-store',
          });
        } catch (cause) {
          auth.checkActive();
          throw new HttpClientError(
            timedOut ? 'SSE 建连超时' : 'SSE 建连失败',
            {
              kind: timedOut ? 'timeout' : 'network',
              cause,
            },
          );
        } finally {
          clearTimeout(timer);
          timer = undefined;
        }
        auth.checkActive();
        if (response.ok) break;
        let error: HttpClientError;
        try {
          error = extractErrorCode(
            await responseError(response, request.signal),
            options.errorCodeExtractor,
          );
        } finally {
          auth.checkActive();
        }
        // 只有建流前的 HTTP 错误可恢复；整个读流阶段在恢复循环之外。
        if (!(await auth.recover(error))) throw error;
        authHeaders = await auth.headers();
      }
      if (
        response.status !== 200 ||
        !response.body ||
        response.headers
          .get('content-type')
          ?.split(';', 1)[0]
          ?.trim()
          .toLowerCase() !== 'text/event-stream'
      ) {
        throw new HttpClientError('响应不是可读取的 SSE 事件流', {
          kind: 'response-format',
          status: response.status,
          headers: Object.fromEntries(response.headers),
        });
      }
      reader = response.body.getReader();
      const decoder = new TextDecoder();
      const parse = createSseParser();
      while (true) {
        auth.checkActive();
        let chunk: ReadableStreamReadResult<Uint8Array>;
        try {
          chunk = await abortable(reader.read(), request.signal);
        } catch (cause) {
          auth.checkActive();
          throw new HttpClientError('SSE 连接读取失败', {
            kind: 'network',
            cause,
          });
        }
        auth.checkActive();
        if (chunk.done) return;
        for (const message of parse(
          decoder.decode(chunk.value, { stream: true }),
        )) {
          auth.checkActive();
          yield message;
        }
      }
    } finally {
      clearTimeout(timer);
      request.signal?.removeEventListener('abort', cancel);
      controller.abort();
      // break、业务抛错和认证失败均释放响应体；清理失败不能覆盖原始错误。
      try {
        if (reader) await reader.cancel();
        else if (response?.body && !response.body.locked)
          await response.body.cancel();
      } catch {
        /* 连接可能已被浏览器关闭。 */
      }
      reader?.releaseLock();
    }
  }

  function declare<TInput, TOptions extends SseOptions>(
    resolve: (input: TInput) => SseRequestDescriptor,
    config: SseDeclarationOptions = {},
    allowParams = false,
  ): SseRequest<TInput, TOptions> {
    const defaults = {
      ...config,
      headers: mergeHeaders(config.headers),
      params: copyParams(config.params),
    };
    return (
      ...[input, invocation]: Parameters<SseRequest<TInput, TOptions>>
    ) => {
      const call = {
        ...invocation,
        headers: mergeHeaders(invocation?.headers),
        params: copyParams(invocation?.params),
      };
      // 一个调用只创建一个 generator；再次遍历不会重新发送请求。
      return (async function* () {
        try {
          throwIfCanceled(call.signal);
          const descriptor = resolve(input as TInput);
          if (
            !descriptor ||
            typeof descriptor.url !== 'string' ||
            !['GET', 'POST'].includes(descriptor.method) ||
            (descriptor.method === 'GET' && descriptor.data !== undefined)
          ) {
            throw new TypeError('SSE 映射必须同步返回有效的 GET 或 POST 请求');
          }
          yield* execute({
            method: descriptor.method,
            url: descriptor.url,
            data: descriptor.data,
            headers: mergeHeaders(
              defaults.headers,
              descriptor.headers,
              call.headers,
            ),
            params: copyParams(
              allowParams
                ? (call.params ?? defaults.params)
                : descriptor.params,
            ),
            timeout: call.timeout ?? defaults.timeout,
            auth: call.auth ?? defaults.auth,
            authRecovery: call.authRecovery ?? defaults.authRecovery,
            signal: call.signal,
          });
        } catch (cause) {
          throw normalizeError(cause);
        }
      })();
    };
  }

  return {
    get: (url, config) =>
      declare(
        (params) => ({
          method: 'GET',
          url,
          params: params as SseOptions['params'],
        }),
        config,
      ),
    post: (url, config) =>
      declare((data) => ({ method: 'POST', url, data }), config, true),
    request: (resolve, config) => declare(resolve, config),
  };
}
