import type { AuthBindingOptions, AuthSession } from '../auth/contracts';
import type { ErrorCodeExtractor } from '../error/contracts';
import type { HttpHeaders } from '../request/contracts';
import type { ResponseTransform } from '../response/contracts';
import type { RetryPolicy } from '../retry/policy';

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
