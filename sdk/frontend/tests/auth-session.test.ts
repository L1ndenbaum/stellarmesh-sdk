import { createServer } from 'node:http';
import type { IncomingMessage, ServerResponse, Server } from 'node:http';
import { AxiosError } from 'axios';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  AuthRefreshResult,
  createAuthSession,
  flattenEnvelopeResponse,
  http,
  HttpClientError,
} from '../src/index.js';
import type { AuthSessionOptions, HttpHeaders } from '../src/index.js';

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
      const accessToken = 'old';
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
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
        onUnauthorized,
      });
      const client = http.withBaseURL(origin).withAuth(auth).withMaxRetries(2);
      const result = client.get<void, unknown>('/')();
      await expect(result).rejects.toMatchObject({ kind });
      if (failure instanceof HttpClientError)
        await expect(result).rejects.toBe(failure);
      else await expect(result).rejects.toMatchObject({ cause: failure });
      expect(refreshSession).toHaveBeenCalledTimes(1);
      expect(onUnauthorized).not.toHaveBeenCalled();
      expect(requests).toBe(1);
    },
  );
  it('EXPIRED 才表示不可恢复，并向退出回调传递所属 epoch', async () => {
    const accessToken = 'old';
    const origin = await serve((_req, res) =>
      json(res, { code: 'EXPIRED' }, 401),
    );
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => AuthRefreshResult.EXPIRED);
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 'login-a',
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession,
        onUnauthorized,
      }),
    );
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
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
  it.each([null, undefined, true, false, '', 'fresh', {}])(
    '非法刷新结果 %j 是契约错误，不解释为失效',
    async (token) => {
      const accessToken = 'old';
      let requests = 0;
      const origin = await serve((_req, res) => {
        requests++;
        json(res, {}, 401);
      });
      const onUnauthorized = vi.fn();
      const client = http.withBaseURL(origin).withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession: async () => token as AuthRefreshResult,
          onUnauthorized,
        }),
      );
      await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
        kind: 'auth',
      });
      expect(onUnauthorized).not.toHaveBeenCalled();
      expect(requests).toBe(1);
    },
  );
  it('自定义谓词只恢复匹配的 HTTP 错误，不匹配时不通知退出', async () => {
    let accessToken = 'old';
    const origin = await serve((req, res) => {
      if (req.headers.authorization === 'Bearer fresh') json(res, true);
      else
        json(res, { code: req.url === '/recover' ? 'EXPIRED' : 'DENIED' }, 401);
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        refreshSession,
        onUnauthorized,
        shouldRefresh: (error) =>
          error.status === 401 && error.apiCode === 'EXPIRED',
      }),
    );
    await expect(client.get<void, unknown>('/denied')()).rejects.toMatchObject({
      apiCode: 'DENIED',
    });
    expect(refreshSession).not.toHaveBeenCalled();
    expect(await client.get<void, unknown>('/recover')()).toBe(true);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it('HTTP 200 信封认证错误仅在显式匹配时恢复，重放失败只通知一次', async () => {
    let accessToken = 'old';
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, { code: 'EXPIRED', message: '过期' });
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const base = http
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withResponseTransform(flattenEnvelopeResponse());
    const options = {
      getSessionEpoch: () => 1,
      getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession,
      onUnauthorized,
    };
    await expect(
      base.withAuth(createAuthSession(options)).get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'business' });
    expect(refreshSession).not.toHaveBeenCalled();
    const custom = base.withAuth(
      createAuthSession({
        ...options,
        shouldRefresh: (error: HttpClientError) =>
          error.kind === 'business' && error.apiCode === 'EXPIRED',
      }),
    );
    await expect(custom.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'business',
      status: 200,
    });
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(calls).toBe(3);
  });
  it('谓词异常保留 cause，网络错误不调用认证谓词', async () => {
    const accessToken = 'old';
    const origin = await serve((req, res) => {
      if (req.url === '/network') req.socket.destroy();
      else json(res, {}, 401);
    });
    const failure = new Error('判定失败');
    const shouldRefresh = vi.fn(() => {
      throw failure;
    });
    const onUnauthorized = vi.fn();
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        refreshSession: async () => AuthRefreshResult.EXPIRED,
        shouldRefresh,
        onUnauthorized,
      }),
    );
    await expect(client.get<void, unknown>('/network')()).rejects.toMatchObject(
      {
        kind: 'network',
      },
    );
    expect(shouldRefresh).not.toHaveBeenCalled();
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'auth',
      cause: failure,
    });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it('authRecovery false 保留凭证，retryable false 单独关闭传输重试', async () => {
    let accessToken = 'old';
    const seen: (string | undefined)[] = [];
    const origin = await serve((req, res) => {
      seen.push(req.headers.authorization);
      json(res, {}, req.headers.authorization === 'Bearer fresh' ? 503 : 401);
    });
    const onUnauthorized = vi.fn();
    const refreshSession = vi.fn(async () => {
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const client = http
      .withBaseURL(origin)
      .withMaxRetries(2)
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession,
          onUnauthorized,
        }),
      );
    await expect(
      client.post<object, void>('/')({}, { authRecovery: false }),
    ).rejects.toMatchObject({ status: 401 });
    expect(seen).toEqual(['Bearer old']);
    expect(refreshSession).not.toHaveBeenCalled();
    expect(onUnauthorized).not.toHaveBeenCalled();
    await expect(
      client.post<object, void>('/')({}, { retryable: false }),
    ).rejects.toMatchObject({ status: 503 });
    expect(seen).toEqual(['Bearer old', 'Bearer old', 'Bearer fresh']);
    expect(refreshSession).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it('未配置刷新时不推断会话失效，auth false 也不参与会话操作', async () => {
    const accessToken = 'old';
    const origin = await serve((_req, res) => json(res, {}, 401));
    const getAuthHeaders = vi.fn(() => ({
      Authorization: `Bearer ${accessToken}`,
    }));
    const getSessionEpoch = vi.fn(() => 1);
    const client = http
      .withBaseURL(origin)
      .withAuth(createAuthSession({ getSessionEpoch, getAuthHeaders }));
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      status: 401,
    });
    getAuthHeaders.mockClear();
    getSessionEpoch.mockClear();
    await expect(
      client.get<void, unknown>('/')(undefined, { auth: false }),
    ).rejects.toMatchObject({
      status: 401,
    });
    expect(getAuthHeaders).not.toHaveBeenCalled();
    expect(getSessionEpoch).not.toHaveBeenCalled();
  });
  it('401 恢复、503 普通重试和 200 成功共发送三次', async () => {
    let accessToken = 'old';
    let calls = 0;
    const origin = await serve((_req, res) => {
      calls++;
      json(res, true, [401, 503, 200][calls - 1]);
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
    expect(await client.get<void, unknown>('/')()).toBe(true);
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
      return AuthRefreshResult.REFRESHED;
    });
    const auth = createAuthSession({
      getSessionEpoch: () => 1,
      getAuthHeaders: () => ({ Authorization: `Bearer ${token}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession,
    });
    const api = http
      .withBaseURL(origin)
      .withAuth(auth, { trustedOrigins: [origin] })
      .withHeaders({ 'X-Client': 'api' });
    const slow = api.withTimeout(60000).withHeaders({ 'x-client': 'slow' });
    const another = http.withBaseURL(origin).withAuth(auth);
    const results = Promise.all([
      api.get<void, unknown>('/')(),
      slow.get<void, unknown>('/')(),
      another.get<void, unknown>('/')(),
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
    expect(await api.withBaseURL(foreign).get<void, unknown>('/')()).toEqual({
      token: null,
    });
    expect(await another.get<void, unknown>(foreign)()).toEqual({
      token: null,
    });
  });
  it('同一配置创建的两个会话不共享刷新', async () => {
    let accessToken = 'old';
    const both = deferred();
    let calls = 0;
    const origin = await serve((req, res) =>
      json(res, true, req.headers.authorization === 'Bearer fresh' ? 200 : 401),
    );
    const refreshSession = vi.fn(async () => {
      calls++;
      if (calls === 2) both.resolve();
      await both.promise;
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const options = {
      getSessionEpoch: () => 1,
      getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession,
    };
    const first = http.withBaseURL(origin).withAuth(createAuthSession(options));
    const second = first.withAuth(createAuthSession(options));
    expect(
      await Promise.all([
        first.get<void, unknown>('/')(),
        second.get<void, unknown>('/')(),
      ]),
    ).toEqual([true, true]);
    expect(refreshSession).toHaveBeenCalledTimes(2);
  });
  it('迟到的失败请求共享原错误，新请求可开始新代次恢复', async () => {
    let accessToken = 'old';
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
      accessToken = 'fresh';
      return AuthRefreshResult.REFRESHED;
    });
    const onUnauthorized = vi.fn();
    const auth = createAuthSession({
      getSessionEpoch: () => 1,
      getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession,
      onUnauthorized,
    });
    const client = http.withBaseURL(origin).withAuth(auth);
    const first = expect(client.get<void, unknown>('/first')()).rejects.toBe(
      failure,
    );
    const delayed = expect(
      client.withTimeout(1000).get<void, unknown>('/late')(),
    ).rejects.toBe(failure);
    await first;
    expect(await client.get<void, unknown>('/new')()).toBe(true);
    late.resolve();
    await delayed;
    expect(refreshSession).toHaveBeenCalledTimes(2);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
describe('登录会话生命周期', () => {
  it('读取认证请求头期间切换账号时不发送旧请求', async () => {
    let epoch = 1;
    let requests = 0;
    const started = deferred();
    const token = deferred<string>();
    const origin = await serve((_req, res) => {
      requests++;
      json(res, true);
    });
    const onUnauthorized = vi.fn();
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => epoch,
        getAuthHeaders: () => {
          started.resolve();
          return token.promise.then((value) => ({
            Authorization: `Bearer ${value}`,
          }));
        },
        shouldRefresh: (error) => error.status === 401,
        refreshSession: async () => AuthRefreshResult.EXPIRED,
        onUnauthorized,
      }),
    );
    const result = expect(
      client.post<object, void>('/')({}),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    await started.promise;
    epoch = 2;
    token.resolve('account-b');
    await result;
    expect(requests).toBe(0);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it.each([200, 401])(
    '会话变化后到达的 HTTP %i 不交付旧结果也不恢复',
    async (status) => {
      const accessToken = 'a';
      let epoch = 1;
      const received = deferred();
      const respond = deferred();
      const origin = await serve(async (_req, res) => {
        received.resolve();
        await respond.promise;
        json(res, { owner: 'a' }, status);
      });
      const refreshSession = vi.fn(async () => AuthRefreshResult.REFRESHED);
      const onUnauthorized = vi.fn();
      const client = http.withBaseURL(origin).withAuth(
        createAuthSession({
          getSessionEpoch: () => epoch,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession,
          onUnauthorized,
        }),
      );
      const result = expect(
        client.get<void, unknown>('/')(),
      ).rejects.toMatchObject({
        kind: 'session-changed',
      });
      await received.promise;
      epoch = 2;
      respond.resolve();
      await result;
      expect(refreshSession).not.toHaveBeenCalled();
      expect(onUnauthorized).not.toHaveBeenCalled();
    },
  );
  it('异步响应转换期间切换会话，不向 metadata 交付旧数据', async () => {
    const accessToken = 'a';
    let epoch = 'a';
    const started = deferred();
    const transformed = deferred<unknown>();
    const origin = await serve((_req, res) => json(res, true));
    const client = http
      .withBaseURL(origin)
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => epoch,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        }),
      )
      .withResponseTransform(() => {
        started.resolve();
        return transformed.promise;
      });
    const result = expect(
      client.withMetadata().request<void, unknown>(() => ({
        method: 'GET',
        url: '/',
      }))(),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    await started.promise;
    epoch = 'b';
    transformed.resolve({ owner: 'a' });
    await result;
  });
  it('普通退避期间退出，后续发送前终止旧请求', async () => {
    const accessToken = 'a';
    let epoch = 1;
    let requests = 0;
    const deciding = deferred();
    const origin = await serve((_req, res) => {
      requests++;
      res.setHeader('Retry-After', '1');
      json(res, {}, 503);
    });
    const client = http
      .withBaseURL(origin)
      .withMaxRetries(1)
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => epoch,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          refreshSession: async () => AuthRefreshResult.EXPIRED,
          shouldRefresh: () => {
            deciding.resolve();
            return false;
          },
        }),
      );
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const result = expect(
        client.get<void, unknown>('/')(),
      ).rejects.toMatchObject({
        kind: 'session-changed',
      });
      await deciding.promise;
      epoch = 2;
      await vi.advanceTimersByTimeAsync(1000);
      await result;
      expect(requests).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });
  it('旧账号刷新完成不能重放写请求或清理新账号正在共享的刷新', async () => {
    let epoch = 'a';
    let token = 'a-old';
    const startedA = deferred();
    const startedB = deferred();
    const finishA = deferred<string>();
    const finishB = deferred<string>();
    const secondB = deferred();
    const seen: {
      url: string;
      token?: string;
    }[] = [];
    const origin = await serve((req, res) => {
      seen.push({ url: req.url!, token: req.headers.authorization });
      if (req.url === '/b2' && req.headers.authorization === 'Bearer b-old')
        secondB.resolve();
      json(
        res,
        true,
        req.headers.authorization === 'Bearer b-fresh' ? 200 : 401,
      );
    });
    const refreshSession = vi.fn(async ({ epoch: expected }) => {
      if (expected === 'a') {
        startedA.resolve();
        await finishA.promise;
        return AuthRefreshResult.REFRESHED;
      }
      startedB.resolve();
      token = await finishB.promise;
      return AuthRefreshResult.REFRESHED;
    });
    const onUnauthorized = vi.fn();
    const auth = createAuthSession({
      getSessionEpoch: () => epoch,
      getAuthHeaders: () => ({ Authorization: `Bearer ${token}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession,
      onUnauthorized,
    });
    const client = http.withBaseURL(origin).withAuth(auth);
    const old = expect(
      client.post<object, void>('/a')({ owner: 'a' }),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    await startedA.promise;
    epoch = 'b';
    token = 'b-old';
    const first = client.get<void, unknown>('/b1')();
    await startedB.promise;
    finishA.resolve('a-fresh');
    await old;
    const second = client.withTimeout(1000).get<void, unknown>('/b2')();
    await secondB.promise;
    finishB.resolve('b-fresh');
    expect(await Promise.all([first, second])).toEqual([true, true]);
    expect(refreshSession).toHaveBeenCalledTimes(2);
    expect(seen.filter((request) => request.url === '/a')).toEqual([
      { url: '/a', token: 'Bearer a-old' },
    ]);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it.each(['expired', 'network'] as const)(
    '旧刷新返回 %s 时不失效新会话',
    async (outcome) => {
      const accessToken = 'a';
      let epoch = 1;
      const started = deferred();
      const finish = deferred();
      const origin = await serve((_req, res) => json(res, {}, 401));
      const onUnauthorized = vi.fn();
      const client = http.withBaseURL(origin).withAuth(
        createAuthSession({
          getSessionEpoch: () => epoch,
          getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
          onUnauthorized,
          shouldRefresh: (error: HttpClientError) => error.status === 401,
          refreshSession: async () => {
            started.resolve();
            await finish.promise;
            if (outcome === 'network')
              throw new HttpClientError('旧刷新失败', { kind: 'network' });
            return AuthRefreshResult.EXPIRED;
          },
        }),
      );
      const result = expect(
        client.get<void, unknown>('/')(),
      ).rejects.toMatchObject({
        kind: 'session-changed',
      });
      await started.promise;
      epoch = 2;
      finish.resolve();
      await result;
      expect(onUnauthorized).not.toHaveBeenCalled();
    },
  );
  it('项目在实际保存时检查 epoch，旧刷新不能覆盖新账号凭证', async () => {
    let credentials = { epoch: 'a', token: 'a-old' };
    const waitingForStorage = deferred();
    const storageAvailable = deferred();
    const origin = await serve((_req, res) => json(res, {}, 401));
    const onUnauthorized = vi.fn();
    const auth = createAuthSession({
      getSessionEpoch: () => credentials.epoch,
      getAuthHeaders: () => ({ Authorization: `Bearer ${credentials.token}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession: async ({ epoch }) => {
        const nextToken = 'a-fresh';
        waitingForStorage.resolve();
        await storageAvailable.promise;
        // 模拟项目条件提交：异步准备完成后，在同一临界区内检查并修改。
        if (credentials.epoch !== epoch)
          throw new HttpClientError('保存所属会话已变化', {
            kind: 'session-changed',
          });
        credentials = { ...credentials, token: nextToken };
        return AuthRefreshResult.REFRESHED;
      },
      onUnauthorized,
    });
    const result = expect(
      http.withBaseURL(origin).withAuth(auth).get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    await waitingForStorage.promise;
    credentials = { epoch: 'b', token: 'b-token' };
    storageAvailable.resolve();
    await result;
    expect(credentials).toEqual({ epoch: 'b', token: 'b-token' });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
  it('项目异步退出的条件清理不能删除新账号凭证', async () => {
    let credentials = { epoch: 'a', token: 'a-old' };
    const clearing = deferred();
    const commit = deferred();
    const origin = await serve((_req, res) => json(res, {}, 401));
    const auth = createAuthSession({
      getSessionEpoch: () => credentials.epoch,
      getAuthHeaders: () => ({ Authorization: `Bearer ${credentials.token}` }),
      shouldRefresh: (error: HttpClientError) => error.status === 401,
      refreshSession: async () => AuthRefreshResult.EXPIRED,
      onUnauthorized: async (_error, { epoch }) => {
        clearing.resolve();
        await commit.promise;
        if (credentials.epoch !== epoch)
          throw new HttpClientError('清理所属会话已变化', {
            kind: 'session-changed',
          });
        credentials = { epoch: 'logged-out', token: '' };
      },
    });
    const result = expect(
      http.withBaseURL(origin).withAuth(auth).get<void, unknown>('/')(),
    ).rejects.toMatchObject({ kind: 'session-changed' });
    await clearing.promise;
    credentials = { epoch: 'b', token: 'b-token' };
    commit.resolve();
    await result;
    expect(credentials).toEqual({ epoch: 'b', token: 'b-token' });
  });
  it('退出回调可以推进 epoch，新登录不会复用旧通知状态', async () => {
    const accessToken = 'token';
    let epoch = 1;
    const origin = await serve((_req, res) => json(res, {}, 401));
    const onUnauthorized = vi.fn((_error, context) => {
      expect(context.epoch).toBe(epoch);
      epoch++;
    });
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => epoch,
        getAuthHeaders: () => ({ Authorization: `Bearer ${accessToken}` }),
        shouldRefresh: (error: HttpClientError) => error.status === 401,
        refreshSession: async () => AuthRefreshResult.EXPIRED,
        onUnauthorized,
      }),
    );
    await expect(client.get<void, unknown>('/a')()).rejects.toMatchObject({
      status: 401,
    });
    epoch = 3;
    await expect(client.get<void, unknown>('/b')()).rejects.toMatchObject({
      status: 401,
    });
    expect(onUnauthorized).toHaveBeenCalledTimes(2);
    expect(
      onUnauthorized.mock.calls.map(([, context]) => context.epoch),
    ).toEqual([1, 3]);
  });
  it('一个派生客户端取消等待，不取消另一客户端共享的刷新', async () => {
    let accessToken = 'old';
    const allOld = deferred();
    let requests = 0;
    const finish = deferred<string>();
    const origin = await serve((req, res) => {
      if (req.headers.authorization !== 'Bearer fresh') {
        requests++;
        if (requests === 2) allOld.resolve();
        json(res, {}, 401);
      } else json(res, true);
    });
    const refreshSession = vi.fn(async () => {
      accessToken = await finish.promise;
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
      client.withTimeout(1000).get<void, unknown>('/a')(undefined, {
        signal: controller.signal,
      }),
    ).rejects.toMatchObject({ kind: 'canceled' });
    const other = client.get<void, unknown>('/b')();
    await allOld.promise;
    controller.abort();
    await canceled;
    finish.resolve('fresh');
    expect(await other).toBe(true);
    expect(refreshSession).toHaveBeenCalledTimes(1);
  });
});

describe('显式认证契约与配置拒绝', () => {
  it.each([
    null,
    {},
    { getSessionEpoch: 1 },
    { getSessionEpoch: () => 1, getAuthHeaders: null },
    { getSessionEpoch: () => 1, getAccessToken: () => 'old' },
    { getSessionEpoch: () => 1, shouldRefresh: () => true },
    {
      getSessionEpoch: () => 1,
      refreshSession: async () => AuthRefreshResult.REFRESHED,
    },
    { getSessionEpoch: () => 1, onUnauthorized: () => {} },
    {
      getSessionEpoch: () => 1,
      shouldRefresh: false,
      refreshSession: async () => AuthRefreshResult.REFRESHED,
    },
    {
      getSessionEpoch: () => 1,
      shouldRefresh: () => true,
      refreshSession: null,
    },
    {
      getSessionEpoch: () => 1,
      shouldRefresh: () => true,
      refreshSession: async () => AuthRefreshResult.EXPIRED,
      onUnauthorized: false,
    },
  ] as unknown[])('无效会话配置 %j 在创建时同步拒绝', (options) => {
    expect(() => createAuthSession(options as AuthSessionOptions)).toThrow(
      TypeError,
    );
  });

  it.each([null, 'include', 1])('withCredentials 不接受 %j', (value) => {
    const session = createAuthSession({ getSessionEpoch: () => 1 });
    expect(() =>
      http.withAuth(session, { withCredentials: value as unknown as boolean }),
    ).toThrow(TypeError);
  });

  it('Node 不静默忽略主动 Cookie 携带，关闭认证后不读取会话', async () => {
    let requests = 0;
    const origin = await serve((_req, res) => {
      requests++;
      json(res, true);
    });
    const getSessionEpoch = vi.fn(() => 1);
    const client = http
      .withBaseURL(origin)
      .withAuth(createAuthSession({ getSessionEpoch }), {
        withCredentials: true,
      });
    await expect(client.get<void, boolean>('/')()).rejects.toMatchObject({
      kind: 'auth',
    });
    expect(requests).toBe(0);
    expect(getSessionEpoch).not.toHaveBeenCalled();
    expect(await client.get<void, boolean>('/', { auth: false })()).toBe(true);
    expect(getSessionEpoch).not.toHaveBeenCalled();
  });

  it.each([undefined, null, 0, 1, 'true', Promise.resolve(true)])(
    '非法谓词结果 %j 不刷新、不通知、不进行传输重试',
    async (value) => {
      let requests = 0;
      const origin = await serve((_req, res) => {
        requests++;
        json(res, {}, 503);
      });
      const refreshSession = vi.fn(async () => AuthRefreshResult.REFRESHED);
      const onUnauthorized = vi.fn();
      const client = http
        .withBaseURL(origin)
        .withMaxRetries(2)
        .withAuth(
          createAuthSession({
            getSessionEpoch: () => 1,
            shouldRefresh: () => value as unknown as boolean,
            refreshSession,
            onUnauthorized,
          }),
        );
      await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
        kind: 'auth',
        cause: expect.any(TypeError),
      });
      expect(requests).toBe(1);
      expect(refreshSession).not.toHaveBeenCalled();
      expect(onUnauthorized).not.toHaveBeenCalled();
    },
  );

  it.each([
    undefined,
    null,
    'Bearer token',
    [],
    { Authorization: false },
    { 'bad header': 'value' },
    { Authorization: 'a\r\nb' },
  ])('非法认证头 %j 不发送请求、不恢复', async (value) => {
    let requests = 0;
    const origin = await serve((_req, res) => {
      requests++;
      json(res, true);
    });
    const shouldRefresh = vi.fn(() => true);
    const onUnauthorized = vi.fn();
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => value as unknown as HttpHeaders,
        shouldRefresh,
        refreshSession: async () => AuthRefreshResult.REFRESHED,
        onUnauthorized,
      }),
    );
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'auth',
      cause: expect.any(TypeError),
    });
    expect(requests).toBe(0);
    expect(shouldRefresh).not.toHaveBeenCalled();
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('误传拒绝的异步谓词时只报告契约错误，不留下未处理的 Promise', async () => {
    const origin = await serve((_req, res) => json(res, {}, 401));
    const refreshSession = vi.fn(async () => AuthRefreshResult.REFRESHED);
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        shouldRefresh: () =>
          Promise.reject(new Error('异步判定失败')) as unknown as boolean,
        refreshSession,
      }),
    );
    await expect(client.get<void, unknown>('/')()).rejects.toMatchObject({
      kind: 'auth',
      cause: expect.any(TypeError),
    });
    expect(refreshSession).not.toHaveBeenCalled();
  });

  it('认证头优先覆盖普通配置，刷新后替换整份认证头且不重做映射', async () => {
    let headers: Record<string, string> = {
      Authorization: 'Custom old',
      'X-Old': 'old',
    };
    const seen: Record<string, unknown>[] = [];
    const origin = await serve((req, res) => {
      seen.push({
        authorization: req.headers.authorization,
        old: req.headers['x-old'],
        csrf: req.headers['x-csrf'],
        ordinary: req.headers['x-ordinary'],
      });
      json(res, true, seen.length === 1 ? 401 : 200);
    });
    const getAuthHeaders = vi.fn(async () => headers);
    const resolve = vi.fn(() => ({
      method: 'POST' as const,
      url: '/',
      data: { id: 1 },
      headers: { authorization: 'mapped' },
    }));
    const client = http
      .withBaseURL(origin)
      .withHeaders({ Authorization: 'base', 'X-Ordinary': 'kept' })
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 'a',
          getAuthHeaders,
          shouldRefresh: (error) => error.status === 401,
          refreshSession: async () => {
            headers = { authorization: 'Custom fresh', 'X-CSRF': 'fresh-csrf' };
            return AuthRefreshResult.REFRESHED;
          },
        }),
      );
    expect(
      await client.request<void, boolean>(resolve, {
        headers: { AUTHORIZATION: 'declared' },
      })(undefined, { headers: { Authorization: 'called' } }),
    ).toBe(true);
    expect(seen).toEqual([
      {
        authorization: 'Custom old',
        old: 'old',
        csrf: undefined,
        ordinary: 'kept',
      },
      {
        authorization: 'Custom fresh',
        old: undefined,
        csrf: 'fresh-csrf',
        ordinary: 'kept',
      },
    ]);
    expect(getAuthHeaders.mock.calls).toEqual([
      [{ epoch: 'a' }],
      [{ epoch: 'a' }],
    ]);
    expect(resolve).toHaveBeenCalledTimes(1);
  });

  it('刷新后的认证头读取失败不重放，也不再次判断或通知退出', async () => {
    let requests = 0;
    let reads = 0;
    const failure = new HttpClientError('读取失败', {
      kind: 'http',
      status: 401,
    });
    const origin = await serve((_req, res) => {
      requests++;
      json(res, {}, 401);
    });
    const shouldRefresh = vi.fn(() => true);
    const onUnauthorized = vi.fn();
    const client = http.withBaseURL(origin).withAuth(
      createAuthSession({
        getSessionEpoch: () => 1,
        getAuthHeaders: () => {
          if (++reads === 2) throw failure;
          return {};
        },
        shouldRefresh,
        refreshSession: async () => AuthRefreshResult.REFRESHED,
        onUnauthorized,
      }),
    );
    await expect(client.get<void, unknown>('/')()).rejects.toBe(failure);
    expect(requests).toBe(1);
    expect(shouldRefresh).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
