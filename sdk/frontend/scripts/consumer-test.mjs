import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, writeFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const expected = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
assert(process.argv.length <= 3, '用法：consumer-test.mjs [本地 tarball 路径]');
const supplied = process.argv[2] ? resolve(process.argv[2]) : undefined;
if (supplied) {
  assert(
    supplied.endsWith('.tgz') && (await stat(supplied)).isFile(),
    '需要本地 .tgz 文件',
  );
}
const directory = await mkdtemp(join(tmpdir(), 'stellarmesh-consumer-'));
try {
  // 外部制品只读取，不触发源码构建或重新打包；默认模式仍验证当前源码打包结果。
  const [packed] = JSON.parse(
    execFileSync(
      'npm',
      supplied
        ? ['pack', supplied, '--dry-run', '--ignore-scripts', '--json']
        : ['pack', '--json', '--pack-destination', directory],
      { cwd: root, encoding: 'utf8' },
    ),
  );
  const tarball = supplied ?? join(directory, packed.filename);
  const digest = () =>
    readFile(tarball).then(
      (bytes) =>
        `sha512-${createHash('sha512').update(bytes).digest('base64')}`,
    );
  const integrity = await digest();
  assert.equal(integrity, packed.integrity);
  assert.equal(packed.name, expected.name);
  assert.equal(packed.version, expected.version);
  const files = packed.files.map((file) => file.path);
  for (const file of [
    'dist/index.js',
    'dist/index.d.ts',
    'package.json',
    'README.md',
    'LICENSE',
  ]) {
    assert(files.includes(file), `发布制品缺少 ${file}`);
  }
  assert(
    files.every(
      (file) =>
        ['package.json', 'README.md', 'LICENSE'].includes(file) ||
        (file.startsWith('dist/') && /\.(js|d\.ts)$/.test(file)),
    ),
    '制品包含非发布文件',
  );
  await writeFile(
    join(directory, 'package.json'),
    JSON.stringify({ private: true, type: 'module' }),
  );
  execFileSync(
    'npm',
    [
      'install',
      '--ignore-scripts',
      '--no-audit',
      '--no-fund',
      '--registry=https://registry.npmjs.org/',
      tarball,
    ],
    { cwd: directory, stdio: 'inherit' },
  );
  const installedRoot = join(directory, 'node_modules', expected.name);
  const installed = JSON.parse(
    await readFile(join(installedRoot, 'package.json'), 'utf8'),
  );
  assert.equal(installed.name, '@stellarmesh/sdk');
  assert.equal(installed.version, expected.version);
  assert.equal(installed.license, 'MIT');
  assert.deepEqual(installed.publishConfig, {
    registry: 'https://registry.npmjs.org/',
    access: 'public',
  });
  assert.deepEqual(installed.repository, expected.repository);
  assert.deepEqual(installed.exports, expected.exports);
  assert.deepEqual(installed.dependencies, expected.dependencies);
  assert.equal(
    await readFile(join(installedRoot, 'LICENSE'), 'utf8'),
    await readFile(join(root, 'LICENSE'), 'utf8'),
  );
  await writeFile(
    join(directory, 'consumer.ts'),
    `
import * as SDK from '@stellarmesh/sdk';
import { http, createAuthSession, AuthRefreshResult, flattenEnvelopeResponse, HttpMethod, ResponseType, HttpErrorKind } from '@stellarmesh/sdk';
import type { HttpApi, HttpResponse, HttpApiRequestDescriptor, ApiEnvelope, ErrorCodeExtractor, HttpMethod as MethodType, HttpErrorKind as ErrorKind } from '@stellarmesh/sdk';
export const httpErrorKind: ErrorKind = HttpErrorKind.HTTP;
export const literalErrorKind: ErrorKind = 'http';
export const exactErrorKind: 'http' = HttpErrorKind.HTTP;
// @ts-expect-error 不接受未定义的错误类别
export const invalidErrorKind: ErrorKind = 'invalid';
// @ts-expect-error 常量成员只读
HttpErrorKind.HTTP = 'business';
interface LoginRequest { username: string; password: string }
interface Token { accessToken: string }
interface Query { page: number; keyword?: string }
const auth = createAuthSession({ getSessionEpoch: () => 1 });
const api: HttpApi = http.withAuth(auth).withResponseTransform(flattenEnvelopeResponse());
const extractErrorCode: ErrorCodeExtractor = (data, context) => {
  const status: number = context.status;
  // @ts-expect-error 提取上下文只读。
  context.status = 200;
  if (!data || typeof data !== 'object') return null;
  const value = (data as Record<string, unknown>).error_code;
  return typeof value === 'string' || typeof value === 'number' ? value : undefined;
};
const custom: HttpApi = api.withErrorCodeExtractor(extractErrorCode);
const customMetadata: HttpApi<true> = custom.withMetadata().withErrorCodeExtractor(extractErrorCode);
const login = api.post<LoginRequest, Token>('/login', { auth: false });
const workspace = api.get<void, { id: number }>('/workspace');
const users = api.get<Query, string[]>('/users');
const metadata = api.withMetadata().withTimeout(1000).withHeaders({ X: 'value' }).withMetadata();
const upload = metadata.request<{ url: string; file: Blob }, string>(input => ({
  method: HttpMethod.PUT, url: input.url, data: input.file,
}), { responseType: ResponseType.TEXT });
export const literalMethod: MethodType = 'GET';
export function checkTypes(): void {
  // @ts-expect-error 提取器必须同步，不能返回 Promise。
  http.withErrorCodeExtractor(async () => 'EXPIRED');
  // @ts-expect-error 不接受布尔错误码。
  http.withErrorCodeExtractor(() => false);
  // @ts-expect-error 不接受对象错误码。
  http.withErrorCodeExtractor(() => ({ code: 'EXPIRED' }));
  // @ts-expect-error 不提供单次请求提取覆盖。
  void custom.get<void, unknown>('/') (undefined, { errorCodeExtractor: extractErrorCode });
  const extractedMetadata: Promise<HttpResponse<string>> = customMetadata.get<void, string>('/')();
  void extractedMetadata;
  let accessToken: string | null = null;
  createAuthSession({
    getSessionEpoch: () => 1,
    getAuthHeaders: (): SDK.HttpHeaders => accessToken ? { Authorization: 'Bearer ' + accessToken } : {},
  });
  accessToken = 'fresh';
  const recovery = {
    shouldRefresh: (error: SDK.HttpClientError) => error.status === 401,
    refreshSession: async () => AuthRefreshResult.REFRESHED,
  };
  createAuthSession({ getSessionEpoch: () => 1, ...recovery });
  createAuthSession({
    getSessionEpoch: () => 'login-a',
    getAuthHeaders: async ({ epoch }) => ({ Authorization: 'Bearer ' + epoch }),
    ...recovery,
    onUnauthorized: (_error: SDK.HttpClientError, { epoch }: SDK.AuthSessionContext) => { void epoch; },
  });
  http.withAuth(auth, { withCredentials: true, trustedOrigins: ['https://api.example.test'] });
  const outcome: SDK.AuthRefreshResult = AuthRefreshResult.EXPIRED;
  void outcome;
  const incomplete = { getSessionEpoch: () => 1, shouldRefresh: recovery.shouldRefresh };
  // @ts-expect-error 刷新判断不能单独配置，通过变量传入也必须拒绝。
  createAuthSession(incomplete);
  // @ts-expect-error 刷新执行不能单独配置。
  createAuthSession({ getSessionEpoch: () => 1, refreshSession: recovery.refreshSession });
  // @ts-expect-error 未启用刷新时不能配置退出回调。
  createAuthSession({ getSessionEpoch: () => 1, onUnauthorized: () => {} });
  // @ts-expect-error 旧 Token 读取契约已移除。
  createAuthSession({ getSessionEpoch: () => 1, getAccessToken: () => 'old' });
  // @ts-expect-error 刷新结果不能是 Token 字符串。
  createAuthSession({ getSessionEpoch: () => 1, shouldRefresh: recovery.shouldRefresh, refreshSession: async () => 'token' });
  // @ts-expect-error 刷新结果不能是 null。
  createAuthSession({ getSessionEpoch: () => 1, shouldRefresh: recovery.shouldRefresh, refreshSession: async () => null });
  // @ts-expect-error 判断必须同步返回布尔值。
  createAuthSession({ getSessionEpoch: () => 1, refreshSession: recovery.refreshSession, shouldRefresh: async () => true });
  // @ts-expect-error 认证头不能返回 Token 字符串。
  createAuthSession({ getSessionEpoch: () => 1, getAuthHeaders: () => 'token' });
  // @ts-expect-error Cookie 配置必须为布尔值。
  http.withAuth(auth, { withCredentials: 'include' });

  const token: Promise<Token> = login({ username: 'u', password: 'p' });
  const plain: Promise<{ id: number }> = workspace();
  const info: Promise<HttpResponse<{ id: number }>> = metadata.get<void, { id: number }>('/workspace')();
  const objectInfo: Promise<HttpResponse<string>> = upload({ url: '/object', file: new Blob() });
  const list: Promise<string[]> = users({ page: 1 });
  const envelope: Promise<ApiEnvelope<Token>> = http.post<LoginRequest, ApiEnvelope<Token>>('/login')({ username: 'u', password: 'p' });
  void [token, plain, info, objectInfo, list, envelope];
  void workspace(undefined, { signal: new AbortController().signal });
  void api.head<void, void>('/health')();
  void api.delete<Query, void>('/users')({ page: 1 });
  void api.put<Blob, string>('/object', { params: { part: 1 } })(new Blob(), { params: { part: 2 } });
  void api.patch<{ name: string }, void>('/users')({ name: 'a' });
  void api.post<void, void>('/logout')();
  const remove = api.request<{ id: string; reason: string }, void>(input => ({ method: HttpMethod.DELETE, url: '/users/' + input.id, data: { reason: input.reason } }));
  void remove({ id: 'a', reason: 'duplicate' });
  // @ts-expect-error 原立即调用根对象已移除。
  void SDK.httpClient;
  // @ts-expect-error 原适配工厂已移除。
  void SDK.createHttpApi;
  // @ts-expect-error 原立即 metadata 方法已移除。
  void http.requestWithMetadata;
  // @ts-expect-error 方法声明返回函数而不是 Promise。
  const immediate: Promise<Token> = api.post<LoginRequest, Token>('/login');
  void immediate;
  // @ts-expect-error 不支持旧请求对象立即发送语法。
  api.request({ method: HttpMethod.GET, url: '/' });
  // @ts-expect-error 有输入时必须提供参数。
  void login();
  // @ts-expect-error 请求体必须匹配 DTO。
  void login({ username: 'u' });
  // @ts-expect-error 响应类型由声明决定。
  const wrong: Promise<number> = login({ username: 'u', password: 'p' });
  void wrong;
  // @ts-expect-error metadata 返回 HTTP 信息，不是 DTO。
  const wrongMetadata: Promise<{ id: number }> = metadata.get<void, { id: number }>('/')();
  void wrongMetadata;
  // @ts-expect-error 普通模式不自动包装 metadata。
  const wrongPlain: Promise<HttpResponse<{ id: number }>> = workspace();
  void wrongPlain;
  // @ts-expect-error 查询输入不能省略。
  void users();
  // @ts-expect-error 查询必须匹配 DTO。
  void users({ page: '1' });
  // @ts-expect-error 查询配置不接受 params。
  void users({ page: 1 }, { params: {} });
  // @ts-expect-error 查询声明不接受 params。
  api.get<Query, unknown>('/users', { params: {} });
  // @ts-expect-error 通用声明配置不接受 params。
  api.request<void, unknown>(() => ({ method: HttpMethod.GET, url: '/' }), { params: {} });
  // @ts-expect-error 通用调用的查询只来自映射。
  void upload({ url: '/', file: new Blob() }, { params: {} });
  // @ts-expect-error 通用输入必须匹配映射。
  void upload({ url: '/' });
  // @ts-expect-error 映射必须同步返回描述。
  api.request<void, unknown>(async () => ({ method: HttpMethod.GET, url: '/' }));
  // @ts-expect-error 描述不能提供取消信号。
  const bound: HttpApiRequestDescriptor = { method: HttpMethod.GET, url: '/', signal: new AbortController().signal };
  void bound;
  const signalOptions = { signal: new AbortController().signal };
  // @ts-expect-error 声明不能绑定 signal，即使通过变量传入。
  api.post<LoginRequest, Token>('/login', signalOptions);
  // @ts-expect-error 单次配置必须放在第二个参数。
  void workspace({ timeout: 1000 });
}
// @ts-expect-error 旧立即调用类型不再导出。
export type RemovedClient = SDK.HttpClient;
// @ts-expect-error 旧配置客户端类型不再导出。
export type RemovedConfigurableClient = SDK.ConfigurableHttpClient;
// @ts-expect-error 旧立即 body 方法类型不再导出。
export type RemovedBodyMethod = SDK.HttpBodyMethod;
`,
  );
  execFileSync(
    process.execPath,
    [
      join(root, 'node_modules/typescript/bin/tsc'),
      '--strict',
      '--noEmit',
      '--skipLibCheck',
      '--target',
      'ES2022',
      '--module',
      'NodeNext',
      '--moduleResolution',
      'NodeNext',
      'consumer.ts',
    ],
    { cwd: directory, stdio: 'inherit' },
  );
  execFileSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import assert from 'node:assert/strict';
    import { createServer } from 'node:http';
    import * as SDK from '@stellarmesh/sdk';
    const { http, createAuthSession, flattenEnvelopeResponse, HttpClientError, HttpMethod, ResponseType, HttpErrorKind } = SDK;
    assert.equal(SDK.AuthRefreshResult.REFRESHED, 'refreshed');
    assert.equal(SDK.AuthRefreshResult.EXPIRED, 'expired');
    assert.deepEqual(HttpErrorKind, {
      HTTP: 'http', BUSINESS: 'business', NETWORK: 'network', TIMEOUT: 'timeout',
      CANCELED: 'canceled', RESPONSE_FORMAT: 'response-format', AUTH: 'auth',
      SESSION_CHANGED: 'session-changed', UNKNOWN: 'unknown',
    });
    assert.equal(new HttpClientError('失败', { kind: HttpErrorKind.HTTP }).kind, 'http');
    assert.equal(HttpMethod.GET, 'GET');
    assert.equal(ResponseType.JSON, 'json');
    assert.equal('httpClient' in SDK, false);
    assert.equal('createHttpApi' in SDK, false);
    assert.equal('requestWithMetadata' in http, false);
    const auth = createAuthSession({ getSessionEpoch: () => 1 });
    assert.notEqual(http.withAuth(auth), http);
    assert.equal(flattenEnvelopeResponse()({ code: 0, message: '', data: 7 }, { status: 200, headers: {} }), 7);
    assert.equal(new HttpClientError('失败', { kind: 'timeout' }).kind, 'timeout');
    let calls = 0;
    const server = createServer((req, res) => {
      calls++;
      if (req.url === '/failure') {
        res.writeHead(401, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ code: 401, error_code: 'EXPIRED', message: '会话失效', data: null }));
        return;
      }
      res.end('{"id":7}');
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    try {
      const api = http.withBaseURL('http://127.0.0.1:' + server.address().port);
      const declared = api.get('/items');
      assert.equal(calls, 0);
      assert.deepEqual(await declared(), { id: 7 });
      assert.deepEqual((await api.withMetadata().request(() => ({ method: HttpMethod.GET, url: '/items' }))()).data, { id: 7 });
      assert.equal(calls, 2);
      const custom = api.withErrorCodeExtractor(data => data.error_code).withResponseTransform(flattenEnvelopeResponse());
      assert.notEqual(custom, api);
      await assert.rejects(custom.get('/failure')(), error => {
        assert.equal(error.kind, HttpErrorKind.HTTP);
        assert.equal(error.status, 401);
        assert.equal(error.apiCode, 'EXPIRED');
        assert.equal(error.message, '会话失效');
        return true;
      });
      await assert.rejects(api.get('/failure')(), error => error.apiCode === 401);
    } finally {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  `,
    ],
    { cwd: directory, stdio: 'inherit' },
  );
  assert.equal(await digest(), integrity, '验证过程中 tarball 不应变化');
  console.log(
    '隔离 tarball 消费验证通过：发布文件、ESM 导入与 TypeScript 公开类型',
  );
} finally {
  await rm(directory, { recursive: true, force: true });
}
