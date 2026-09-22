import { http, isHttpClientError } from '@stellarmesh/sdk';

/** 服务需提供 GET /items，响应为 { items: string[] }。 */
export async function listItems(baseURL: string): Promise<string[]> {
  const requestItems = http
    .withBaseURL(baseURL)
    .withTimeout(5_000)
    .get<void, { items: string[] }>('/items');
  const controller = new AbortController();
  try {
    return (await requestItems(undefined, { signal: controller.signal })).items;
  } catch (error) {
    if (isHttpClientError(error)) console.error(error.kind, error.status);
    throw error;
  } finally {
    controller.abort();
  }
}
