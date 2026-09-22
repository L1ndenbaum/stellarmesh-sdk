import type { HttpClientError } from '../error/http-client-error';
import type { HttpHeaders } from '../request/contracts';

declare const authSessionBrand: unique symbol;

/** 仅由 createAuthSession 创建；可跨客户端共享，但不能跨用户请求共享。 */
export interface AuthSession {
  readonly [authSessionBrand]: true;
}

/** 业务维护的会话代号；不同登录会话不得复用旧值。 */
export type AuthSessionEpoch = string | number;

/** 当前逻辑请求捕获的会话；异步保存和清理仍须由业务核对 epoch。 */
export interface AuthSessionContext {
  readonly epoch: AuthSessionEpoch;
}

/** 业务完成刷新后的明确结果；临时故障应抛异常，而非返回 EXPIRED。 */
export const AuthRefreshResult = {
  REFRESHED: 'refreshed',
  EXPIRED: 'expired',
} as const;

export type AuthRefreshResult =
  (typeof AuthRefreshResult)[keyof typeof AuthRefreshResult];

interface AuthSessionBaseOptions {
  /** 登录、退出、切换账号时更换且不可复用；同一会话正常刷新不更换。 */
  getSessionEpoch(): AuthSessionEpoch;
  /** 返回当前认证或 CSRF 请求头；省略或返回空对象用于纯 Cookie 会话。 */
  getAuthHeaders?(
    context: AuthSessionContext,
  ): HttpHeaders | Promise<HttpHeaders>;
}

interface AuthRecoveryOptions {
  /** 同步业务判定，仅接收 HTTP／业务错误；SDK 不默认匹配 401。 */
  shouldRefresh(error: HttpClientError): boolean;
  /** 完成凭证条件保存或 Cookie 刷新后报告结果；异常仅使本次操作失败。 */
  refreshSession(context: AuthSessionContext): Promise<AuthRefreshResult>;
  /** 项目在实际清理凭证时也必须检查 epoch，避免异步清理影响新会话。 */
  onUnauthorized?(
    error: HttpClientError,
    context: AuthSessionContext,
  ): void | Promise<void>;
}

interface AuthWithoutRecoveryOptions {
  shouldRefresh?: never;
  refreshSession?: never;
  onUnauthorized?: never;
}

/** 刷新判断与执行必须成对提供，不隐式推断 HTTP 状态或业务错误码。 */
export type AuthSessionOptions = AuthSessionBaseOptions &
  (AuthRecoveryOptions | AuthWithoutRecoveryOptions);

/** 认证目标边界；不自动发现可信域名或提供 Node Cookie 容器。 */
export interface AuthBindingOptions {
  /** 省略时信任 baseURL 的 origin，无 baseURL 时使用浏览器 origin；显式列表替换默认范围。 */
  trustedOrigins?: readonly string[];
  /** 仅对可信目标启用浏览器跨源 Cookie；默认关闭，不控制同源 Cookie。 */
  withCredentials?: boolean;
}
