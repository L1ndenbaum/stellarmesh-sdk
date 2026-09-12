import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  AuthRefreshResult,
  createAuthSession,
  flattenEnvelopeResponse,
  http,
  HttpClientError,
  HttpMethod,
} from '../src/index.js';
import type {
  HttpApiDeclarationOptions,
  HttpApiQueryMethod,
  HttpApiRequestDescriptor,
} from '../src/index.js';
const servers: Server[] = [];

async function serve(
  handler: (req: IncomingMessage, res: ServerResponse) => void | Promise<void>,
) {
  const server = createServer((req, res) => {
    void handler(req, res);
  });
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string')
    throw new Error('测试服务地址不可用');
  return `http://127.0.0.1:${address.port}`;
}

function json(res: ServerResponse, data: unknown, status = 200) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(data));
}

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(async () => {
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
describe('声明式 HTTP 接口', () => {
  it('无效的运行时映射描述不会意外请求 baseURL', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, true);
    });
    for (const value of [
      { method: 'GET' },
      { method: 'INVALID', url: '/' },
      Promise.resolve({ method: 'GET', url: '/' }),
    ]) {
      const endpoint = http
        .withBaseURL(origin)
        .request<void, void>(
          () => value as unknown as HttpApiRequestDescriptor,
        );
      await expect(endpoint()).rejects.toMatchObject({
        kind: 'unknown',
        cause: expect.any(TypeError),
      });
    }
    expect(calls).toBe(0);
  });
  it('metadata 与信封转换独立，派生不修改原入口或已声明函数', async () => {
    const origin = await serve((req, res) => {
      res.setHeader('ETag', '"part-1"');
      json(res, {
        code: 0,
        message: '',
        data: { marker: req.headers['x-marker'] ?? 'base' },
      });
    });
    const api = http
      .withBaseURL(origin)
      .withResponseTransform(flattenEnvelopeResponse());
    const original = api.get<void, { marker: string }>('/');
    const metadata = api
      .withMetadata()
      .withHeaders({ 'X-Marker': 'derived' })
      .withMetadata();
    const withInfo = metadata.get<void, { marker: string }>('/');
    expect(await original()).toEqual({ marker: 'base' });
    expect(await withInfo()).toMatchObject({
      data: { marker: 'derived' },
      status: 200,
      headers: { etag: '"part-1"' },
    });
    expect(await api.get<void, { marker: string }>('/')()).toEqual({
      marker: 'base',
    });
    const envelope = { code: 0, message: '', data: { marker: 'base' } };
    expect(await http.withBaseURL(origin).get<void, unknown>('/')()).toEqual(
      envelope,
    );
    expect(
      (await http.withBaseURL(origin).withMetadata().get<void, unknown>('/')())
        .data,
    ).toEqual(envelope);
  });

  it('通用声明隔离路径、查询和 DELETE 请求体，按层级合并 headers', async () => {
    let calls = 0;
    const origin = await serve(async (req, res) => {
      calls++;
      let body = '';
      for await (const chunk of req) body += String(chunk);
      json(res, {
        method: req.method,
        url: req.url,
        body,
        headers: req.headers,
      });
    });
    const resolve = vi.fn(
      (input: { id: string; page: number; body: { reason: string } }) => ({
        method: HttpMethod.DELETE,
        url: `/users/${encodeURIComponent(input.id)}`,
        params: { page: input.page },
        data: input.body,
        headers: { 'X-Value': 'mapped', 'X-Mapped': 'kept' },
      }),
    );
    const endpoint = http
      .withBaseURL(origin)
      .withHeaders({ 'X-Base': 'kept', 'X-Value': 'base' })
      .request<
        Parameters<typeof resolve>[0],
        {
          url: string;
          body: string;
          method: string;
          headers: Record<string, string>;
        }
      >(resolve, { headers: { 'X-Value': 'declared', 'X-Default': 'kept' } });
    expect(calls).toBe(0);
    expect(resolve).not.toHaveBeenCalled();
    const input = { id: 'a/b c', page: 2, body: { reason: 'duplicate' } };
    const result = await endpoint(input, { headers: { 'x-value': 'call' } });
    expect(result).toMatchObject({
      method: 'DELETE',
      url: '/users/a%2Fb%20c?page=2',
      body: '{"reason":"duplicate"}',
    });
    expect(result.headers).toMatchObject({
      'x-value': 'call',
      'x-base': 'kept',
      'x-default': 'kept',
      'x-mapped': 'kept',
    });
    expect((await endpoint(input)).headers['x-value']).toBe('mapped');
    expect(resolve).toHaveBeenCalledTimes(2);
  });

  it('动态请求在认证恢复和普通重试中只映射一次', async () => {
    let accessToken = 'old';
    const seen: string[] = [];
    const origin = await serve((req, res) => {
      seen.push(req.url ?? '');
      json(res, true, seen.length === 1 ? 401 : seen.length === 2 ? 503 : 200);
    });
    const refresh = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const resolve = vi.fn((id: number) => ({
      method: HttpMethod.GET,
      url: `/items/${id}`,
    }));
    const endpoint = http
      .withBaseURL(origin)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 })
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession: refresh,
        }),
      )
      .withMetadata()
      .request<number, boolean>(resolve);
    expect((await endpoint(7)).data).toBe(true);
    expect(seen).toEqual(['/items/7', '/items/7', '/items/7']);
    expect(resolve).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('取消及映射错误不会发送请求，错误保留 cause', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, true);
    });
    const cause = new Error('映射失败');
    const resolve = vi.fn(() => {
      throw cause;
    });
    const endpoint = http.withBaseURL(origin).request<void, boolean>(resolve);
    const controller = new AbortController();
    controller.abort();
    await expect(
      endpoint(undefined, { signal: controller.signal }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    expect(resolve).not.toHaveBeenCalled();
    await expect(endpoint()).rejects.toMatchObject({ kind: 'unknown', cause });
    expect(resolve).toHaveBeenCalledTimes(1);
    const failure = new HttpClientError('映射失败', { kind: 'business' });
    await expect(
      http.request<void, void>(() => {
        throw failure;
      })(),
    ).rejects.toBe(failure);
    expect(calls).toBe(0);
  });
  it.each(['get', 'head', 'delete', 'post', 'put', 'patch'] as const)(
    '%s 声明零发送，每次调用独立映射输入',
    async (method) => {
      const requests: { method?: string; url?: string; body: string }[] = [];
      const origin = await serve(async (req, res) => {
        let body = '';
        for await (const chunk of req) body += String(chunk);
        requests.push({ method: req.method, url: req.url, body });
        json(res, true);
      });
      const declare: HttpApiQueryMethod = http.withBaseURL(origin)[method];
      const endpoint = declare<{ id: number }, boolean>('/items');
      expect(requests).toHaveLength(0);
      expect(await endpoint({ id: 1 })).toBe(
        method === 'head' ? undefined : true,
      );
      await endpoint({ id: 2 });
      const query = ['get', 'head', 'delete'].includes(method);
      expect(requests).toEqual(
        [1, 2].map((id) => ({
          method: method.toUpperCase(),
          url: query ? `/items?id=${id}` : '/items',
          body: query ? '' : JSON.stringify({ id }),
        })),
      );
      await declare<void, boolean>('/empty')();
      expect(requests[2]).toEqual({
        method: method.toUpperCase(),
        url: '/empty',
        body: '',
      });
    },
  );

  it('声明配置快照、头覆盖和参数替换不污染后续调用', async () => {
    const origin = await serve((req, res) =>
      json(res, { url: req.url, headers: req.headers }),
    );
    type Echo = { url: string; headers: Record<string, string> };
    const headers = { 'X-Value': 'declared', 'X-Default': 'kept' };
    const params = { page: 1 };
    const defaults: HttpApiDeclarationOptions = {
      headers,
      params,
      timeout: 1000,
    };
    const endpoint = http
      .withBaseURL(origin)
      .post<void, Echo>('/items', defaults);
    defaults.timeout = 1;
    headers['X-Value'] = 'mutated';
    params.page = 99;
    const overridden = await endpoint(undefined, {
      headers: { 'x-VALUE': 'call', 'X-Call': 'once' },
      timeout: undefined,
      params: { limit: 2 },
    });
    expect(overridden.url).toBe('/items?limit=2');
    expect(overridden.headers).toMatchObject({
      'x-value': 'call',
      'x-default': 'kept',
      'x-call': 'once',
    });
    const next = await endpoint();
    expect(next.url).toBe('/items?page=1');
    expect(next.headers).toMatchObject({
      'x-value': 'declared',
      'x-default': 'kept',
    });
    expect(next.headers['x-call']).toBeUndefined();
  });

  it('URLSearchParams 保留重复查询键，声明保存独立容器', async () => {
    const origin = await serve((req, res) => json(res, req.url));
    const api = http.withBaseURL(origin);
    const params = new URLSearchParams('tag=a&tag=b');
    const body = api.post<void, string>('/', { params });
    params.set('tag', 'changed');
    expect(await body()).toBe('/?tag=a&tag=b');
    expect(
      await body(undefined, { params: new URLSearchParams('tag=call') }),
    ).toBe('/?tag=call');
    expect(await body()).toBe('/?tag=a&tag=b');
    expect(await api.get<URLSearchParams, string>('/')(params)).toBe(
      '/?tag=changed',
    );
  });

  it('真实传输继承客户端头与信封转换，查询和请求体分别发送', async () => {
    const received: unknown[] = [];
    const origin = await serve(async (req, res) => {
      let body = '';
      for await (const chunk of req) body += String(chunk);
      const data = {
        method: req.method,
        url: req.url,
        body,
        headers: req.headers,
      };
      received.push(data);
      json(res, { code: 0, message: '', data });
    });
    const api = http
      .withBaseURL(origin)
      .withHeaders({ 'X-Base': 'base', 'X-Value': 'base' })
      .withResponseTransform(flattenEnvelopeResponse());
    interface Query {
      page: number;
      keyword?: string;
    }
    type Echo = {
      method: string;
      url: string;
      body: string;
      headers: Record<string, string>;
    };
    const search = api.get<Query, Echo>('/users', {
      headers: { 'x-value': 'declared' },
    });
    const save = api.post<
      {
        name: string;
      },
      Echo
    >('/users', {
      params: { draft: 1 },
    });
    expect(received).toHaveLength(0);
    const result = await search(
      { page: 2, keyword: '张' },
      { headers: { 'X-VALUE': 'call' } },
    );
    const url = new URL(result.url, origin);
    expect(url.searchParams.get('page')).toBe('2');
    expect(url.searchParams.get('keyword')).toBe('张');
    expect(result.body).toBe('');
    expect(result.headers).toMatchObject({
      'x-base': 'base',
      'x-value': 'call',
    });
    expect((await search({ page: 1 })).headers['x-value']).toBe('declared');
    expect(await save({ name: '示例' })).toMatchObject({
      method: 'POST',
      url: '/users?draft=1',
      body: '{"name":"示例"}',
    });
  });
  it('调用时读取当前会话，匿名声明与认证恢复开关沿用原语义', async () => {
    const seen: (string | undefined)[] = [];
    const origin = await serve((req, res) => {
      seen.push(req.headers.authorization);
      json(res, true, req.headers.authorization === 'Bearer stale' ? 401 : 200);
    });
    let epoch = 1;
    let token = 'account-a';
    const getSessionEpoch = vi.fn(() => epoch);
    const getAuthHeaders = vi.fn(() => ({ Authorization: `Bearer ${token}` }));
    const refreshSession = vi.fn(async () => {
      token = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const api = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch,
        getAuthHeaders,
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
      }),
    );
    const workspace = api.get<void, boolean>('/workspace');
    const login = api.post<
      {
        username: string;
      },
      boolean
    >('/login', {
      auth: false,
    });
    const manual = api.post<void, boolean>('/manual', { authRecovery: false });
    expect(getSessionEpoch).not.toHaveBeenCalled();
    expect(getAuthHeaders).not.toHaveBeenCalled();
    epoch = 2;
    token = 'account-b';
    await workspace();
    await login({ username: 'b' });
    token = 'stale';
    await expect(manual()).rejects.toMatchObject({ status: 401 });
    expect(refreshSession).not.toHaveBeenCalled();
    await workspace();
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(seen).toEqual([
      'Bearer account-b',
      undefined,
      'Bearer stale',
      'Bearer stale',
      'Bearer fresh',
    ]);
  });
  it('调用、声明与客户端重试额度按优先级生效，每次调用独立计数', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, {}, 503);
    });
    const api = http
      .withBaseURL(origin)
      .withRetry({ maxRetries: 2, baseDelayMs: 0 });
    const inherited = api.get<void, void>('/inherited');
    const declared = api.get<void, void>('/declared', { maxRetries: 1 });
    await expect(inherited()).rejects.toMatchObject({ status: 503 });
    expect(calls).toBe(3);
    await expect(declared()).rejects.toMatchObject({ status: 503 });
    expect(calls).toBe(5);
    await expect(declared(undefined, { maxRetries: 0 })).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(6);
    await expect(declared()).rejects.toMatchObject({ status: 503 });
    expect(calls).toBe(8);
  });
  it('同一声明的并发调用各自取消，后续调用不继承已取消信号', async () => {
    const arrived = deferred();
    const release = deferred();
    let calls = 0;
    const origin = await serve(async (_req, res) => {
      calls++;
      if (calls === 2) arrived.resolve();
      await release.promise;
      json(res, true);
    });
    const endpoint = http.withBaseURL(origin).get<void, boolean>('/');
    const controller = new AbortController();
    const canceled = expect(
      endpoint(undefined, { signal: controller.signal }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    const other = endpoint();
    await arrived.promise;
    controller.abort();
    await canceled;
    release.resolve();
    expect(await other).toBe(true);
    expect(await endpoint()).toBe(true);
    expect(calls).toBe(3);
  });
  it('底层错误保持原对象，不增加错误包装', async () => {
    const failure = new HttpClientError('失败', {
      kind: 'business',
      apiCode: 'DENIED',
    });
    const origin = await serve((_req, res) => json(res, true));
    const endpoint = http
      .withBaseURL(origin)
      .withResponseTransform(() => {
        throw failure;
      })
      .get<void, void>('/');
    await expect(endpoint()).rejects.toBe(failure);
  });
});
