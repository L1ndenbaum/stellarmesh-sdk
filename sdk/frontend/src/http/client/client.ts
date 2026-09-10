import {
  createTransport,
  mergeHeaders,
  normalizeError,
  send,
} from '../transport/axios-transport.js';
import { getAuthCoordinator, isTrustedTarget } from '../auth/session.js';
import type { AuthBindingOptions, AuthSession } from '../auth/contracts.js';
import { abortable, throwIfCanceled } from '../error/cancellation.js';
import { HttpClientError } from '../error/http-client-error.js';
import {
  defaultRetry,
  retryDelay,
  validateNumber,
  waitForRetry,
} from '../retry/policy.js';
import type { RetryPolicy } from '../retry/policy.js';
import type {
  HttpHeaders,
  HttpRequest,
  HttpRequestOptions,
} from '../request/contracts.js';
import type { HttpResponse, ResponseTransform } from '../response/contracts.js';
import type { ConfigurableHttpClient } from './contracts.js';

interface ClientOptions {
  baseURL?: string;
  headers?: HttpHeaders;
  timeout: number;
  retry: RetryPolicy;
  auth?: { session: AuthSession; binding: AuthBindingOptions };
  transform?: ResponseTransform;
}

function createClient(options: ClientOptions): ConfigurableHttpClient {
  const transport = createTransport(options.baseURL);
  const session = options.auth
    ? getAuthCoordinator(options.auth.session)
    : undefined;
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
    const authenticated =
      options.auth &&
      request.auth !== false &&
      isTrustedTarget(request.url, options.baseURL, options.auth.binding);
    const snapshot = authenticated ? session!.capture() : undefined;
    const checkActive = () => {
      throwIfCanceled(request.signal);
      if (snapshot) session!.assertCurrent(snapshot);
    };
    let token = snapshot
      ? await abortable(session!.token(snapshot), request.signal)
      : null;
    let recovered = false;
    let retries = 0;
    while (true) {
      checkActive();
      const headers = mergeHeaders(request.headers);
      if (token) headers.authorization = `Bearer ${token}`;
      let transportFailed = false;
      try {
        let response: HttpResponse<unknown>;
        try {
          response = await send(transport, request, headers);
        } catch (error) {
          transportFailed = true;
          throw error;
        }
        checkActive();
        if (response.status === 204 || request.method === 'HEAD') {
          response.data = undefined;
        } else if (
          options.transform &&
          request.responseMode !== 'raw' &&
          (request.responseType === undefined ||
            request.responseType === 'json')
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
        checkActive();
        return response;
      } catch (rawError) {
        checkActive();
        const error = normalizeError(rawError);
        if (
          snapshot &&
          request.authRecovery !== false &&
          session!.shouldRefresh(error)
        ) {
          checkActive();
          if (!session!.canRefresh) throw error;
          if (!recovered) {
            recovered = true;
            // 回调异常直接离开循环，不通知退出，也不能当成业务请求的传输失败重试。
            token = await abortable(session!.refresh(snapshot), request.signal);
            checkActive();
            if (token !== null) continue;
          }
          await abortable(session!.notify(snapshot, error), request.signal);
          throw error;
        }
        checkActive();
        // 只有真实传输失败消耗普通重试额度；信封恢复需显式命中认证谓词。
        const delay = transportFailed
          ? retryDelay(error, request, retries, options.retry)
          : null;
        if (delay === null) throw error;
        retries += 1;
        await waitForRetry(delay, request.signal);
      }
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
    withAuth: (auth, binding = {}) => {
      getAuthCoordinator(auth);
      return derive({
        auth: {
          session: auth,
          binding: {
            trustedOrigins: binding.trustedOrigins
              ? [...binding.trustedOrigins]
              : undefined,
          },
        },
      });
    },
    withResponseTransform: (transform) => derive({ transform }),
  };
}

export const httpClient: ConfigurableHttpClient = createClient({
  timeout: 0,
  retry: defaultRetry,
});
