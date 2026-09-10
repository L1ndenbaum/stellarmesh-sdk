export const ResponseType = {
  JSON: 'json',
  TEXT: 'text',
  BLOB: 'blob',
  ARRAYBUFFER: 'arraybuffer',
} as const;

export type ResponseType = (typeof ResponseType)[keyof typeof ResponseType];

export interface HttpResponse<T> {
  data: T;
  status: number;
  headers: Record<string, string>;
}

export interface ResponseContext {
  status: number;
  headers: Readonly<Record<string, string>>;
}

/** 支持同步或异步转换；metadata 与普通请求均等待最终数据。 */
export type ResponseTransform = (
  data: unknown,
  context: ResponseContext,
) => unknown;
