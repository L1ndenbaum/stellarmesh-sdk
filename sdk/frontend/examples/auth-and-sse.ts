import {
  AuthRefreshResult,
  createAuthSession,
  http,
  isHttpClientError,
} from '@stellarmesh/sdk';

/** 演示服务需提供 POST /refresh 返回 token，以及 GET /events 返回 SSE。 */
export async function readEvents(baseURL: string): Promise<string[]> {
  const publicApi = http.withBaseURL(baseURL).withTimeout(5_000);
  const requestRefresh = publicApi.post<void, { token: string }>('/refresh');
  // 示例内存会话；实际项目应在登录、退出、切换账号时更换 epoch。
  const session = { epoch: 1, token: 'expired-example-token' };
  const auth = createAuthSession({
    getSessionEpoch: () => session.epoch,
    getAuthHeaders: () => ({ Authorization: `Bearer ${session.token}` }),
    shouldRefresh: (error) => error.status === 401,
    refreshSession: async ({ epoch }) => {
      const result = await requestRefresh();
      if (epoch !== session.epoch) throw new Error('会话已变化');
      session.token = result.token;
      return AuthRefreshResult.REFRESHED;
    },
  });
  const requestEvents = publicApi.withAuth(auth).sse.get<void>('/events');
  const controller = new AbortController();
  const result: string[] = [];
  try {
    for await (const message of requestEvents(undefined, {
      signal: controller.signal,
    })) {
      // 本示例服务使用 completed 事件；SDK 不赋予它终态含义。
      if (message.event === 'completed') return result;
      result.push(message.data);
    }
    throw new Error('生成中断：未收到业务终态');
  } catch (error) {
    if (isHttpClientError(error)) console.error(error.kind, error.status);
    throw error;
  } finally {
    controller.abort();
  }
}
