import type { ResponseType } from '../response/contracts';

/** 声明支持的方法；查询方法与请求体方法使用不同的输入映射。 */
export const HttpMethod = {
  GET: 'GET',
  HEAD: 'HEAD',
  POST: 'POST',
  PUT: 'PUT',
  PATCH: 'PATCH',
  DELETE: 'DELETE',
} as const;

export type HttpMethod = (typeof HttpMethod)[keyof typeof HttpMethod];

/** 字符串请求头映射；合并时名称大小写不敏感。 */
export type HttpHeaders = Readonly<Record<string, string>>;

/** 字节传输进度；未知总长度时 total 缺省，不可假定能计算百分比。 */
export interface HttpProgress {
  loaded: number;
  total?: number;
}

/** 单次请求配置；undefined 沿用上层配置，false 和 0 为显式覆盖。 */
export interface HttpRequestOptions {
  /** 请求体方法的附加查询；整个容器替换上层 params，不逐字段合并。 */
  params?: Readonly<Record<string, unknown>> | URLSearchParams;
  /** 覆盖同名默认头；自动认证回调提供的头具有更高优先级。 */
  headers?: HttpHeaders;
  /** 每次 HTTP 传输超时，毫秒；默认 0，不包含重试等待。 */
  timeout?: number;
  /** 普通传输额外重试上限，默认 0；刷新不会重置已使用额度。 */
  maxRetries?: number;
  /** 写请求由调用方确认幂等性和请求体可重放后开启。 */
  retryable?: boolean;
  /** 本次调用的取消信号；取消等待不会终止其他调用共享的刷新。 */
  signal?: AbortSignal;
  /** false 关闭 SDK 认证行为；不清除手动头，也不禁止浏览器同源 Cookie。 */
  auth?: boolean;
  /** false 仅关闭认证恢复重放和未授权通知，仍正常注入凭证。 */
  authRecovery?: boolean;
  /** raw 跳过成功响应转换，仍执行 HTTP 错误码提取；默认 transformed。 */
  responseMode?: 'transformed' | 'raw';
  /** 响应读取类型，默认 JSON；不用于 SSE。 */
  responseType?: ResponseType;
  /** 上传进度由 Axios 适配器提供，不表示服务端已经完成业务操作。 */
  onUploadProgress?(progress: HttpProgress): void;
  /** 下载字节进度，total 可能未知。 */
  onDownloadProgress?(progress: HttpProgress): void;
}

/** HTTP 请求描述；公开类型不提供独立的立即执行入口。 */
export interface HttpRequest<TBody = unknown> extends HttpRequestOptions {
  method: HttpMethod;
  url: string;
  data?: TBody;
}
