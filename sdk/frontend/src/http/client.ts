import {
  createTransport,
  mergeHeaders,
  normalizeError,
  send,
} from './axios-transport.js';
import { createAuthCoordinator, isTrustedTarget } from './auth.js';
import { abortable, HttpClientError, throwIfCanceled } from './errors.js';
import {
  defaultRetry,
  retryDelay,
  validateNumber,
  waitForRetry,
} from './retry.js';
import type { RetryPolicy } from './retry.js';
import type {
  AuthOptions,
  ConfigurableHttpClient,
  HttpHeaders,
  HttpRequest,
  HttpRequestOptions,
  HttpResponse,
  ResponseTransform,
} from './types.js';

interface ClientOptions {
  baseURL?: string;
  headers?: HttpHeaders;
  timeout: number;
  retry: RetryPolicy;
  auth?: AuthOptions;
  transform?: ResponseTransform;
}
function createClient(options: ClientOptions): ConfigurableHttpClient {
  const transport = createTransport(options.baseURL);
  const session = createAuthCoordinator(options.auth);
  const derive = (changes: Partial<ClientOptions>) =>
    createClient({ ...options, ...changes });

  async function execute(input: HttpRequest): Promise<HttpResponse<unknown>> {
    const request = {
      ...input,
      timeout: input.timeout ?? options.timeout,
      headers: mergeHeaders(options.headers, input.headers),
    };
    validateNumber(request.timeout, 'timeout');
    validateNumber(
      request.maxRetries ?? options.retry.maxRetries,
      'maxRetries',
      true,
    );
    throwIfCanceled(request.signal);
    const authenticated = Boolean(
      options.auth &&
      request.auth !== false &&
      isTrustedTarget(request.url, options.baseURL, options.auth),
    );
    const observedVersion = session.version;
    let token = authenticated
      ? await abortable(session.token(), request.signal)
      : null;
    let refreshed = false;
    let retries = 0;
    while (true) {
      throwIfCanceled(request.signal);
      const headers = mergeHeaders(request.headers);
      if (token) headers.authorization = `Bearer ${token}`;
      let response: HttpResponse<unknown>;
      try {
        response = await send(transport, request, headers);
      } catch (rawError) {
        const error = normalizeError(rawError);
        throwIfCanceled(request.signal);
        if (error.status === 401 && authenticated) {
          let terminalError = error;
          if (!refreshed && options.auth?.refreshSession) {
            refreshed = true;
            try {
              token = await abortable(
                session.refresh(observedVersion),
                request.signal,
              );
              if (token) continue;
            } catch (refreshError) {
              terminalError = normalizeError(refreshError);
              throwIfCanceled(request.signal);
            }
          }
          await abortable(session.notify(terminalError), request.signal);
          throw terminalError;
        }
        const delay = retryDelay(error, request, retries, options.retry);
        if (delay === null) throw error;
        retries += 1;
        await waitForRetry(delay, request.signal);
        continue;
      }
      throwIfCanceled(request.signal);
      // 响应处理在传输重试之外：业务错误或转换异常不能触发重复写入。
      if (response.status === 204 || request.method === 'HEAD') {
        response.data = undefined;
      } else if (
        options.transform &&
        request.responseMode !== 'raw' &&
        (request.responseType === undefined || request.responseType === 'json')
      ) {
        try {
          response.data = await abortable(
            Promise.resolve(options.transform(response.data, response)),
            request.signal,
          );
        } catch (cause) {
          if (cause instanceof HttpClientError) throw cause;
          throw new HttpClientError('响应转换失败', {
            kind: 'response-format',
            status: response.status,
            data: response.data,
            headers: response.headers,
            cause,
          });
        }
      }
      return response;
    }
  }
  async function request<TRequest, TResponse>(
    config: HttpRequest<TRequest>,
  ): Promise<TResponse> {
    return (await execute(config)).data as TResponse;
  }
  async function requestWithMetadata<TRequest, TResponse>(
    config: HttpRequest<TRequest>,
  ): Promise<HttpResponse<TResponse>> {
    return (await execute(config)) as HttpResponse<TResponse>;
  }
  const bodyMethod =
    (method: 'POST' | 'PUT' | 'PATCH') =>
    <TRequest, TResponse>(
      url: string,
      data?: TRequest,
      config?: HttpRequestOptions,
    ): Promise<TResponse> =>
      request<TRequest, TResponse>({ ...config, method, url, data });
  return {
    request,
    requestWithMetadata,
    get: <T>(url: string, config?: HttpRequestOptions) =>
      request<never, T>({ ...config, method: 'GET', url }),
    head: <T>(url: string, config?: HttpRequestOptions) =>
      request<never, T>({ ...config, method: 'HEAD', url }),
    delete: <T>(url: string, config?: HttpRequestOptions) =>
      request<never, T>({ ...config, method: 'DELETE', url }),
    post: bodyMethod('POST'),
    put: bodyMethod('PUT'),
    patch: bodyMethod('PATCH'),
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
    withAuth: (auth) =>
      derive({
        auth: {
          ...auth,
          trustedOrigins: auth.trustedOrigins
            ? [...auth.trustedOrigins]
            : undefined,
        },
      }),
    withResponseTransform: (transform) => derive({ transform }),
  };
}

export const httpClient: ConfigurableHttpClient = createClient({
  timeout: 0,
  retry: defaultRetry,
});
