import type { HttpHeaders, HttpRequestOptions } from '../request/contracts';

/** 原始 SSE 消息；JSON 解码、业务终态和 [DONE] 由调用方解释。 */
export interface SseMessage {
  /** 多行 data 以换行拼接，不额外 trim；EOF 丢弃未完成分帧。 */
  data: string;
  /** 未指定或为空时为 message。 */
  event: string;
  /** 当前事件 ID，未指定时沿用此前的值；空 id 字段重置为空字符串。 */
  id: string;
  /** 最近一次有效的 retry 字段，单位毫秒，仅暴露数值，不启动重连。 */
  retry?: number;
}

/** SSE 配置；不支持普通网络重试、响应转换与进度回调。 */
export interface SseOptions {
  /** 采用 HTTP 的不区分大小写合并与认证覆盖规则。 */
  headers?: HttpHeaders;
  /** POST 可补充查询参数；GET／动态声明由输入或描述符独占查询来源。 */
  params?: HttpRequestOptions['params'];
  /** 仅限制每次建连至收到响应头的时间，单位毫秒，0 表示不限制。 */
  timeout?: number;
  /** false 跳过 SDK 认证；浏览器默认同源 Cookie 不受此开关控制。 */
  auth?: boolean;
  /** false 关闭建流前认证恢复和退出通知，保留凭证携带。 */
  authRecovery?: boolean;
  /** 只属于单次调用；建流后使用它取消等待中的读取。 */
  signal?: AbortSignal;
}

/** 可复用声明默认配置，不允许绑定 AbortSignal。 */
export interface SseDeclarationOptions extends Omit<SseOptions, 'signal'> {
  signal?: never;
}

/** GET／动态调用配置，不允许第二个 params 来源。 */
export interface SseQueryOptions extends SseOptions {
  params?: never;
}

/** GET／动态声明配置，排除 signal 和 params。 */
export interface SseQueryDeclarationOptions extends SseDeclarationOptions {
  params?: never;
}

/**
 * 创建单次消费的异步迭代流；首次迭代才发请求，重复遍历不会再次发送。
 * @remarks break、消费方抛错和取消会关闭连接；流结束不表示业务完成。
 */
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

/** GET／POST 事件流入口；仅建流前 HTTP 失败可以按会话策略恢复一次。 */
export interface SseApi {
  /** 声明 GET，输入作为查询；网络错误及流中断均不自动重发。 */
  get<TQuery extends object | void>(
    url: string,
    options?: SseQueryDeclarationOptions,
  ): SseRequest<TQuery, SseQueryOptions>;
  /** 声明 POST，输入序列化为 JSON；成功建流后绝不自动重放。 */
  post<TBody>(url: string, options?: SseDeclarationOptions): SseRequest<TBody>;
  /** 声明动态 GET／POST 映射；首次迭代同步执行一次。 */
  request<TInput>(
    resolve: (input: TInput) => SseRequestDescriptor,
    options?: SseQueryDeclarationOptions,
  ): SseRequest<TInput, SseQueryOptions>;
}
