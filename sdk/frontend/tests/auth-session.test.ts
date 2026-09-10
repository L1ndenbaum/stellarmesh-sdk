import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { AxiosError } from 'axios';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createAuthSession,
  flattenEnvelopeResponse,
  httpClient,
  HttpClientError,
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

describe('认证失败与操作失败分流', () => {
  it.each([
    [
      'SDK 超时',
      new HttpClientError('超时', {
        kind: 'timeout',
        cause: new Error('socket'),
      }),
      'timeout',
    ],
    [
      'SDK 网络故障',
      new HttpClientError('网络中断', { kind: 'network' }),
      'network',
    ],
    [
      'SDK 服务故障',
      new HttpClientError('暂不可用', { kind: 'http', status: 503 }),
      'http',
    ],
    [
      'Axios 网络故障',
      new AxiosError('连接中断', 'ERR_NETWORK', undefined, {}),
      'network',
    ],
    [
      'Axios 超时',
      new AxiosError('超时', 'ETIMEDOUT', undefined, {}),
      'timeout',
    ],
    ['凭证保存失败', new Error('保存失败'), 'auth'],
  ] as const)(
    '%s 直接传播，不退出，也不消耗业务传输重试',
    async (_name, failure, kind) => {
      let requests = 0;
      const origin = await serve((_req, res) => {
        requests++;
        json(res, {}, 401);
      });
      const onUnauthorized = vi.fn();
      const refreshSession = vi.fn(async () => {
        throw failure;
      });
      const auth = createAuthSession({
        getSessionEpoch: () => 1,
        getAccessToken: () => 'old',
        refreshSession,
        onUnauthorized,
      });
      const client = httpClient
        .withBaseURL(origin)
        .withAuth(auth)
        .withMaxRetries(2);
      const result = client.get('/');
      await expect(result).rejects.toMatchObject({ kind });
      if (failure instanceof HttpClientError)
        await expect(result).rejects.toBe(failure);
      else await expect(result).rejects.toMatchObject({ cause: failure });
      expect(refreshSession).toHaveBeenCalledTimes(1);
      expect(onUnauthorized).not.toHaveBeenCalled();
      expect(requests).toBe(1);
    },
  );

  it('null 才表示不可恢复，并向退出回调传递所属 epoch', async () => {
    const origin = await serve((_req, res) =>
      json(res, { code: 'EXPIRED' }, 401),
    );
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => null);
    const client = httpClient.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 'login-a',
        getAccessToken: () => 'old',
        refreshSession,
        onUnauthorized,
      }),
    );
    await expect(client.get('/')).rejects.toMatchObject({
      kind: 'http',
      status: 401,
      apiCode: 'EXPIRED',
    });
    expect(refreshSession).toHaveBeenCalledWith({ epoch: 'login-a' });
    expect(onUnauthorized).toHaveBeenCalledWith(
      expect.objectContaining({ status: 401 }),
      { epoch: 'login-a' },
    );
  });

  it.each(['', '   '])(
    '空 Token %j 是契约错误，不解释为失效',
    async (token) => {
      const origin = await serve((_req, res) => json(res, {}, 401));
      const onUnauthorized = vi.fn();
      const client = httpClient.withBaseURL(origin).withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAccessToken: () => 'old',
          refreshSession: async () => token,
          onUnauthorized,
        }),
      );
      await expect(client.get('/')).rejects.toMatchObject({ kind: 'auth' });
      expect(onUnauthorized).not.toHaveBeenCalled();
    },
  );

  it('自定义谓词只恢复匹配的 HTTP 错误，不匹配时不通知退出', async () => {
    const origin = await serve((req, res) => {
      if (req.headers.authorization === 'Bearer fresh') json(res, true);
      else
        json(res, { code: req.url === '/recover' ? 'EXPIRED' : 'DENIED' }, 401);
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => 'fresh');
    const client = httpClient.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAccessToken: () => 'old',
        refreshSession,
        onUnauthorized,
        shouldRefresh: (error) =>
          error.status === 401 && error.apiCode === 'EXPIRED',
      }),
    );
    await expect(client.get('/denied')).rejects.toMatchObject({
      apiCode: 'DENIED',
    });
    expect(refreshSession).not.toHaveBeenCalled();
    expect(await client.get('/recover')).toBe(true);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('HTTP 200 信封认证错误仅在显式匹配时恢复，重放失败只通知一次', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, { code: 'EXPIRED', message: '过期' });
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => 'fresh');
    const base = httpClient
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withResponseTransform(flattenEnvelopeResponse());
    const options = {
      getSessionEpoch: () => 1,
      getAccessToken: () => 'old',
      refreshSession,
      onUnauthorized,
    };
    await expect(
      base.withAuth(createAuthSession(options)).get('/'),
    ).rejects.toMatchObject({ kind: 'business' });
    expect(refreshSession).not.toHaveBeenCalled();
    const custom = base.withAuth(
      createAuthSession({
        ...options,
        shouldRefresh: (error) =>
          error.kind === 'business' && error.apiCode === 'EXPIRED',
      }),
    );
    await expect(custom.get('/')).rejects.toMatchObject({
      kind: 'business',
      status: 200,
    });
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(calls).toBe(3);
  });

  it('谓词异常保留 cause，网络错误不调用认证谓词', async () => {
    const origin = await serve((req, res) => {
      if (req.url === '/network') req.socket.destroy();
      else json(res, {}, 401);
    });
    const failure = new Error('判定失败');
    const shouldRefresh = vi.fn(() => {
      throw failure;
    });
    const onUnauthorized = vi.fn();
    const client = httpClient.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAccessToken: () => 'old',
        refreshSession: async () => null,
        shouldRefresh,
        onUnauthorized,
      }),
    );
    await expect(client.get('/network')).rejects.toMatchObject({
      kind: 'network',
    });
    expect(shouldRefresh).not.toHaveBeenCalled();
    await expect(client.get('/')).rejects.toMatchObject({
      kind: 'auth',
      cause: failure,
    });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('authRecovery false 保留凭证，retryable false 单独关闭传输重试', async () => {
    const seen: (string | undefined)[] = [];
    const origin = await serve((req, res) => {
      seen.push(req.headers.authorization);
      json(res, {}, req.headers.authorization === 'Bearer fresh' ? 503 : 401);
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => 'fresh');
    const client = httpClient
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAccessToken: () => 'old',
          refreshSession,
          onUnauthorized,
        }),
      );
    await expect(
      client.post<object, void>('/', {}, { authRecovery: false }),
    ).rejects.toMatchObject({ status: 401 });
    expect(seen).toEqual(['Bearer old']);
    expect(refreshSession).not.toHaveBeenCalled();
    expect(onUnauthorized).not.toHaveBeenCalled();
    await expect(
      client.post<object, void>('/', {}, { retryable: false }),
    ).rejects.toMatchObject({ status: 503 });
    expect(seen).toEqual(['Bearer old', 'Bearer old', 'Bearer fresh']);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('未配置刷新时不推断会话失效，auth false 也不参与会话操作', async () => {
    const origin = await serve((_req, res) => json(res, {}, 401));
    const onUnauthorized = vi.fn();
    const getAccessToken = vi.fn(() => 'old');
    const getSessionEpoch = vi.fn(() => 1);
    const client = httpClient
      .withBaseURL(origin)
      .withAuth(
        createAuthSession({ getSessionEpoch, getAccessToken, onUnauthorized }),
      );
    await expect(client.get('/')).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).not.toHaveBeenCalled();
    getAccessToken.mockClear();
    getSessionEpoch.mockClear();
    await expect(client.get('/', { auth: false })).rejects.toMatchObject({
      status: 401,
    });
    expect(getAccessToken).not.toHaveBeenCalled();
    expect(getSessionEpoch).not.toHaveBeenCalled();
  });

  it('401 恢复、503 普通重试和 200 成功共发送三次', async () => {
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, true, [401, 503, 200][calls - 1]);
    });
    const refreshSession = vi.fn(async () => 'fresh');
    const client = httpClient
      .withBaseURL(origin)
      .withRetry({ maxRetries: 1, baseDelayMs: 0 })
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAccessToken: () => 'old',
          refreshSession,
        }),
      );
    expect(await client.get('/')).toBe(true);
    expect(calls).toBe(3);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
});

describe('显式共享认证会话', () => {
  it('派生和独立装配客户端共享同一刷新，配置与可信范围保持独立', async () => {
    const started = deferred();
    const release = deferred<string>();
    let seenOld = 0;
    const allOld = deferred();
    let token = 'old';
    const origin = await serve((req, res) => {
      if (req.headers.authorization === 'Bearer old') {
        seenOld++;
        json(res, {}, 401);
        if (seenOld === 3) allOld.resolve();
      } else
        json(res, {
          token: req.headers.authorization,
          header: req.headers['x-client'],
        });
    });
    const foreign = await serve((req, res) =>
      json(res, { token: req.headers.authorization ?? null }),
    );
    const refreshSession = vi.fn(async () => {
      started.resolve();
      token = await release.promise;
      return token;
    });
    const auth = createAuthSession({
      getSessionEpoch: () => 1,
      getAccessToken: () => token,
      refreshSession,
    });
    const api = httpClient
      .withBaseURL(origin)
      .withAuth(auth, { trustedOrigins: [origin] })
      .withHeaders({ 'X-Client': 'api' });
    const slow = api.withTimeout(60_000).withHeaders({ 'x-client': 'slow' });
    const another = httpClient.withBaseURL(origin).withAuth(auth);
    const results = Promise.all([
      api.get('/'),
      slow.get('/'),
      another.get('/'),
    ]);
    await allOld.promise;
    await started.promise;
    release.resolve('fresh');
    expect(await results).toEqual([
      { token: 'Bearer fresh', header: 'api' },
      { token: 'Bearer fresh', header: 'slow' },
      { token: 'Bearer fresh' },
    ]);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(await api.withBaseURL(foreign).get('/')).toEqual({ token: null });
    expect(await another.get(foreign)).toEqual({ token: null });
  });

  it('同一配置创建的两个会话不共享刷新', async () => {
    const both = deferred();
    let calls = 0;
    const origin = await serve((req, res) =>
      json(res, true, req.headers.authorization === 'Bearer fresh' ? 200 : 401),
    );
    const refreshSession = vi.fn(async () => {
      calls++;
      if (calls === 2) both.resolve();
      await both.promise;
      return 'fresh';
    });
    const options = {
      getSessionEpoch: () => 1,
      getAccessToken: () => 'old',
      refreshSession,
    };
    const first = httpClient
      .withBaseURL(origin)
      .withAuth(createAuthSession(options));
    const second = first.withAuth(createAuthSession(options));
    expect(await Promise.all([first.get('/'), second.get('/')])).toEqual([
      true,
      true,
    ]);
    expect(refreshSession).toHaveBeenCalledTimes(2);
  });

  it('迟到的失败请求共享原错误，新请求可开始新代次恢复', async () => {
    const both = deferred();
    const late = deferred();
    let oldRequests = 0;
    const origin = await serve(async (req, res) => {
      if (req.headers.authorization === 'Bearer fresh') {
        json(res, true);
        return;
      }
      oldRequests++;
      if (oldRequests === 2) both.resolve();
      await both.promise;
      if (req.url === '/late') await late.promise;
      json(res, {}, 401);
    });
    const failure = new HttpClientError('暂时失败', { kind: 'network' });
    const refreshSession = vi.fn(async () => {
      if (refreshSession.mock.calls.length === 1) throw failure;
      return 'fresh';
    });
    const onUnauthorized = vi.fn();
    const auth = createAuthSession({
      getSessionEpoch: () => 1,
      getAccessToken: () => 'old',
      refreshSession,
      onUnauthorized,
    });
    const client = httpClient.withBaseURL(origin).withAuth(auth);
    const first = expect(client.get('/first')).rejects.toBe(failure);
    const delayed = expect(client.withTimeout(1000).get('/late')).rejects.toBe(
      failure,
    );
    await first;
    expect(await client.get('/new')).toBe(true);
    late.resolve();
    await delayed;
    expect(refreshSession).toHaveBeenCalledTimes(2);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
