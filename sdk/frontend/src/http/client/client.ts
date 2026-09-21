import {
  createTransport,
  mergeHeaders,
  normalizeError,
  send,
} from '../transport/axios-transport';
import { createRequestAuth } from '../auth/request';
import { abortable, throwIfCanceled } from '../error/cancellation';
import { HttpClientError } from '../error/http-client-error';
import { extractErrorCode } from '../error/extract-error-code';
import { retryDelay, validateNumber, waitForRetry } from '../retry/policy';
import type { HttpRequest } from '../request/contracts';
import type { HttpResponse } from '../response/contracts';
import type { ClientOptions } from './contracts';

/** 内部执行器始终返回元信息，公开声明入口决定最终交付形态。 */
export function createExecutor(options: ClientOptions) {
  const transport = createTransport(options.baseURL);
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
    const auth = createRequestAuth(options, request);
    const { checkActive, withCredentials } = auth;
    let authHeaders = await auth.headers();
    let retries = 0;
    while (true) {
      checkActive();
      const headers = mergeHeaders(request.headers, authHeaders);
      let transportFailed = false;
      try {
        let response: HttpResponse<unknown>;
        try {
          response = await send(transport, request, headers, withCredentials);
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
        let error: HttpClientError;
        try {
          error = extractErrorCode(
            normalizeError(rawError),
            options.errorCodeExtractor,
          );
        } finally {
          // 项目回调可能同步取消请求或切换会话，仍遵循原来的会话边界。
          checkActive();
        }
        if (await auth.recover(error)) {
          authHeaders = await auth.headers();
          continue;
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
  return execute;
}
