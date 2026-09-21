import type { HttpHeaders, HttpRequestOptions } from '../request/contracts';

export interface SseMessage {
  data: string;
  event: string;
  /** 当前事件 ID，未指定时沿用此前的值；空 id 字段重置为空字符串。 */
  id: string;
  /** 最近一次有效的 retry 字段，仅暴露数值，不启动重连。 */
  retry?: number;
}

export interface SseOptions {
  headers?: HttpHeaders;
  params?: HttpRequestOptions['params'];
  /** 仅限制每次建连至收到响应头的时间，0 表示不限制。 */
  timeout?: number;
  auth?: boolean;
  authRecovery?: boolean;
  signal?: AbortSignal;
}

export interface SseDeclarationOptions extends Omit<SseOptions, 'signal'> {
  signal?: never;
}

export interface SseQueryOptions extends SseOptions {
  params?: never;
}

export interface SseQueryDeclarationOptions extends SseDeclarationOptions {
  params?: never;
}

export type SseRequest<TInput, TOptions = SseOptions> = (
  ...args: [TInput] extends [void]
    ? [input?: undefined, options?: TOptions]
    : [input: TInput, options?: TOptions]
) => AsyncIterable<SseMessage>;

/** 映射同步执行一次，认证恢复复用同一 URL、查询参数及序列化后的请求体。 */
export type SseRequestDescriptor = {
  url: string;
  headers?: HttpHeaders;
  params?: HttpRequestOptions['params'];
  signal?: never;
} & ({ method: 'GET'; data?: never } | { method: 'POST'; data?: unknown });

export interface SseApi {
  get<TQuery extends object | void>(
    url: string,
    options?: SseQueryDeclarationOptions,
  ): SseRequest<TQuery, SseQueryOptions>;
  post<TBody>(url: string, options?: SseDeclarationOptions): SseRequest<TBody>;
  request<TInput>(
    resolve: (input: TInput) => SseRequestDescriptor,
    options?: SseQueryDeclarationOptions,
  ): SseRequest<TInput, SseQueryOptions>;
}
