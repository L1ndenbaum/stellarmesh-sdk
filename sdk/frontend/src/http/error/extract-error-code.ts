import type { ErrorCodeExtractor } from './contracts.js';
import { HttpClientError, HttpErrorKind } from './http-client-error.js';

export function extractErrorCode(
  error: HttpClientError,
  extractor?: ErrorCodeExtractor,
): HttpClientError {
  if (
    !extractor ||
    error.status === undefined ||
    (error.kind !== HttpErrorKind.HTTP && error.kind !== HttpErrorKind.BUSINESS)
  ) {
    return error;
  }

  let apiCode: string | number | undefined;
  try {
    const value: unknown = extractor(
      error.data,
      Object.freeze({
        status: error.status,
        headers: Object.freeze({ ...error.headers }),
      }),
    );
    if (value == null) {
      apiCode = undefined;
    } else if (typeof value === 'string' || typeof value === 'number') {
      apiCode = value;
    } else {
      // JS 可绕过同步类型约束；观察误传 Promise 的拒绝，但不等待它。
      void Promise.resolve(value).catch(() => {});
      throw new TypeError('错误码提取器必须同步返回字符串、数字或空值');
    }
  } catch (cause) {
    throw new HttpClientError('错误码提取失败', {
      kind: HttpErrorKind.RESPONSE_FORMAT,
      status: error.status,
      data: error.data,
      headers: error.headers,
      cause,
    });
  }

  if (Object.is(apiCode, error.apiCode)) return error;
  // 配置只改变当前客户端交付的错误码，不修改转换器可能复用的错误对象。
  const result = new HttpClientError(error.message, {
    kind: error.kind,
    status: error.status,
    apiCode,
    data: error.data,
    headers: error.headers,
    cause: error.cause,
  });
  result.stack = error.stack;
  return result;
}
