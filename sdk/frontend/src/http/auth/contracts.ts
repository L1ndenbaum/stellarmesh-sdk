import type { HttpClientError } from '../error/http-client-error.js';

declare const authSessionBrand: unique symbol;

/** 仅由 createAuthSession 创建；可跨客户端共享，但不能跨用户请求共享。 */
export interface AuthSession {
  readonly [authSessionBrand]: true;
}

export type AuthSessionEpoch = string | number;

export interface AuthSessionContext {
  readonly epoch: AuthSessionEpoch;
}

export interface AuthSessionOptions {
  /** 登录、退出、切换账号时更换且不可复用；正常 Token 刷新不更换。 */
  getSessionEpoch(): AuthSessionEpoch;
  getAccessToken(): string | null | Promise<string | null>;
  /** 按 epoch 条件保存后返回新 Token；null 确认失效，异常仅使本次操作失败。 */
  refreshSession?(context: AuthSessionContext): Promise<string | null>;
  shouldRefresh?(error: HttpClientError): boolean;
  /** 项目在实际清理凭证时也必须检查 epoch，避免异步清理影响新会话。 */
  onUnauthorized?(
    error: HttpClientError,
    context: AuthSessionContext,
  ): void | Promise<void>;
}

export interface AuthBindingOptions {
  trustedOrigins?: readonly string[];
}
