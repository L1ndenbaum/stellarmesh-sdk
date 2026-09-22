import { HttpClientError } from '../error/http-client-error';
import type { ResponseTransform } from './contracts';

/** 可配置工具类型，不代表所有业务项目必须采用的跨语言协议。 */
export interface ApiEnvelope<T> {
  code: number | string;
  message: string;
  data?: T | null;
}

/** 业务信封工具配置，不预设项目错误码目录。 */
export interface EnvelopeOptions {
  /** 默认接受数值 0 或数值 2xx；字符串成功码需业务显式判断。 */
  isSuccess?(code: number | string): boolean;
  /** 默认 false，非信封响应报 RESPONSE_FORMAT；true 时原样返回。 */
  allowNonEnvelope?: boolean;
}

/**
 * 将成功信封解包为 data；业务失败抛 BUSINESS，204 返回 undefined。
 * @remarks 装配到 withResponseTransform，不作用于成功 SSE；不会验证 data 的 DTO 字段。
 */
export function flattenEnvelopeResponse(
  options: EnvelopeOptions = {},
): ResponseTransform {
  const {
    allowNonEnvelope = false,
    isSuccess = (code) =>
      code === 0 || (typeof code === 'number' && code >= 200 && code < 300),
  } = options;
  return (data, context) => {
    if (context.status === 204) return undefined;
    const value =
      data !== null && typeof data === 'object'
        ? (data as Record<string, unknown>)
        : undefined;
    if (
      !value ||
      (typeof value.code !== 'number' && typeof value.code !== 'string') ||
      typeof value.message !== 'string'
    ) {
      if (allowNonEnvelope) return data;
      throw new HttpClientError('响应不符合信封格式', {
        kind: 'response-format',
        ...context,
        data,
      });
    }
    if (!isSuccess(value.code)) {
      throw new HttpClientError(value.message || '业务请求失败', {
        kind: 'business',
        ...context,
        apiCode: value.code,
        data,
      });
    }
    return value.data;
  };
}
