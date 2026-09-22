/** 普通 HTTP 的响应读取类型；成功 SSE 始终交付原始字符串消息。 */
export const ResponseType = {
  JSON: 'json',
  TEXT: 'text',
  BLOB: 'blob',
  ARRAYBUFFER: 'arraybuffer',
} as const;

export type ResponseType = (typeof ResponseType)[keyof typeof ResponseType];

/** metadata 响应；data 仍经过配置的成功响应转换。 */
export interface HttpResponse<T> {
  data: T;
  status: number;
  headers: Record<string, string>;
}

/** 响应转换与错误码提取所见的真实 HTTP 状态和响应头。 */
export interface ResponseContext {
  status: number;
  headers: Readonly<Record<string, string>>;
}

/** 支持同步或异步转换；metadata 与普通请求均等待最终数据。 */
export type ResponseTransform = (
  data: unknown,
  context: ResponseContext,
) => unknown;
