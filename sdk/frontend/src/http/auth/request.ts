import type { ClientOptions } from '../client/contracts.js';
import { abortable, throwIfCanceled } from '../error/cancellation.js';
import { HttpClientError } from '../error/http-client-error.js';
import type { HttpRequest } from '../request/contracts.js';
import { AuthRefreshResult } from './contracts.js';
import { getAuthCoordinator, isTrustedTarget } from './session.js';

/** HTTP 与 SSE 共用单次请求的认证边界，刷新代次仍由 AuthSession 协调。 */
export function createRequestAuth(
  options: ClientOptions,
  request: HttpRequest,
) {
  const authenticated =
    options.auth &&
    request.auth !== false &&
    isTrustedTarget(request.url, options.baseURL, options.auth.binding);
  const session = authenticated
    ? getAuthCoordinator(options.auth!.session)
    : undefined;
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
  const snapshot = session?.capture();
  let recovered = false;
  const checkActive = () => {
    throwIfCanceled(request.signal);
    if (snapshot) session!.assertCurrent(snapshot);
  };
  return {
    withCredentials,
    checkActive,
    async headers() {
      checkActive();
      return snapshot
        ? abortable(session!.headers(snapshot), request.signal)
        : {};
    },
    async recover(error: HttpClientError): Promise<boolean> {
      checkActive();
      if (!snapshot || !session!.canRefresh || request.authRecovery === false)
        return false;
      const shouldRefresh = session!.shouldRefresh(error);
      checkActive();
      if (!shouldRefresh) return false;
      if (!recovered) {
        recovered = true;
        const result = await abortable(
          session!.refresh(snapshot),
          request.signal,
        );
        checkActive();
        if (result === AuthRefreshResult.REFRESHED) return true;
      }
      await abortable(session!.notify(snapshot, error), request.signal);
      // 退出回调本身可以推进 epoch；仍向发起者交付导致退出的响应错误。
      throw error;
    },
  };
}
