import { HttpClientError } from './errors.js';
import type { AuthOptions } from './types.js';

export function isTrustedTarget(
  url: string,
  baseURL: string | undefined,
  options: AuthOptions,
): boolean {
  const browserOrigin =
    typeof location === 'undefined' ? undefined : location.origin;
  try {
    const base = baseURL ? new URL(baseURL, browserOrigin) : undefined;
    // 使用 URL 解析同时处理绝对地址和 //host，不能仅靠 http 前缀判定跨域。
    const target = new URL(url, base ?? browserOrigin);
    if (target.protocol !== 'http:' && target.protocol !== 'https:')
      return false;
    const trusted =
      options.trustedOrigins ??
      (base ? [base.origin] : browserOrigin ? [browserOrigin] : []);
    return trusted.some((origin) => new URL(origin).origin === target.origin);
  } catch {
    return false;
  }
}

export function createAuthCoordinator(options?: AuthOptions) {
  let version = 0;
  let outcome: string | null = null;
  let failure: HttpClientError | undefined;
  let refreshing: Promise<string | null> | undefined;
  let notifiedVersion = -1;
  let notifying: Promise<void> | undefined;
  return {
    get version() {
      return version;
    },
    async token(): Promise<string | null> {
      try {
        return (await options?.getAccessToken()) ?? null;
      } catch (cause) {
        throw new HttpClientError('读取会话失败', { kind: 'auth', cause });
      }
    },
    async refresh(observedVersion: number): Promise<string | null> {
      if (observedVersion !== version) {
        if (failure) throw failure;
        return outcome;
      }
      if (!refreshing) {
        refreshing = (async () => {
          try {
            outcome = (await options?.refreshSession?.()) ?? null;
            failure = undefined;
            return outcome;
          } catch (cause) {
            failure = new HttpClientError('刷新会话失败', {
              kind: 'auth',
              cause,
            });
            throw failure;
          } finally {
            version += 1;
          }
        })().finally(() => {
          refreshing = undefined;
        });
      }
      return refreshing;
    },
    async notify(error: unknown): Promise<void> {
      if (!options?.onUnauthorized) return;
      if (notifiedVersion === version) return notifying;
      notifiedVersion = version;
      notifying = Promise.resolve()
        .then(() => options.onUnauthorized?.(error))
        .then(() => undefined)
        .catch((cause) => {
          throw new HttpClientError('未授权回调失败', { kind: 'auth', cause });
        });
      return notifying;
    },
  };
}
