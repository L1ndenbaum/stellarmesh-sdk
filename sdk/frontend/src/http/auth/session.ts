import axios from 'axios';
import { mergeHeaders, normalizeError } from '../transport/axios-transport.js';
import { HttpClientError } from '../error/http-client-error.js';
import { AuthRefreshResult } from './contracts.js';
import type { HttpHeaders } from '../request/contracts.js';
import type {
  AuthBindingOptions,
  AuthSession,
  AuthSessionContext,
  AuthSessionEpoch,
  AuthSessionOptions,
} from './contracts.js';

interface RefreshRound {
  refresh?: Promise<AuthRefreshResult>;
  notification?: Promise<void>;
}

interface EpochState {
  epoch: AuthSessionEpoch;
  round: RefreshRound;
}

interface AuthSnapshot {
  state: EpochState;
  round: RefreshRound;
  context: AuthSessionContext;
}

function normalizeAuthError(cause: unknown, message: string): HttpClientError {
  if (cause instanceof HttpClientError || axios.isAxiosError(cause)) {
    return normalizeError(cause);
  }
  return new HttpClientError(message, { kind: 'auth', cause });
}

function createAuthCoordinator(options: AuthSessionOptions) {
  let current: EpochState | undefined;

  function epoch(): AuthSessionEpoch {
    try {
      return options.getSessionEpoch();
    } catch (cause) {
      throw normalizeAuthError(cause, '读取会话标识失败');
    }
  }

  function assertCurrent(snapshot: AuthSnapshot): void {
    if (!Object.is(snapshot.context.epoch, epoch())) {
      throw new HttpClientError('请求所属会话已变化', {
        kind: 'session-changed',
      });
    }
  }

  return {
    canRefresh: Boolean(options.refreshSession),
    capture(): AuthSnapshot {
      const value = epoch();
      if (!current || !Object.is(current.epoch, value)) {
        current = { epoch: value, round: {} };
      }
      return {
        state: current,
        round: current.round,
        context: Object.freeze({ epoch: value }),
      };
    },
    assertCurrent,
    async headers(snapshot: AuthSnapshot): Promise<HttpHeaders> {
      assertCurrent(snapshot);
      try {
        const headers = options.getAuthHeaders
          ? await options.getAuthHeaders(snapshot.context)
          : {};
        assertCurrent(snapshot);
        if (
          !headers ||
          typeof headers !== 'object' ||
          (Object.getPrototypeOf(headers) !== Object.prototype &&
            Object.getPrototypeOf(headers) !== null) ||
          Object.entries(headers).some(
            ([name, value]) =>
              !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) ||
              typeof value !== 'string' ||
              /[\r\n]/.test(value),
          )
        ) {
          throw new TypeError(
            'getAuthHeaders 必须返回名称有效且值为字符串的请求头对象',
          );
        }
        return mergeHeaders(headers);
      } catch (cause) {
        assertCurrent(snapshot);
        throw normalizeAuthError(cause, '读取认证请求头失败');
      }
    },
    shouldRefresh(error: HttpClientError): boolean {
      if (
        !options.shouldRefresh ||
        (error.kind !== 'http' && error.kind !== 'business')
      )
        return false;
      try {
        const result = options.shouldRefresh(error);
        if (typeof result !== 'boolean') {
          // JS 误传异步谓词时观察其拒绝，仍按同步契约错误终止本次请求。
          void Promise.resolve(result).catch(() => {});
          throw new TypeError('shouldRefresh 必须返回布尔值');
        }
        return result;
      } catch (cause) {
        throw normalizeAuthError(cause, '判断认证恢复条件失败');
      }
    },
    refresh(snapshot: AuthSnapshot): Promise<AuthRefreshResult> {
      assertCurrent(snapshot);
      const { state, round, context } = snapshot;
      if (!round.refresh) {
        round.refresh = Promise.resolve()
          .then(async () => {
            assertCurrent(snapshot);
            try {
              const result = await options.refreshSession!(context);
              assertCurrent(snapshot);
              if (
                result !== AuthRefreshResult.REFRESHED &&
                result !== AuthRefreshResult.EXPIRED
              ) {
                throw new TypeError(
                  'refreshSession 必须返回 AuthRefreshResult 中的结果',
                );
              }
              return result;
            } catch (cause) {
              assertCurrent(snapshot);
              throw normalizeAuthError(cause, '刷新会话失败');
            }
          })
          .finally(() => {
            // 旧请求持有原代次并复用其结果；新请求可再次恢复，不保留无界历史。
            // 这里只推进该 epoch 的状态，不能清理新账号正在进行的刷新。
            if (state.round === round) state.round = {};
          });
      }
      return round.refresh;
    },
    notify(snapshot: AuthSnapshot, error: HttpClientError): Promise<void> {
      assertCurrent(snapshot);
      if (!options.onUnauthorized) return Promise.resolve();
      if (!snapshot.round.notification) {
        snapshot.round.notification = Promise.resolve()
          .then(() => {
            assertCurrent(snapshot);
            return options.onUnauthorized!(error, snapshot.context);
          })
          .catch((cause) => {
            throw normalizeAuthError(cause, '未授权回调失败');
          });
      }
      return snapshot.round.notification;
    },
  };
}

const sessions = new WeakMap<
  AuthSession,
  ReturnType<typeof createAuthCoordinator>
>();

export function createAuthSession(options: AuthSessionOptions): AuthSession {
  if (!options || typeof options !== 'object') {
    throw new TypeError('createAuthSession 需要会话配置');
  }
  const config = { ...options };
  if ('getAccessToken' in config) {
    throw new TypeError('getAccessToken 已移除，请使用 getAuthHeaders');
  }
  if (typeof config.getSessionEpoch !== 'function') {
    throw new TypeError('getSessionEpoch 必须为函数');
  }
  for (const name of [
    'getAuthHeaders',
    'shouldRefresh',
    'refreshSession',
    'onUnauthorized',
  ] as const) {
    if (config[name] !== undefined && typeof config[name] !== 'function') {
      throw new TypeError(`${name} 必须为函数`);
    }
  }
  if (
    (config.shouldRefresh !== undefined) !==
    (config.refreshSession !== undefined)
  ) {
    throw new TypeError('shouldRefresh 和 refreshSession 必须同时提供');
  }
  if (
    config.onUnauthorized !== undefined &&
    config.refreshSession === undefined
  ) {
    throw new TypeError('onUnauthorized 需要启用认证恢复');
  }
  const session = Object.freeze({}) as AuthSession;
  sessions.set(session, createAuthCoordinator(config));
  return session;
}

export function getAuthCoordinator(session: AuthSession) {
  const coordinator = sessions.get(session);
  if (!coordinator)
    throw new TypeError('withAuth 需要 createAuthSession 创建的会话');
  return coordinator;
}

export function isTrustedTarget(
  url: string,
  baseURL: string | undefined,
  options: AuthBindingOptions,
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
