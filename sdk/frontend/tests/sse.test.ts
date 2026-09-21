import { createServer } from 'node:http';
import type { Server, ServerResponse } from 'node:http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  AuthRefreshResult,
  createAuthSession,
  http,
  HttpMethod,
} from '../src/index.js';
import type {
  AuthSessionOptions,
  SseMessage,
  SseRequestDescriptor,
} from '../src/index.js';

const encoder = new TextEncoder();
const servers: Server[] = [];

function stream(text = 'data: ok\n\n') {
  return new Response(text, {
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
  });
}

async function collect(messages: AsyncIterable<SseMessage>) {
  const result: SseMessage[] = [];
  for await (const message of messages) result.push(message);
  return result;
}

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await Promise.all(
    servers.splice(0).map(
      (server) =>
        new Promise<void>((resolve) => {
          server.closeAllConnections();
          server.close(() => resolve());
        }),
    ),
  );
});

describe('SSE 协议与声明', () => {
  it('增量解码任意字节切分、BOM、三种换行、多行数据和字段状态', async () => {
    const bytes = encoder.encode(
      '\uFEFF: 心跳\r\nid: 7\rretry: 012\r\nevent: update\ndata:  你  \r\ndata: 好\r\n\r\n' +
        'id: bad\0id\nretry: -1\nretry: 9007199254740992\ndata\n\n' +
        'id\r\ndata: next\n\n: 空注释\n\nevent: ignored\n\ndata: tail\n\n' +
        'data: incomplete\n',
    );
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            new ReadableStream({
              start(controller) {
                for (const byte of bytes)
                  controller.enqueue(new Uint8Array([byte]));
                controller.close();
              },
            }),
            { headers: { 'Content-Type': 'text/event-stream' } },
          ),
      ),
    );
    expect(await collect(http.sse.get<void>('/events')())).toEqual([
      { data: ' 你  \n好', event: 'update', id: '7', retry: 12 },
      { data: '', event: 'message', id: '7', retry: 12 },
      { data: 'next', event: 'message', id: '', retry: 12 },
      { data: 'tail', event: 'message', id: '', retry: 12 },
    ]);
  });
  it('声明和取得 iterable 均不发送，消费单次，void 无需输入', async () => {
    const fetcher = vi.fn<typeof fetch>(async () => stream());
    vi.stubGlobal('fetch', fetcher);
    const request = http.sse.get<void>('/events');
    const messages = request();
    expect(fetcher).not.toHaveBeenCalled();
    expect(await collect(messages)).toHaveLength(1);
    expect(await collect(messages)).toEqual([]);
    await collect(request());
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it('URL 保留 baseURL 路径，查询编码和请求头优先级与 HTTP 一致', async () => {
    const fetcher = vi.fn<typeof fetch>(async () => stream());
    vi.stubGlobal('fetch', fetcher);
    const headers = { X: 'declaration' };
    const api = http
      .withBaseURL('https://api.test/v1')
      .withHeaders({ X: 'client', Z: 'client' });
    const get = api.sse.get<{ name: string }>('/events', { headers });
    headers.X = 'changed';
    await collect(get({ name: '张 三' }, { headers: { x: 'call' } }));
    expect(fetcher.mock.calls[0]).toMatchObject([
      'https://api.test/v1/events?name=%E5%BC%A0+%E4%B8%89',
      {
        method: 'GET',
        headers: { x: 'call', z: 'client', accept: 'text/event-stream' },
        redirect: 'error',
        credentials: 'same-origin',
      },
    ]);
    await collect(
      api.sse.post<{ id: number }>('/events', { params: { a: 1 } })(
        { id: 2 },
        { params: new URLSearchParams('a=3') },
      ),
    );
    expect(fetcher.mock.calls[1]).toMatchObject([
      'https://api.test/v1/events?a=3',
      {
        method: 'POST',
        body: '{"id":2}',
        headers: { 'content-type': 'application/json' },
      },
    ]);
  });
  it('不执行响应转换、metadata 包装或继承的网络重试', async () => {
    const transform = vi.fn(() => {
      throw new Error('不应调用');
    });
    const fetcher = vi.fn<typeof fetch>(async () => stream('data: [DONE]\n\n'));
    vi.stubGlobal('fetch', fetcher);
    const api = http
      .withMetadata()
      .withResponseTransform(transform)
      .withMaxRetries(3);
    expect((await collect(api.sse.post<void>('/')()))[0]?.data).toBe('[DONE]');
    fetcher.mockRejectedValueOnce(new TypeError('offline'));
    await expect(collect(api.sse.get<void>('/')())).rejects.toMatchObject({
      kind: 'network',
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(transform).not.toHaveBeenCalled();
  });
  it.each([
    { method: 'PUT', url: '/' },
    { method: 'GET', url: '/', data: {} },
    {},
    Promise.resolve({ method: 'GET', url: '/' }),
  ])('运行时拒绝非法动态请求 %j', async (descriptor) => {
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    await expect(
      collect(
        http.sse.request<void>(() => descriptor as SseRequestDescriptor)(),
      ),
    ).rejects.toMatchObject({ kind: 'unknown' });
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe('SSE 生命周期与失败', () => {
  it.each(['break', 'throw'])(
    '消费方 %s 释放 reader 和连接',
    async (action) => {
      const cancel = vi.fn();
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          c.enqueue(encoder.encode('data: one\n\ndata: two\n\n'));
        },
        cancel,
      });
      const fetcher = vi.fn<typeof fetch>(
        async () =>
          new Response(body, {
            headers: { 'Content-Type': 'text/event-stream' },
          }),
      );
      vi.stubGlobal('fetch', fetcher);
      const received: string[] = [];
      try {
        for await (const message of http.sse.get<void>('/')()) {
          received.push(message.data);
          if (action === 'throw') throw new Error('consumer');
          break;
        }
      } catch (error) {
        expect((error as Error).message).toBe('consumer');
      }
      expect(received).toEqual(['one']);
      expect(cancel).toHaveBeenCalledTimes(1);
      expect(body.locked).toBe(false);
      expect(fetcher.mock.calls[0]?.[1]?.signal?.aborted).toBe(true);
    },
  );
  it('取消等待中的 reader，并释放资源', async () => {
    const entered = deferred();
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({
      pull() {
        entered.resolve();
      },
      cancel,
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(body, {
            headers: { 'Content-Type': 'text/event-stream' },
          }),
      ),
    );
    const controller = new AbortController();
    const result = collect(
      http.sse.get<void>('/')(undefined, { signal: controller.signal }),
    );
    await entered.promise;
    controller.abort();
    await expect(result).rejects.toMatchObject({ kind: 'canceled' });
    expect(cancel).toHaveBeenCalled();
    expect(body.locked).toBe(false);
  });
  it('调用前取消不执行映射或发送请求', async () => {
    const resolve = vi.fn(() => ({ method: HttpMethod.GET, url: '/' }));
    const controller = new AbortController();
    controller.abort();
    await expect(
      collect(
        http.sse.request<void>(resolve)(undefined, {
          signal: controller.signal,
        }),
      ),
    ).rejects.toMatchObject({ kind: 'canceled' });
    expect(resolve).not.toHaveBeenCalled();
  });
  it('建连超时归类 TIMEOUT，不重试', async () => {
    const fetcher = vi.fn(
      (_url: string, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () =>
            reject(new Error('aborted')),
          );
        }),
    );
    vi.stubGlobal('fetch', fetcher);
    await expect(
      collect(http.withTimeout(10).withMaxRetries(3).sse.get<void>('/')()),
    ).rejects.toMatchObject({ kind: 'timeout' });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it('收到响应头后不再限制长流的时间', async () => {
    let source!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        source = c;
        c.enqueue(encoder.encode('data: one\n\n'));
      },
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(body, {
            headers: { 'Content-Type': 'text/event-stream' },
          }),
      ),
    );
    const iterator = http
      .withTimeout(5)
      .sse.get<void>('/')()
      [Symbol.asyncIterator]();
    expect((await iterator.next()).value?.data).toBe('one');
    await new Promise((resolve) => setTimeout(resolve, 20));
    source.enqueue(encoder.encode('data: two\n\n'));
    source.close();
    expect((await iterator.next()).value?.data).toBe('two');
    expect((await iterator.next()).done).toBe(true);
  });
  it.each([
    new Response('json'),
    new Response(null, { status: 204 }),
    new Response('data: x\n\n', {
      status: 201,
      headers: { 'Content-Type': 'text/event-stream' },
    }),
  ])('非 SSE 成功响应拒绝且不恢复', async (response) => {
    const predicate = vi.fn(() => true);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response),
    );
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        shouldRefresh: predicate,
        refreshSession: async () => AuthRefreshResult.REFRESHED,
      }),
    );
    await expect(collect(api.sse.get<void>('/')())).rejects.toMatchObject({
      kind: 'response-format',
    });
    expect(predicate).not.toHaveBeenCalled();
  });
  it('JSON 与文本 HTTP 错误保留状态、响应头和错误体', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(
          new Response(
            JSON.stringify({ message: '拒绝', error_code: 'DENIED' }),
            { status: 403, headers: { 'X-Reason': 'test' } },
          ),
        )
        .mockResolvedValueOnce(
          new Response('<html>unavailable</html>', { status: 503 }),
        ),
    );
    const api = http
      .withMaxRetries(3)
      .withErrorCodeExtractor(
        (data) => (data as { error_code?: string }).error_code,
      );
    await expect(collect(api.sse.get<void>('/')())).rejects.toMatchObject({
      kind: 'http',
      status: 403,
      message: '拒绝',
      apiCode: 'DENIED',
      headers: { 'x-reason': 'test' },
    });
    await expect(collect(api.sse.get<void>('/')())).rejects.toMatchObject({
      kind: 'http',
      status: 503,
      data: '<html>unavailable</html>',
    });
  });
  it('读取中断不刷新、不重发，检查每次交付事件的 epoch', async () => {
    let epoch = 1;
    let source!: ReadableStreamDefaultController<Uint8Array>;
    const fetcher = vi.fn<typeof fetch>(
      async () =>
        new Response(
          new ReadableStream<Uint8Array>({
            start(c) {
              source = c;
              c.enqueue(encoder.encode('data: one\n\ndata: two\n\n'));
            },
          }),
          { headers: { 'Content-Type': 'text/event-stream' } },
        ),
    );
    vi.stubGlobal('fetch', fetcher);
    const predicate = vi.fn(() => true);
    const api = http
      .withBaseURL('https://api.test')
      .withMaxRetries(5)
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => epoch,
          shouldRefresh: predicate,
          refreshSession: async () => AuthRefreshResult.REFRESHED,
        }),
      );
    const first = api.sse.get<void>('/')()[Symbol.asyncIterator]();
    await first.next();
    epoch++;
    await expect(first.next()).rejects.toMatchObject({
      kind: 'session-changed',
    });
    const second = api.sse.get<void>('/')()[Symbol.asyncIterator]();
    await second.next();
    await second.next();
    source.error(new Error('broken'));
    await expect(second.next()).rejects.toMatchObject({ kind: 'network' });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(predicate).not.toHaveBeenCalled();
  });
});

describe('SSE 会话恢复', () => {
  it('谓词返回 false 前切换账号仍拒绝旧响应', async () => {
    let epoch = 1;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{}', { status: 401 })),
    );
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => epoch,
        shouldRefresh: () => {
          epoch++;
          return false;
        },
        refreshSession: async () => AuthRefreshResult.REFRESHED,
      }),
    );
    await expect(collect(api.sse.get<void>('/')())).rejects.toMatchObject({
      kind: 'session-changed',
    });
  });
  it('并发失效只通知一次，退出回调允许推进 epoch', async () => {
    let epoch = 1;
    const responses = [deferred<Response>(), deferred<Response>()];
    const fetcher = vi
      .fn()
      .mockImplementationOnce(() => responses[0]!.promise)
      .mockImplementationOnce(() => responses[1]!.promise);
    vi.stubGlobal('fetch', fetcher);
    const onUnauthorized = vi.fn(() => {
      epoch++;
    });
    const refreshSession = vi.fn(async () => AuthRefreshResult.EXPIRED);
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => epoch,
        shouldRefresh: () => true,
        refreshSession,
        onUnauthorized,
      }),
    );
    const results = Promise.allSettled([
      collect(api.sse.get<void>('/')()),
      collect(api.sse.get<void>('/')()),
    ]);
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    for (const response of responses)
      response.resolve(new Response('{}', { status: 401 }));
    const settled = await results;
    expect(settled.every((result) => result.status === 'rejected')).toBe(true);
    expect(
      settled.some(
        (result) =>
          result.status === 'rejected' && result.reason.status === 401,
      ),
    ).toBe(true);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('动态映射只执行一次，恢复后读取新认证头并覆盖调用头', async () => {
    let token = 'old';
    const resolve = vi.fn((id: number) => ({
      method: HttpMethod.POST,
      url: `/events/${id}`,
      data: { id },
    }));
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(new Response('{}', { status: 401 }))
      .mockResolvedValueOnce(stream());
    vi.stubGlobal('fetch', fetcher);
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: token }),
        shouldRefresh: (error) => error.status === 401,
        refreshSession: async () => {
          token = 'fresh';
          return AuthRefreshResult.REFRESHED;
        },
      }),
    );
    await collect(
      api.sse.request(resolve)(7, { headers: { authorization: 'manual' } }),
    );
    expect(resolve).toHaveBeenCalledTimes(1);
    expect(
      fetcher.mock.calls.map(([, init]) => [
        init.headers.authorization,
        init.body,
      ]),
    ).toEqual([
      ['old', '{"id":7}'],
      ['fresh', '{"id":7}'],
    ]);
  });
  it('不配置刷新时 401 直接失败', async () => {
    const fetcher = vi.fn<typeof fetch>(
      async () => new Response('{}', { status: 401 }),
    );
    vi.stubGlobal('fetch', fetcher);
    await expect(
      collect(
        http
          .withBaseURL('https://api.test')
          .withAuth(createAuthSession({ getSessionEpoch: () => 1 }))
          .sse.get<void>('/')(),
      ),
    ).rejects.toMatchObject({ status: 401 });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it.each([
    'expired',
    'twice',
    'throws',
    'bad-result',
    'bad-predicate',
  ] as const)('%s 保留一次恢复上限及失败关闭', async (scenario) => {
    const fetcher = vi.fn<typeof fetch>(
      async () => new Response('{}', { status: 401 }),
    );
    vi.stubGlobal('fetch', fetcher);
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => {
      if (scenario === 'throws') throw new Error('refresh down');
      return (
        scenario === 'expired'
          ? AuthRefreshResult.EXPIRED
          : scenario === 'bad-result'
            ? 'token'
            : AuthRefreshResult.REFRESHED
      ) as AuthRefreshResult;
    });
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        shouldRefresh: () =>
          (scenario === 'bad-predicate' ? 'truthy' : true) as boolean,
        refreshSession,
        onUnauthorized,
      }),
    );
    await expect(collect(api.sse.post<void>('/')())).rejects.toMatchObject({
      kind: ['expired', 'twice'].includes(scenario) ? 'http' : 'auth',
    });
    expect(fetcher).toHaveBeenCalledTimes(scenario === 'twice' ? 2 : 1);
    expect(refreshSession).toHaveBeenCalledTimes(
      scenario === 'bad-predicate' ? 0 : 1,
    );
    expect(onUnauthorized).toHaveBeenCalledTimes(
      ['expired', 'twice'].includes(scenario) ? 1 : 0,
    );
  });
  it.each(['anonymous', 'recovery-off', 'foreign'] as const)(
    '%s 保留认证开关与可信来源语义',
    async (scenario) => {
      const getAuthHeaders = vi.fn(() => ({ Authorization: 'session' }));
      const shouldRefresh = vi.fn(() => true);
      const fetcher = vi.fn<typeof fetch>(
        async () => new Response('{}', { status: 401 }),
      );
      vi.stubGlobal('fetch', fetcher);
      const api = http.withBaseURL('https://api.test').withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders,
          shouldRefresh,
          refreshSession: async () => AuthRefreshResult.REFRESHED,
        }),
      );
      await expect(
        collect(
          api.sse.get<void>(
            scenario === 'foreign' ? 'https://foreign.test/' : '/',
            {
              auth: scenario === 'anonymous' ? false : undefined,
              authRecovery: scenario === 'recovery-off' ? false : undefined,
              headers: { Authorization: 'manual' },
            },
          )(),
        ),
      ).rejects.toMatchObject({ status: 401 });
      expect(fetcher.mock.calls[0]).toMatchObject([
        expect.any(String),
        {
          headers: {
            authorization: scenario === 'recovery-off' ? 'session' : 'manual',
          },
        },
      ]);
      expect(getAuthHeaders).toHaveBeenCalledTimes(
        scenario === 'recovery-off' ? 1 : 0,
      );
      expect(shouldRefresh).not.toHaveBeenCalled();
    },
  );
  it('Node 不宣称支持 Cookie 容器', async () => {
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    await expect(
      collect(
        http
          .withBaseURL('https://api.test')
          .withAuth(createAuthSession({ getSessionEpoch: () => 1 }), {
            withCredentials: true,
          })
          .sse.get<void>('/')(),
      ),
    ).rejects.toMatchObject({ kind: 'auth' });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('共享刷新期间取消单个 SSE 等待者不会取消其他请求', async () => {
    const refresh = deferred<AuthRefreshResult>();
    const started = deferred();
    let token = 'old';
    const refreshSession = vi.fn(() => {
      started.resolve();
      return refresh.promise;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        token === 'old' ? new Response('{}', { status: 401 }) : stream(),
      ),
    );
    const api = http.withBaseURL('https://api.test').withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        shouldRefresh: () => true,
        refreshSession,
      }),
    );
    const controller = new AbortController();
    const canceled = collect(
      api.sse.get<void>('/')(undefined, { signal: controller.signal }),
    );
    const surviving = collect(api.sse.get<void>('/')());
    await started.promise;
    controller.abort();
    await expect(canceled).rejects.toMatchObject({ kind: 'canceled' });
    token = 'fresh';
    refresh.resolve(AuthRefreshResult.REFRESHED);
    expect(await surviving).toHaveLength(1);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('普通 HTTP 与 SSE 在真实请求中共享同一个刷新代次', async () => {
    let token = 'old';
    const pending: ServerResponse[] = [];
    const server = createServer((req, res) => {
      if (req.headers.authorization === 'old') {
        pending.push(res);
        if (pending.length === 2)
          for (const response of pending) {
            response.writeHead(401);
            response.end('{}');
          }
      } else if (req.url === '/stream') {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.end('data: fresh\n\n');
      } else {
        res.end('{"ok":true}');
      }
    });
    servers.push(server);
    await new Promise<void>((resolve) =>
      server.listen(0, '127.0.0.1', resolve),
    );
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('address');
    const refreshSession = vi.fn(async () => {
      token = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const api = http.withBaseURL(`http://127.0.0.1:${address.port}`).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: token }),
        shouldRefresh: (error) => error.status === 401,
        refreshSession,
      }),
    );
    const [plain, messages] = await Promise.all([
      api.get<void, { ok: boolean }>('/plain')(),
      collect(api.sse.post<void>('/stream')()),
    ]);
    expect(plain.ok).toBe(true);
    expect(messages[0]?.data).toBe('fresh');
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('刷新期间切换 epoch 不重放也不退出新会话', async () => {
    let epoch = 1;
    const onUnauthorized = vi.fn();
    const fetcher = vi.fn<typeof fetch>(
      async () => new Response('{}', { status: 401 }),
    );
    vi.stubGlobal('fetch', fetcher);
    const options: AuthSessionOptions = {
      getSessionEpoch: () => epoch,
      shouldRefresh: () => true,
      refreshSession: async () => {
        epoch++;
        return AuthRefreshResult.REFRESHED;
      },
      onUnauthorized,
    };
    await expect(
      collect(
        http
          .withBaseURL('https://api.test')
          .withAuth(createAuthSession(options))
          .sse.get<void>('/')(),
      ),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
