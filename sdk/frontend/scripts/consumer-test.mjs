import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const directory = await mkdtemp(join(tmpdir(), 'stellarmesh-consumer-'));
try {
  const result = JSON.parse(
    execFileSync('npm', ['pack', '--json', '--pack-destination', directory], {
      encoding: 'utf8',
    }),
  );
  const files = result[0].files.map((file) => file.path);
  assert(files.includes('dist/index.js'));
  assert(files.includes('dist/index.d.ts'));
  assert(
    files.every(
      (file) =>
        file === 'package.json' ||
        file === 'README.md' ||
        file.startsWith('dist/'),
    ),
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
      join(directory, result[0].filename),
    ],
    { cwd: directory, stdio: 'pipe' },
  );
  await writeFile(
    join(directory, 'consumer.ts'),
    `
import * as SDK from 'stellarmesh-sdk';
import { http, createAuthSession, flattenEnvelopeResponse, HttpMethod, ResponseType } from 'stellarmesh-sdk';
import type { HttpApi, HttpResponse, HttpApiRequestDescriptor, ApiEnvelope, HttpMethod as MethodType } from 'stellarmesh-sdk';
interface LoginRequest { username: string; password: string }
interface Token { accessToken: string }
interface Query { page: number; keyword?: string }
const auth = createAuthSession({ getSessionEpoch: () => 1, getAccessToken: () => null });
const api: HttpApi = http.withAuth(auth).withResponseTransform(flattenEnvelopeResponse());
const login = api.post<LoginRequest, Token>('/login', { auth: false });
const workspace = api.get<void, { id: number }>('/workspace');
const users = api.get<Query, string[]>('/users');
const metadata = api.withMetadata().withTimeout(1000).withHeaders({ X: 'value' }).withMetadata();
const upload = metadata.request<{ url: string; file: Blob }, string>(input => ({
  method: HttpMethod.PUT, url: input.url, data: input.file,
}), { responseType: ResponseType.TEXT });
export const literalMethod: MethodType = 'GET';
export function checkTypes(): void {
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
      resolve('node_modules/typescript/bin/tsc'),
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
    { cwd: directory, stdio: 'pipe' },
  );
  execFileSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import assert from 'node:assert/strict';
    import { createServer } from 'node:http';
    import * as SDK from 'stellarmesh-sdk';
    const { http, createAuthSession, flattenEnvelopeResponse, HttpClientError, HttpMethod, ResponseType } = SDK;
    assert.equal(HttpMethod.GET, 'GET');
    assert.equal(ResponseType.JSON, 'json');
    assert.equal('httpClient' in SDK, false);
    assert.equal('createHttpApi' in SDK, false);
    assert.equal('requestWithMetadata' in http, false);
    const auth = createAuthSession({ getSessionEpoch: () => 1, getAccessToken: () => null });
    assert.notEqual(http.withAuth(auth), http);
    assert.equal(flattenEnvelopeResponse()({ code: 0, message: '', data: 7 }, { status: 200, headers: {} }), 7);
    assert.equal(new HttpClientError('失败', { kind: 'timeout' }).kind, 'timeout');
    let calls = 0;
    const server = createServer((_req, res) => { calls++; res.end('{"id":7}'); });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    try {
      const api = http.withBaseURL('http://127.0.0.1:' + server.address().port);
      const declared = api.get('/items');
      assert.equal(calls, 0);
      assert.deepEqual(await declared(), { id: 7 });
      assert.deepEqual((await api.withMetadata().request(() => ({ method: HttpMethod.GET, url: '/items' }))()).data, { id: 7 });
      assert.equal(calls, 2);
    } finally {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  `,
    ],
    { cwd: directory, stdio: 'pipe' },
  );
  console.log(
    '隔离 tarball 消费验证通过：发布文件、ESM 导入与 TypeScript 公开类型',
  );
} finally {
  await rm(directory, { recursive: true, force: true });
}
