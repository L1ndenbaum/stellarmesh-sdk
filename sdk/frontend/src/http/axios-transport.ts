import axios, { AxiosHeaders } from 'axios';
import type { AxiosInstance } from 'axios';
import { HttpClientError } from './errors.js';
import type { HttpRequest, HttpResponse } from './types.js';

export function normalizeHeaders(headers: object): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(headers)) {
    if (value != null) result[key.toLowerCase()] = String(value);
  }
  return result;
}
export function mergeHeaders(
  ...headers: (Readonly<Record<string, string>> | undefined)[]
): Record<string, string> {
  const result = new AxiosHeaders();
  for (const value of headers) if (value) result.set(value);
  return normalizeHeaders(result.toJSON());
}
export function normalizeError(error: unknown): HttpClientError {
  if (error instanceof HttpClientError) return error;
  if (!axios.isAxiosError(error)) {
    return new HttpClientError(
      error instanceof Error ? error.message : '请求失败',
      { kind: 'unknown', cause: error },
    );
  }
  let data: unknown = error.response?.data;
  if (typeof data === 'string' && data.length > 0) {
    try {
      data = JSON.parse(data) as unknown;
    } catch {
      /* 非 JSON 错误体仍保留 HTTP 状态和原始内容。 */
    }
  }
  const object =
    data && typeof data === 'object'
      ? (data as Record<string, unknown>)
      : undefined;
  const message =
    typeof object?.message === 'string' ? object.message : error.message;
  return new HttpClientError(message || '请求失败', {
    kind: axios.isCancel(error)
      ? 'canceled'
      : error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT'
        ? 'timeout'
        : error.response
          ? 'http'
          : error.request
            ? 'network'
            : 'unknown',
    status: error.response?.status,
    apiCode:
      typeof object?.code === 'string' || typeof object?.code === 'number'
        ? object.code
        : undefined,
    data,
    headers: error.response
      ? normalizeHeaders(error.response.headers)
      : undefined,
    cause: error,
  });
}
export function createTransport(baseURL?: string): AxiosInstance {
  return axios.create({ baseURL, timeout: 0 });
}
export async function send(
  instance: AxiosInstance,
  request: HttpRequest,
  headers: Record<string, string>,
): Promise<HttpResponse<unknown>> {
  const parseJson =
    request.responseType === undefined || request.responseType === 'json';
  try {
    const response = await instance.request<unknown>({
      method: request.method,
      url: request.url,
      data: request.data,
      params: request.params,
      headers,
      timeout: request.timeout,
      signal: request.signal,
      responseType: parseJson ? 'text' : request.responseType,
      // 先保留 HTTP 失败语义，再解析成功响应，避免 HTML 503 掩盖重试状态。
      transformResponse: [(data: unknown) => data],
      onUploadProgress: request.onUploadProgress
        ? (event) =>
            request.onUploadProgress?.({
              loaded: event.loaded,
              total: event.total,
            })
        : undefined,
      onDownloadProgress: request.onDownloadProgress
        ? (event) =>
            request.onDownloadProgress?.({
              loaded: event.loaded,
              total: event.total,
            })
        : undefined,
    });
    let data = response.data;
    if (
      parseJson &&
      typeof data === 'string' &&
      data.length > 0 &&
      request.method !== 'HEAD' &&
      response.status !== 204
    ) {
      try {
        data = JSON.parse(data) as unknown;
      } catch (cause) {
        throw new HttpClientError('响应不是有效的 JSON', {
          kind: 'response-format',
          status: response.status,
          data,
          headers: normalizeHeaders(response.headers),
          cause,
        });
      }
    }
    return {
      data,
      status: response.status,
      headers: normalizeHeaders(response.headers),
    };
  } catch (error) {
    throw normalizeError(error);
  }
}
