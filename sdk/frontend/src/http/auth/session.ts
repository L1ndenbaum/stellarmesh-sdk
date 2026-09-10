import axios from 'axios';
import { normalizeError } from '../transport/axios-transport.js';
import { HttpClientError } from '../error/http-client-error.js';
import type {
  AuthBindingOptions,
  AuthSession,
  AuthSessionContext,
  AuthSessionEpoch,
  AuthSessionOptions,
} from './contracts.js';

interface RefreshRound {
  refresh?: Promise<string | null>;
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
    async token(snapshot: AuthSnapshot): Promise<string | null> {
      assertCurrent(snapshot);
      try {
        const token = await options.getAccessToken();
        assertCurrent(snapshot);
        return token;
      } catch (cause) {
        assertCurrent(snapshot);
        throw normalizeAuthError(cause, '读取会话失败');
      }
    },
    shouldRefresh(error: HttpClientError): boolean {
      if (error.kind !== 'http' && error.kind !== 'business') return false;
      try {
        return options.shouldRefresh
          ? options.shouldRefresh(error)
          : error.kind === 'http' && error.status === 401;
      } catch (cause) {
        throw normalizeAuthError(cause, '判断认证恢复条件失败');
      }
    },
    refresh(snapshot: AuthSnapshot): Promise<string | null> {
      assertCurrent(snapshot);
      const { state, round, context } = snapshot;
      if (!round.refresh) {
        round.refresh = Promise.resolve()
          .then(async () => {
            assertCurrent(snapshot);
            try {
              const token = await options.refreshSession!(context);
              assertCurrent(snapshot);
              if (
                token !== null &&
                (typeof token !== 'string' || token.trim().length === 0)
              ) {
                throw new HttpClientError('刷新必须返回非空 Token 或 null', {
                  kind: 'auth',
                });
              }
              return token;
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
  const session = Object.freeze({}) as AuthSession;
  sessions.set(session, createAuthCoordinator({ ...options }));
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
