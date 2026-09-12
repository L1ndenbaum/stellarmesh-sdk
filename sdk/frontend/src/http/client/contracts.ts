import type { AuthBindingOptions, AuthSession } from '../auth/contracts.js';
import type { ErrorCodeExtractor } from '../error/contracts.js';
import type { HttpHeaders } from '../request/contracts.js';
import type { ResponseTransform } from '../response/contracts.js';
import type { RetryPolicy } from '../retry/policy.js';

/** 内部执行配置，不提供公开的立即调用契约。 */
export interface ClientOptions {
  baseURL?: string;
  headers?: HttpHeaders;
  timeout: number;
  retry: RetryPolicy;
  auth?: { session: AuthSession; binding: AuthBindingOptions };
  transform?: ResponseTransform;
  errorCodeExtractor?: ErrorCodeExtractor;
}
