import { HttpClientError } from '../error/http-client-error.js';
import type { ResponseTransform } from './contracts.js';

/** 可配置工具类型，不代表所有业务项目必须采用的跨语言协议。 */
export interface ApiEnvelope<T> {
  code: number | string;
  message: string;
  data?: T | null;
}

export interface EnvelopeOptions {
  isSuccess?(code: number | string): boolean;
  allowNonEnvelope?: boolean;
}

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
