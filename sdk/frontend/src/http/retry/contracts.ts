/** HTTP 退避配置；默认仅 GET／HEAD 可重试，SSE 不使用该策略。 */
export interface RetryOptions {
  /** 非负整数，默认 0；表示首次请求之后的额外次数。 */
  maxRetries?: number;
  /** 抖动指数退避的基础延迟，非负毫秒，默认 300。 */
  baseDelayMs?: number;
  /** 延迟上限，非负毫秒，默认 10000；Retry-After 超过它时不重试。 */
  maxDelayMs?: number;
}
