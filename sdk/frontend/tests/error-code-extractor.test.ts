import { createServer } from 'node:http';
import type { IncomingMessage, Server, ServerResponse } from 'node:http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  AuthRefreshResult,
  createAuthSession,
  flattenEnvelopeResponse,
  http,
  HttpClientError,
  HttpErrorKind,
  isHttpClientError,
  ResponseType,
} from '../src/index.js';
import type { ErrorCodeExtractor } from '../src/index.js';

const servers: Server[] = [];

async function serve(
  handler: (req: IncomingMessage, res: ServerResponse) => void,
) {
  const server = createServer(handler);
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string')
    throw new Error('测试服务未启动');
  return `http://127.0.0.1:${address.port}`;
}

function json(res: ServerResponse, status: number, body: unknown) {
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'X-Reason': 'test',
  });
  res.end(JSON.stringify(body));
}

async function failure(pending: Promise<unknown>): Promise<HttpClientError> {
  try {
    await pending;
  } catch (error) {
    expect(isHttpClientError(error)).toBe(true);
    if (isHttpClientError(error)) return error;
    throw error;
  }
  throw new Error('预期请求失败');
}

const readErrorCode: ErrorCodeExtractor = (data) => {
  if (!data || typeof data !== 'object') return undefined;
  const value = (data as Record<string, unknown>).error_code;
  return typeof value === 'string' || typeof value === 'number'
    ? value
    : undefined;
};

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

describe('可配置错误码提取', () => {
  it.each([401, 422, 500])(
    'HTTP %i 保留实际状态、消息和完整响应',
    async (status) => {
      const body = {
        code: status,
        error_code: 'SPECIFIC_FAILURE',
        message: '明确的错误',
        data: { field: 'name' },
      };
      const origin = await serve((_req, res) => json(res, status, body));
      const extractor = vi.fn(readErrorCode);
      const base = http.withBaseURL(origin);
      const error = await failure(
        base.withErrorCodeExtractor(extractor).get<void, unknown>('/')(),
      );
      expect(error).toMatchObject({
        kind: HttpErrorKind.HTTP,
        status,
        apiCode: body.error_code,
        message: body.message,
        data: body,
        headers: { 'x-reason': 'test' },
      });
      expect(error.cause).toBeDefined();
      expect(extractor).toHaveBeenCalledExactlyOnceWith(
        body,
        expect.objectContaining({
          status,
          headers: expect.objectContaining({ 'x-reason': 'test' }),
        }),
      );
      expect(await failure(base.get<void, unknown>('/')())).toMatchObject({
        apiCode: status,
      });
    },
  );

  it.each(['TEXT_CODE', 0, null, undefined])(
    '自定义返回值 %s 不回退到默认 code',
    async (value) => {
      const origin = await serve((_req, res) =>
        json(res, 409, { code: 409, message: '冲突' }),
      );
      const error = await failure(
        http
          .withBaseURL(origin)
          .withErrorCodeExtractor(() => value)
          .get<void, unknown>('/')(),
      );
      expect(error.apiCode).toBe(value ?? undefined);
      expect(error.status).toBe(409);
    },
  );

  it('顶层错误码缺失时清空原值，提取函数也支持嵌套结构', async () => {
    const origin = await serve((_req, res) =>
      json(res, 422, {
        code: 422,
        message: '校验失败',
        error: { reason: 'NESTED_FAILURE' },
      }),
    );
    const base = http.withBaseURL(origin);
    expect(
      (
        await failure(
          base.withErrorCodeExtractor(readErrorCode).get<void, unknown>('/')(),
        )
      ).apiCode,
    ).toBeUndefined();
    const nested: ErrorCodeExtractor = (data) =>
      (data as { error: { reason: string } }).error.reason;
    expect(
      (
        await failure(
          base.withErrorCodeExtractor(nested).get<void, unknown>('/')(),
        )
      ).apiCode,
    ).toBe('NESTED_FAILURE');
  });

  it('同一配置覆盖 Envelope 业务失败，保持成功判断且不触发普通重试', async () => {
    let calls = 0;
    const body = {
      code: 422,
      error_code: 'INVALID_VALUE',
      message: '校验失败',
      data: null,
    };
    const origin = await serve((req, res) => {
      calls++;
      json(
        res,
        200,
        req.url === '/success' ? { ...body, code: 200, data: 7 } : body,
      );
    });
    const extractor = vi.fn(readErrorCode);
    const client = http
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withErrorCodeExtractor(extractor)
      .withResponseTransform(flattenEnvelopeResponse());
    const error = await failure(client.get<void, unknown>('/')());
    expect(error).toMatchObject({
      kind: HttpErrorKind.BUSINESS,
      status: 200,
      apiCode: 'INVALID_VALUE',
      data: body,
    });
    expect(calls).toBe(1);
    expect(await client.get<void, number>('/success')()).toBe(7);
    expect(extractor).toHaveBeenCalledTimes(1);
  });

  it('派生入口及 metadata 保留配置，其他实例隔离，raw 仍提取 HTTP 错误', async () => {
    const origin = await serve((_req, res) =>
      json(res, 401, { code: 401, error_code: 'EXPIRED', message: '过期' }),
    );
    const extractor = vi.fn(readErrorCode);
    const base = http.withBaseURL(origin);
    const configured = base.withErrorCodeExtractor(extractor);
    const derived = configured
      .withHeaders({ 'X-Client': 'derived' })
      .withTimeout(1000)
      .withMetadata();
    const error = await failure(
      derived.get<void, unknown>('/')(undefined, { responseMode: 'raw' }),
    );
    expect(error.apiCode).toBe('EXPIRED');
    expect(extractor).toHaveBeenCalledTimes(1);
    expect(
      (
        await failure(
          configured
            .withErrorCodeExtractor(() => 'OTHER')
            .get<void, unknown>('/')(),
        )
      ).apiCode,
    ).toBe('OTHER');
    expect((await failure(base.get<void, unknown>('/')())).apiCode).toBe(401);
    expect((await failure(http.get<void, unknown>(origin)())).apiCode).toBe(
      401,
    );
    expect((await failure(configured.get<void, unknown>('/')())).apiCode).toBe(
      'EXPIRED',
    );
  });

  it('不原地修改转换器错误，保留类别、cause、响应及原始堆栈', async () => {
    const origin = await serve((_req, res) => json(res, 200, {}));
    const cause = new Error('原始原因');
    const original = new HttpClientError('原始业务错误', {
      kind: HttpErrorKind.BUSINESS,
      status: 200,
      apiCode: 'OLD',
      data: { error_code: 'NEW' },
      headers: { etag: 'version' },
      cause,
    });
    const base = http.withBaseURL(origin).withResponseTransform(() => {
      throw original;
    });
    const error = await failure(
      base.withErrorCodeExtractor(readErrorCode).get<void, unknown>('/')(),
    );
    expect(error).not.toBe(original);
    expect(error).toMatchObject({
      apiCode: 'NEW',
      kind: original.kind,
      status: original.status,
      message: original.message,
    });
    expect(error.cause).toBe(cause);
    expect(error.data).toBe(original.data);
    expect(error.headers).toBe(original.headers);
    expect(error.stack).toBe(original.stack);
    expect(original.apiCode).toBe('OLD');
    expect(await failure(base.get<void, unknown>('/')())).toBe(original);
  });

  it.each([401, 200])(
    'HTTP %i 认证谓词读取新错误码，并发失败仍共享一次恢复',
    async (status) => {
      let token = 'old';
      let oldResponses = 0;
      let release!: () => void;
      const bothResponses = new Promise<void>((resolve) => {
        release = resolve;
      });
      const origin = await serve((req, res) => {
        if (req.headers.authorization === 'Bearer old') {
          json(res, status, {
            code: 401,
            error_code: 'EXPIRED',
            message: '过期',
            data: null,
          });
        } else {
          json(res, 200, { code: 200, message: '', data: true });
        }
      });
      const extractor = vi.fn<ErrorCodeExtractor>((data, context) => {
        if (++oldResponses === 2) release();
        return readErrorCode(data, context);
      });
      const refreshSession = vi.fn(async () => {
        await bothResponses;
        token = 'fresh';
        return AuthRefreshResult.REFRESHED;
      });
      const shouldRefresh = vi.fn(
        (error: HttpClientError) =>
          error.status === status && error.apiCode === 'EXPIRED',
      );
      const auth = createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${token}` }),
        shouldRefresh,
        refreshSession,
      });
      const client = http
        .withBaseURL(origin)
        .withErrorCodeExtractor(extractor)
        .withAuth(auth)
        .withResponseTransform(flattenEnvelopeResponse());
      expect(
        await Promise.all([
          client.get<void, boolean>('/')(),
          client.withMetadata().get<void, boolean>('/')(),
        ]),
      ).toEqual([true, expect.objectContaining({ data: true })]);
      expect(refreshSession).toHaveBeenCalledTimes(1);
      expect(extractor).toHaveBeenCalledTimes(2);
      expect(shouldRefresh).toHaveBeenCalledTimes(2);
    },
  );

  it('不改变 HTTP 传输重试，按每次失败响应提取一次', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(
        res,
        calls === 1 ? 503 : 200,
        calls === 1 ? { code: 503, error_code: 'BUSY', message: '忙' } : true,
      );
    });
    const extractor = vi.fn(readErrorCode);
    const client = http
      .withBaseURL(origin)
      .withErrorCodeExtractor(extractor)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 });
    expect(await client.get<void, boolean>('/')()).toBe(true);
    expect(calls).toBe(2);
    expect(extractor).toHaveBeenCalledTimes(1);
  });

  it.each([
    [
      '抛出异常',
      () => {
        throw new RangeError('提取异常');
      },
      RangeError,
    ],
    ['布尔值', () => false, TypeError],
    ['对象', () => ({}), TypeError],
    ['Promise', () => Promise.reject(new Error('异步失败')), TypeError],
    [
      'thenable',
      () => ({
        then: (_resolve: unknown, reject: (reason: Error) => void) =>
          reject(new Error('异步失败')),
      }),
      TypeError,
    ],
  ])(
    '拒绝%s并保留 HTTP 诊断，不刷新或重试',
    async (_name, extract, causeType) => {
      let calls = 0;
      const origin = await serve((_req, res) => {
        calls++;
        json(res, 503, { code: 503, message: '上游不可用' });
      });
      const shouldRefresh = vi.fn(() => true);
      const refreshSession = vi.fn(async () => AuthRefreshResult.REFRESHED);
      const auth = createAuthSession({
        getSessionEpoch: () => 1,
        shouldRefresh,
        refreshSession,
      });
      const client = http
        .withBaseURL(origin)
        .withAuth(auth)
        .withMaxRetries(2)
        .withErrorCodeExtractor(extract as unknown as ErrorCodeExtractor);
      const error = await failure(client.get<void, unknown>('/')());
      expect(error).toMatchObject({
        kind: HttpErrorKind.RESPONSE_FORMAT,
        status: 503,
        data: { code: 503 },
        headers: { 'x-reason': 'test' },
      });
      expect(error.cause).toBeInstanceOf(causeType);
      expect(calls).toBe(1);
      expect(shouldRefresh).not.toHaveBeenCalled();
      expect(refreshSession).not.toHaveBeenCalled();
    },
  );

  it('配置必须是函数', () => {
    expect(() =>
      http.withErrorCodeExtractor(null as unknown as ErrorCodeExtractor),
    ).toThrow(TypeError);
  });

  it('成功、204、HEAD、文本和二进制响应不触发提取', async () => {
    const origin = await serve((req, res) => {
      if (req.url === '/empty') {
        res.writeHead(204);
        res.end();
      } else
        json(res, 200, { code: 200, error_code: null, message: '', data: 7 });
    });
    const extractor = vi.fn(readErrorCode);
    const client = http
      .withBaseURL(origin)
      .withErrorCodeExtractor(extractor)
      .withResponseTransform(flattenEnvelopeResponse());
    expect(await client.get<void, number>('/')()).toBe(7);
    expect(await client.get<void, number>('/empty')()).toBeUndefined();
    expect(await client.head<void, undefined>('/')()).toBeUndefined();
    expect(
      await client.get<void, string>('/', {
        responseType: ResponseType.TEXT,
      })(),
    ).toContain('error_code');
    expect(
      await client.get<void, ArrayBuffer>('/', {
        responseType: ResponseType.ARRAYBUFFER,
      })(),
    ).toBeInstanceOf(Uint8Array);
    expect(extractor).not.toHaveBeenCalled();
  });

  it('非 JSON HTTP 错误仍保留状态，网络、超时、取消和格式错误不提取', async () => {
    const origin = await serve((req, res) => {
      if (req.url === '/network') req.socket.destroy();
      else if (req.url === '/slow') return;
      else {
        res.writeHead(req.url === '/html' ? 503 : 200);
        res.end('<html>unavailable</html>');
      }
    });
    const extractor = vi.fn(readErrorCode);
    const client = http.withBaseURL(origin).withErrorCodeExtractor(extractor);
    const html = await failure(client.get<void, unknown>('/html')());
    expect(html).toMatchObject({
      kind: HttpErrorKind.HTTP,
      status: 503,
      data: '<html>unavailable</html>',
    });
    expect(html.apiCode).toBeUndefined();
    expect(extractor).toHaveBeenCalledTimes(1);
    extractor.mockClear();
    expect((await failure(client.get<void, unknown>('/network')())).kind).toBe(
      HttpErrorKind.NETWORK,
    );
    expect(
      (await failure(client.get<void, unknown>('/slow', { timeout: 10 })()))
        .kind,
    ).toBe(HttpErrorKind.TIMEOUT);
    expect((await failure(client.get<void, unknown>('/')())).kind).toBe(
      HttpErrorKind.RESPONSE_FORMAT,
    );
    const controller = new AbortController();
    controller.abort();
    expect(
      (
        await failure(
          client.get<void, unknown>('/')(undefined, {
            signal: controller.signal,
          }),
        )
      ).kind,
    ).toBe(HttpErrorKind.CANCELED);
    expect(extractor).not.toHaveBeenCalled();
  });

  it.each([ResponseType.TEXT, ResponseType.ARRAYBUFFER])(
    'HTTP 错误的 %s 数据原样交给提取器',
    async (responseType) => {
      const origin = await serve((_req, res) => {
        res.writeHead(500, { 'Content-Type': 'application/octet-stream' });
        res.end('upstream failed');
      });
      const extractor = vi.fn<ErrorCodeExtractor>(() => 'UPSTREAM');
      const error = await failure(
        http
          .withBaseURL(origin)
          .withErrorCodeExtractor(extractor)
          .get<void, unknown>('/', { responseType })(),
      );
      expect(error).toMatchObject({
        kind: HttpErrorKind.HTTP,
        status: 500,
        apiCode: 'UPSTREAM',
      });
      expect(extractor.mock.calls[0]?.[0]).toBe(error.data);
      if (responseType === ResponseType.TEXT)
        expect(error.data).toBe('upstream failed');
      else expect(error.data).toBeInstanceOf(Uint8Array);
      expect(extractor).toHaveBeenCalledTimes(1);
    },
  );

  it('提取回调切换会话时拒绝旧请求，认证回调失败不再次提取', async () => {
    let epoch = 1;
    const origin = await serve((_req, res) =>
      json(res, 401, { code: 401, message: '过期' }),
    );
    const authFailure = new HttpClientError('刷新失败', {
      kind: HttpErrorKind.HTTP,
      status: 503,
      apiCode: 'REFRESH_FAILED',
    });
    const refreshSession = vi.fn(async () => {
      throw authFailure;
    });
    const auth = createAuthSession({
      getSessionEpoch: () => epoch,
      shouldRefresh: () => true,
      refreshSession,
    });
    const base = http.withBaseURL(origin).withAuth(auth);
    expect(
      (
        await failure(
          base
            .withErrorCodeExtractor(() => {
              epoch++;
              return 'EXPIRED';
            })
            .get<void, unknown>('/')(),
        )
      ).kind,
    ).toBe(HttpErrorKind.SESSION_CHANGED);
    expect(refreshSession).not.toHaveBeenCalled();
    const extractor = vi.fn(() => 'EXPIRED');
    expect(
      await failure(
        base.withErrorCodeExtractor(extractor).get<void, unknown>('/')(),
      ),
    ).toBe(authFailure);
    expect(extractor).toHaveBeenCalledTimes(1);
  });
});
