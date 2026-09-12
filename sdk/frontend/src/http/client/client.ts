import {
  createTransport,
  mergeHeaders,
  normalizeError,
  send,
} from '../transport/axios-transport.js';
import { AuthRefreshResult } from '../auth/contracts.js';
import { getAuthCoordinator, isTrustedTarget } from '../auth/session.js';
import { abortable, throwIfCanceled } from '../error/cancellation.js';
import { HttpClientError } from '../error/http-client-error.js';
import { extractErrorCode } from '../error/extract-error-code.js';
import { retryDelay, validateNumber, waitForRetry } from '../retry/policy.js';
import type { HttpRequest } from '../request/contracts.js';
import type { HttpResponse } from '../response/contracts.js';
import type { ClientOptions } from './contracts.js';

/** 内部执行器始终返回元信息，公开声明入口决定最终交付形态。 */
export function createExecutor(options: ClientOptions) {
  const transport = createTransport(options.baseURL);
  const session = options.auth
    ? getAuthCoordinator(options.auth.session)
    : undefined;
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
    const withCredentials = Boolean(
      authenticated && options.auth!.binding.withCredentials,
    );
    if (
      withCredentials &&
      (typeof XMLHttpRequest === 'undefined' || typeof location === 'undefined')
    ) {
      throw new HttpClientError('跨源 Cookie 会话仅支持浏览器环境', {
        kind: 'auth',
      });
    }
    const snapshot = authenticated ? session!.capture() : undefined;
    const checkActive = () => {
      throwIfCanceled(request.signal);
      if (snapshot) session!.assertCurrent(snapshot);
    };
    let authHeaders = snapshot
      ? await abortable(session!.headers(snapshot), request.signal)
      : {};
    let recovered = false;
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
        if (
          snapshot &&
          session!.canRefresh &&
          request.authRecovery !== false &&
          session!.shouldRefresh(error)
        ) {
          checkActive();
          if (!recovered) {
            recovered = true;
            // 回调异常直接离开循环，不通知退出，也不能当成业务请求的传输失败重试。
            const result = await abortable(
              session!.refresh(snapshot),
              request.signal,
            );
            checkActive();
            if (result === AuthRefreshResult.REFRESHED) {
              // 刷新只报告恢复结果；认证头重新读取，Cookie 由浏览器管理。
              authHeaders = await abortable(
                session!.headers(snapshot),
                request.signal,
              );
              checkActive();
              continue;
            }
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
  return execute;
}
