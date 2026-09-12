import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { afterEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import {
  AuthRefreshResult,
  createAuthSession,
  flattenEnvelopeResponse,
  http,
  HttpClientError,
  isHttpClientError,
} from '../src/index.js';
import type { HttpApi, HttpResponse } from '../src/index.js';

type Handler = (
  req: IncomingMessage,
  res: ServerResponse,
) => void | Promise<void>;

const servers: Server[] = [];

async function serve(handler: Handler): Promise<string> {
  const server = createServer((req, res) => {
    void handler(req, res);
  });
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string')
    throw new Error('无法启动测试服务');
  return `http://127.0.0.1:${address.port}`;
}

function json(res: ServerResponse, data: unknown, status = 200) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(data));
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(async () => {
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
describe('公开 HTTP 契约', () => {
  it('配置派生隔离、大小写不敏感头覆盖、请求参数和请求体', async () => {
    const origin = await serve(async (req, res) => {
      let body = '';
      for await (const chunk of req) body += String(chunk);
      json(res, { url: req.url, header: req.headers['x-value'], body });
    });
    const base = http.withBaseURL(origin).withHeaders({ 'X-Value': 'base' });
    const child = base.withHeaders({ 'x-value': 'child' });
    type Echo = {
      url: string;
      header: string;
      body: string;
    };
    expect((await base.get<void, Echo>('/')()).header).toBe('base');
    expect((await child.get<void, Echo>('/')()).header).toBe('child');
    const result = await child.post<
      {
        id: number;
      },
      Echo
    >('/items')(
      { id: 2 },
      {
        params: { page: 3 },
        headers: { 'X-VALUE': 'request' },
      },
    );
    expect(result).toEqual({
      url: '/items?page=3',
      header: 'request',
      body: '{"id":2}',
    });
    expect((await http.get<void, Echo>(origin)()).header).toBeUndefined();
  });
  it('处理信封、保留 metadata、允许单次原始响应', async () => {
    const envelope = { code: 0, message: '', data: { id: 7 } };
    const origin = await serve((_req, res) => {
      res.setHeader('ETag', 'part-1');
      json(res, envelope);
    });
    const client = http
      .withBaseURL(origin)
      .withResponseTransform(flattenEnvelopeResponse());
    expect(await client.get<void, unknown>('/')()).toEqual({ id: 7 });
    expect(
      await client.get<void, unknown>('/')(undefined, { responseMode: 'raw' }),
    ).toEqual(envelope);
    expect(
      await client.withMetadata().request<
        void,
        {
          id: number;
        }
      >(() => ({
        method: 'GET',
        url: '/',
      }))(),
    ).toMatchObject({
      data: { id: 7 },
      status: 200,
      headers: { etag: 'part-1' },
    });
  });
  it('异步响应转换在普通与 metadata 入口一致，失败和取消保持统一错误', async () => {
    const origin = await serve((_req, res) => json(res, { value: 1 }));
    const client = http
      .withBaseURL(origin)
      .withResponseTransform(async () => ({ id: 7 }));
    expect(await client.get<void, unknown>('/')()).toEqual({ id: 7 });
    expect(
      await client.withMetadata().request<void, unknown>(() => ({
        method: 'GET',
        url: '/',
      }))(),
    ).toMatchObject({ data: { id: 7 } });
    const failure = new Error('异步转换失败');
    await expect(
      client
        .withResponseTransform(async () => {
          throw failure;
        })
        .get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'response-format', cause: failure });
    const started = deferred<void>();
    const pending = deferred<unknown>();
    const controller = new AbortController();
    const canceled = expect(
      client
        .withResponseTransform(() => {
          started.resolve();
          return pending.promise;
        })
        .withMetadata()
        .request<void, unknown>(() => ({
          method: 'GET',
          url: '/',
        }))(undefined, {
        signal: controller.signal,
      }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    await started.promise;
    controller.abort();
    await canceled;
    pending.resolve(null);
  });
  it('业务失败不因 HTTP 200 成功，且不触发重试', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, { code: 422, message: '无效', data: null });
    });
    const client = http
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withResponseTransform(flattenEnvelopeResponse());
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'business',
      status: 200,
      apiCode: 422,
      message: '无效',
    });
    expect(calls).toBe(1);
  });
  it('严格信封检查、兼容透传和自定义成功码', async () => {
    const origin = await serve((req, res) =>
      json(
        res,
        req.url === '/custom'
          ? { code: 'OK', message: '', data: true }
          : { id: 1 },
      ),
    );
    const client = http.withBaseURL(origin);
    await expect(
      client
        .withResponseTransform(flattenEnvelopeResponse())
        .get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'response-format' });
    expect(
      await client
        .withResponseTransform(
          flattenEnvelopeResponse({ allowNonEnvelope: true }),
        )
        .get<void, unknown>('/')(),
    ).toEqual({ id: 1 });
    expect(
      await client
        .withResponseTransform(
          flattenEnvelopeResponse({ isSuccess: (code) => code === 'OK' }),
        )
        .get<void, unknown>('/custom')(),
    ).toBe(true);
  });
  it('分别保留 null、缺失 data、204 和 HEAD', async () => {
    const origin = await serve((req, res) => {
      if (req.url === '/empty') {
        res.writeHead(204);
        res.end();
        return;
      }
      json(res, {
        code: 0,
        message: '',
        ...(req.url === '/null' ? { data: null } : {}),
      });
    });
    const client = http
      .withBaseURL(origin)
      .withResponseTransform(flattenEnvelopeResponse());
    expect(await client.get<void, unknown>('/null')()).toBeNull();
    expect(await client.get<void, unknown>('/missing')()).toBeUndefined();
    expect(await client.get<void, unknown>('/empty')()).toBeUndefined();
    expect(await client.head<void, unknown>('/')()).toBeUndefined();
  });
  it('不将二进制和文本送入信封处理，支持二进制请求体与 ETag', async () => {
    const origin = await serve(async (req, res) => {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      res.setHeader('ETag', '"part"');
      res.end(Buffer.concat(chunks));
    });
    const client = http
      .withBaseURL(origin)
      .withResponseTransform(flattenEnvelopeResponse());
    const result = await client
      .withMetadata()
      .request<void, ArrayBuffer>(() => ({
        method: 'PUT',
        url: '/',
        data: new Uint8Array([1, 2, 3]).buffer,
      }))(undefined, {
      responseType: 'arraybuffer',
    });
    expect([...new Uint8Array(result.data)]).toEqual([1, 2, 3]);
    expect(result.headers.etag).toBe('"part"');
    expect(
      await client.post<string, string>('/')('hello', { responseType: 'text' }),
    ).toBe('hello');
  });
  it('规范化 HTTP、网络、超时、取消、JSON 和转换错误', async () => {
    const origin = await serve((req, res) => {
      if (req.url === '/network') {
        req.socket.destroy();
        return;
      }
      if (req.url === '/slow') return;
      if (req.url === '/invalid') {
        res.setHeader('Content-Type', 'application/json');
        res.end('{bad');
        return;
      }
      json(res, { code: 'DENIED', message: '禁止' }, 403);
    });
    const client = http.withBaseURL(origin);
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'http',
      status: 403,
      apiCode: 'DENIED',
    });
    await expect(client.get<void, unknown>('/network')()).rejects.toMatchObject(
      {
        kind: 'network',
      },
    );
    await expect(
      client.withTimeout(10).get<void, unknown>('/slow')(),
    ).rejects.toMatchObject({
      kind: 'timeout',
    });
    await expect(
      client.get<void, unknown>('/slow')(undefined, {
        signal: AbortSignal.timeout(10),
      }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    await expect(client.get<void, unknown>('/invalid')()).rejects.toMatchObject(
      {
        kind: 'response-format',
      },
    );
    const ok = await serve((_req, res) => json(res, {}));
    await expect(
      http
        .withResponseTransform(() => {
          throw new Error('bad');
        })
        .get<void, unknown>(ok)(),
    ).rejects.toMatchObject({
      kind: 'response-format',
      cause: new Error('bad'),
    });
    expect(
      isHttpClientError(new HttpClientError('失败', { kind: 'network' })),
    ).toBe(true);
    expect(isHttpClientError(new Error('失败'))).toBe(false);
  });
  it('非 JSON 的 HTTP 错误仍按状态重试和刷新，格式错误保留原响应', async () => {
    let accessToken = 'old';
    let calls = 0;
    const origin = await serve((req, res) => {
      calls++;
      if (req.url === '/auth' && req.headers.authorization === 'Bearer fresh') {
        json(res, true);
        return;
      }
      res.writeHead(
        req.url === '/auth' ? 401 : req.url === '/invalid' ? 200 : 503,
        { 'Content-Type': 'text/html' },
      );
      res.end('<html>网关响应</html>');
    });
    const client = http
      .withBaseURL(origin)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 });
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'http',
      status: 503,
      data: '<html>网关响应</html>',
    });
    expect(calls).toBe(2);
    expect(
      await client
        .withAuth(
          createAuthSession({
            getSessionEpoch: () => 1,
            getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
            shouldRefresh: (error: HttpClientError) => error.status === 401,
            refreshSession: async () => {
              accessToken = 'fresh';
              return AuthRefreshResult.REFRESHED;
            },
          }),
        )
        .get<void, unknown>('/auth')(),
    ).toBe(true);
    await expect(client.get<void, unknown>('/invalid')()).rejects.toMatchObject(
      {
        kind: 'response-format',
        status: 200,
        data: '<html>网关响应</html>',
      },
    );
  });
  it('拒绝无效配置', () => {
    expect(() => http.withMaxRetries(1.5)).toThrow(RangeError);
    expect(() => http.withTimeout(-1)).toThrow(RangeError);
    expect(() => http.withRetry({ maxDelayMs: Infinity })).toThrow(RangeError);
  });
  it('编译期响应类型与请求体契约', () => {
    // 函数不执行；tsc 检查真实调用，避免通过断言掩盖泛型顺序错误。
    const compile = (client: HttpApi) => {
      // @ts-expect-error 旧配置对象不再是 withAuth 的装配入口。
      http.withAuth({ getAuthHeaders: () => null });
      // @ts-expect-error 显式会话必须提供 epoch 读取接口。
      createAuthSession({ getAuthHeaders: () => null });
      expectTypeOf(
        client.post<
          {
            name: string;
          },
          {
            id: number;
          }
        >('/')({ name: 'a' }),
      ).toEqualTypeOf<
        Promise<{
          id: number;
        }>
      >();
      expectTypeOf(client.post<void, void>('/')()).toEqualTypeOf<
        Promise<void>
      >();
      expectTypeOf(
        client.get<
          void,
          {
            id: number;
          }
        >('/')(),
      ).toEqualTypeOf<
        Promise<{
          id: number;
        }>
      >();
      expectTypeOf(
        client.withMetadata().request<void, string>(() => ({
          method: 'PUT',
          url: '/',
          data: new Blob(),
        }))(),
      ).toEqualTypeOf<Promise<HttpResponse<string>>>();
      const declared = client.post<{ name: string }, { id: number }>('/');
      // @ts-expect-error 请求体必须匹配第一个泛型。
      void declared({ id: 1 });
    };
    expect(compile).toBeTypeOf('function');
  });
});
describe('重试与取消', () => {
  it('默认不重试、GET 有限重试、POST 默认不重试、PUT 显式开启', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, {}, 503);
    });
    const base = http.withBaseURL(origin);
    await expect(base.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(1);
    const retry = base.withRetry({ maxRetries: 2, baseDelayMs: 0 });
    await expect(retry.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(4);
    await expect(retry.post<object, void>('/')({})).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(5);
    await expect(
      retry.put<string, void>('/')('chunk', { retryable: true }),
    ).rejects.toMatchObject({ status: 503 });
    expect(calls).toBe(8);
    await expect(
      retry.get<void, unknown>('/')(undefined, { maxRetries: 0 }),
    ).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(9);
  });
  it('遵守 Retry-After，过长等待直接失败', async () => {
    const times: number[] = [];
    const origin = await serve((req, res) => {
      times.push(Date.now());
      res.setHeader('Retry-After', req.url === '/long' ? '11' : '0.03');
      json(res, {}, 429);
    });
    const client = http
      .withBaseURL(origin)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 });
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 429,
    });
    expect(times[1]! - times[0]!).toBeGreaterThanOrEqual(25);
    await expect(client.get<void, unknown>('/long')()).rejects.toMatchObject({
      status: 429,
    });
    expect(times).toHaveLength(3);
  });
  it('取消退避不继续发请求，预取消不发送', async () => {
    let calls = 0;
    const received = deferred<void>();
    const origin = await serve((_req, res) => {
      calls++;
      res.setHeader('Retry-After', '1');
      json(res, {}, 503);
      received.resolve();
    });
    const client = http.withBaseURL(origin).withMaxRetries(3);
    const controller = new AbortController();
    const result = client.get<void, unknown>('/')(undefined, {
      signal: controller.signal,
    });
    const check = expect(result).rejects.toMatchObject({ kind: 'canceled' });
    await received.promise;
    await new Promise((resolve) => setTimeout(resolve, 20));
    controller.abort();
    await check;
    await expect(
      client.get<void, unknown>('/')(undefined, { signal: controller.signal }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    expect(calls).toBe(1);
  });
});
describe('鉴权与刷新', () => {
  it('限定可信 origin、显式关闭鉴权、阻止协议相对地址注入', async () => {
    let accessToken = 'secret';
    const foreign = await serve((req, res) =>
      json(res, { token: req.headers.authorization ?? null }),
    );
    const origin = await serve((req, res) =>
      json(res, { token: req.headers.authorization ?? null }),
    );
    const getAuthHeaders = vi.fn(() => ({
      Authorization: `Bearer ${accessToken}`,
    }));
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders,
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
      }),
    );
    expect(await client.get<void, unknown>('/')()).toEqual({
      token: 'Bearer secret',
    });
    expect(
      await client.get<void, unknown>('/')(undefined, { auth: false }),
    ).toEqual({ token: null });
    expect(await client.get<void, unknown>(foreign)()).toEqual({ token: null });
    // Node 不接受协议相对 URL，但 SDK 也不能为它读取或注入业务 Token。
    await expect(
      client.get<void, unknown>(foreign.replace('http:', ''))(),
    ).rejects.toBeInstanceOf(HttpClientError);
    expect(getAuthHeaders).toHaveBeenCalledTimes(1);
    expect(refreshSession).not.toHaveBeenCalled();
    const trusted = client.withAuth(
      createAuthSession({ getSessionEpoch: () => 1, getAuthHeaders }),
      { trustedOrigins: [foreign] },
    );
    expect(await trusted.get<void, unknown>(foreign)()).toEqual({
      token: 'Bearer secret',
    });
    expect(await trusted.get<void, unknown>(origin)()).toEqual({ token: null });
  });
  it('并发和迟到的旧 Token 401 共用刷新结果', async () => {
    let accessToken = 'expired';
    const both = deferred<void>();
    const releaseLate = deferred<void>();
    let expired = 0;
    const origin = await serve(async (req, res) => {
      if (req.headers.authorization === 'Bearer expired') {
        expired++;
        if (expired === 2) both.resolve();
        await both.promise;
        if (req.url === '/late') await releaseLate.promise;
        json(res, {}, 401);
      } else json(res, req.headers.authorization);
    });
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
      }),
    );
    const first = client.get<void, unknown>('/first')();
    const late = client.get<void, unknown>('/late')();
    expect(await first).toBe('Bearer fresh');
    releaseLate.resolve();
    expect(await late).toBe('Bearer fresh');
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('并发刷新失败只通知一次，抛出的刷新错误保留 cause', async () => {
    const accessToken = 'old';
    const release = deferred<AuthRefreshResult>();
    const received = deferred<void>();
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, {}, 401);
      if (calls === 2) received.resolve();
    });
    const refreshSession = vi.fn(() => release.promise);
    const onUnauthorized = vi.fn();
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
        onUnauthorized,
      }),
    );
    const all = Promise.allSettled([
      client.get<void, unknown>('/a')(),
      client.get<void, unknown>('/b')(),
    ]);
    await received.promise;
    await new Promise((resolve) => setTimeout(resolve, 10));
    release.resolve(AuthRefreshResult.EXPIRED);
    expect((await all).every((result) => result.status === 'rejected')).toBe(
      true,
    );
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    const cause = new Error('刷新连接失败');
    await expect(
      client
        .withAuth(
          createAuthSession({
            getSessionEpoch: () => 1,
            getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
            shouldRefresh: (error: HttpClientError) => error.status === 401,
            refreshSession: async () => {
              throw cause;
            },
          }),
        )
        .get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'auth', cause });
  });
  it('取消一个刷新等待者不会取消其他请求', async () => {
    let accessToken = 'old';
    const refresh = deferred<string | null>();
    const started = deferred<void>();
    const origin = await serve((req, res) =>
      json(res, {}, req.headers.authorization === 'Bearer fresh' ? 200 : 401),
    );
    const refreshSession = vi.fn(async () => {
      started.resolve();
      accessToken = (await refresh.promise)!;
      return AuthRefreshResult.REFRESHED;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
      }),
    );
    const controller = new AbortController();
    const canceled = expect(
      client.get<void, unknown>('/a')(undefined, { signal: controller.signal }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    const other = client.get<void, unknown>('/b')();
    await started.promise;
    controller.abort();
    await canceled;
    refresh.resolve('fresh');
    expect(await other).toEqual({});
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('刷新重放和普通重试共享额度，不对外部 401 刷新', async () => {
    let accessToken = 'old';
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, {}, calls === 2 ? 401 : 503);
    });
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http
      .withBaseURL(origin)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 })
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession,
        }),
      );
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 503,
    });
    expect(calls).toBe(3);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    const foreign = await serve((_req, res) => json(res, {}, 401));
    await expect(client.get<void, unknown>(foreign)()).rejects.toMatchObject({
      status: 401,
    });
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
  it('再次 401 不循环恢复，后续新请求可以再次刷新', async () => {
    let accessToken = 'old';
    const origin = await serve((_req, res) => json(res, {}, 401));
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
      }),
    );
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 401,
    });
    expect(refreshSession).toHaveBeenCalledTimes(1);
    await expect(
      client.withTimeout(1000).get<void, unknown>('/')(),
    ).rejects.toMatchObject({
      status: 401,
    });
    expect(refreshSession).toHaveBeenCalledTimes(2);
  });
});
