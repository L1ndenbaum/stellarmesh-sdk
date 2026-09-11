import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createAuthSession,
  createHttpApi,
  flattenEnvelopeResponse,
  httpClient,
  HttpClientError,
} from '../src/index.js';
import type {
  HttpApiDeclarationOptions,
  HttpApiQueryMethod,
  HttpRequest,
} from '../src/index.js';

function recorder() {
  const requests: HttpRequest[] = [];
  const request = vi.fn(
    async <TInput, TResponse>(
      input: HttpRequest<TInput>,
    ): Promise<TResponse> => {
      requests.push(input);
      return true as TResponse;
    },
  );
  return { requests, request, api: createHttpApi({ request }) };
}

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
  it.each(['get', 'head', 'delete', 'post', 'put', 'patch'] as const)(
    '%s 声明零发送，每次调用独立映射输入',
    async (method) => {
      const { api, requests, request } = recorder();
      const declare: HttpApiQueryMethod = api[method];
      const endpoint = declare<{ id: number }, boolean>('/items');
      expect(request).not.toHaveBeenCalled();
      expect(await endpoint({ id: 1 })).toBe(true);
      expect(await endpoint({ id: 2 })).toBe(true);
      expect(requests).toHaveLength(2);
      const query = ['get', 'head', 'delete'].includes(method);
      requests.forEach((sent, index) => {
        expect(sent.method).toBe(method.toUpperCase());
        expect(sent.url).toBe('/items');
        expect(query ? sent.params : sent.data).toEqual({ id: index + 1 });
        expect(query ? sent.data : sent.params).toBeUndefined();
      });
      const empty = declare<void, boolean>('/empty');
      await empty();
      expect(requests[2]).toMatchObject({
        method: method.toUpperCase(),
        url: '/empty',
      });
      expect(requests[2]?.data).toBeUndefined();
      expect(requests[2]?.params).toBeUndefined();
    },
  );

  it('声明配置快照、大小写不敏感头合并和显式覆盖不污染后续调用', async () => {
    const { api, requests } = recorder();
    const headers = { 'X-Value': 'declared', 'X-Default': 'kept' };
    const params = { page: 1 };
    const defaults: HttpApiDeclarationOptions = {
      headers,
      params,
      timeout: 1000,
      maxRetries: 2,
      retryable: true,
      auth: false,
    };
    const endpoint = api.post<void, boolean>('/items', defaults);
    defaults.timeout = 9000;
    headers['X-Value'] = 'mutated';
    params.page = 99;
    const controller = new AbortController();
    await endpoint(undefined, {
      headers: { 'x-VALUE': 'call', 'X-Call': 'once' },
      timeout: undefined,
      maxRetries: 0,
      retryable: false,
      signal: controller.signal,
      params: { limit: 2 },
    });
    await endpoint();
    expect(requests[0]).toMatchObject({
      headers: { 'x-value': 'call', 'x-default': 'kept', 'x-call': 'once' },
      timeout: 1000,
      maxRetries: 0,
      retryable: false,
      auth: false,
      signal: controller.signal,
      params: { limit: 2 },
    });
    expect(requests[1]).toMatchObject({
      headers: { 'x-value': 'declared', 'x-default': 'kept' },
      timeout: 1000,
      maxRetries: 2,
      retryable: true,
      auth: false,
      params: { page: 1 },
    });
    expect(requests[1]?.headers?.['x-call']).toBeUndefined();
    expect(requests[1]?.signal).toBeUndefined();
    expect(requests[0]?.params).toEqual({ limit: 2 });
  });

  it('URLSearchParams 保留重复查询键，声明和每次调用分别复制容器', async () => {
    const { api, requests } = recorder();
    const params = new URLSearchParams('tag=a&tag=b');
    const body = api.post<void, boolean>('/', { params });
    params.set('tag', 'changed');
    await body();
    expect(String(requests[0]?.params)).toBe('tag=a&tag=b');
    (requests[0]?.params as URLSearchParams).set('tag', 'request-changed');
    await body();
    expect(String(requests[1]?.params)).toBe('tag=a&tag=b');
    const query = api.get<URLSearchParams, boolean>('/');
    await query(params);
    expect(String(requests[2]?.params)).toBe('tag=changed');
    expect(requests[2]?.params).not.toBe(params);
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
    const api = createHttpApi(
      httpClient
        .withBaseURL(origin)
        .withHeaders({ 'X-Base': 'base', 'X-Value': 'base' })
        .withResponseTransform(flattenEnvelopeResponse()),
    );
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
    const save = api.post<{ name: string }, Echo>('/users', {
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
    const getAccessToken = vi.fn(() => token);
    const refreshSession = vi.fn(async () => {
      token = 'fresh';
      return token;
    });
    const api = createHttpApi(
      httpClient.withBaseURL(origin).withAuth(
        createAuthSession({
          getSessionEpoch,
          getAccessToken,
          refreshSession,
        }),
      ),
    );
    const workspace = api.get<void, boolean>('/workspace');
    const login = api.post<{ username: string }, boolean>('/login', {
      auth: false,
    });
    const manual = api.post<void, boolean>('/manual', { authRecovery: false });
    expect(getSessionEpoch).not.toHaveBeenCalled();
    expect(getAccessToken).not.toHaveBeenCalled();
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
    const api = createHttpApi(
      httpClient
        .withBaseURL(origin)
        .withRetry({ maxRetries: 2, baseDelayMs: 0 }),
    );
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
    const endpoint = createHttpApi(httpClient.withBaseURL(origin)).get<
      void,
      boolean
    >('/');
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
    const request = vi.fn(async () => {
      throw failure;
    });
    const endpoint = createHttpApi({ request }).get<void, void>('/');
    await expect(endpoint()).rejects.toBe(failure);
  });
});
