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
import { httpClient, createHttpApi, createAuthSession, flattenEnvelopeResponse, HttpMethod, ResponseType } from 'stellarmesh-sdk';
import type { AuthSession, AuthSessionContext, HttpClient, HttpResponse, HttpMethod as HttpMethodType, ResponseType as ResponseTypeType } from 'stellarmesh-sdk';
const method: HttpMethodType = HttpMethod.PUT;
const responseType: ResponseTypeType = ResponseType.TEXT;
export const literalMethod: HttpMethod = 'GET';
export const literalResponseType: ResponseType = 'json';
const auth: AuthSession = createAuthSession({
  getSessionEpoch: () => 'request-session',
  getAccessToken: () => null,
  refreshSession: async (context: AuthSessionContext) => { void context.epoch; return null; },
  shouldRefresh: error => error.status === 401,
});
const client: HttpClient = httpClient.withAuth(auth).withResponseTransform(flattenEnvelopeResponse());
export function check(): Promise<{ id: number }> {
  return client.post<{ name: string }, { id: number }>('/items', { name: 'a' }, {authRecovery: false});
}
export function metadata(): Promise<HttpResponse<string>> {
  return client.requestWithMetadata<Blob, string>({method, responseType, url: '/', data: new Blob()});
}
interface LoginRequest { username: string; password: string }
interface Token { accessToken: string }
interface ListUsersQuery { page: number; keyword?: string }
const api = createHttpApi(client);
const requestLogin = api.post<LoginRequest, Token>('/auth/login', { auth: false });
const requestWorkspace = api.get<void, { id: number }>('/workspace');
const requestUsers = api.get<ListUsersQuery, { items: string[] }>('/users');
export function declaredTypes(): void {
  const session: Promise<Token> = requestLogin({ username: 'u', password: 'p' }, { signal: new AbortController().signal });
  const workspace: Promise<{ id: number }> = requestWorkspace();
  const users: Promise<{ items: string[] }> = requestUsers({ page: 1 });
  void [session, workspace, users];
  void requestWorkspace(undefined, { timeout: 1000 });
  void api.post<void, void>('/logout')();
  void api.head<void, void>('/health')();
  void api.delete<{ id: number }, void>('/items')({ id: 1 });
  void api.put<Blob, string>('/object', { params: { part: 1 } })(new Blob(), { params: { part: 2 } });
  void api.patch<{ name: string }, void>('/items')({ name: 'a' });
  // @ts-expect-error 有请求体时不能省略输入。
  void requestLogin();
  // @ts-expect-error 请求体必须符合声明 DTO。
  void requestLogin({ username: 'u' });
  // @ts-expect-error 响应类型由声明确定。
  const wrong: Promise<number> = requestLogin({ username: 'u', password: 'p' });
  void wrong;
  // @ts-expect-error 有查询输入时不能省略。
  void requestUsers();
  // @ts-expect-error 查询参数必须符合声明 DTO。
  void requestUsers({ page: '1' });
  // @ts-expect-error GET 配置不能重复提供 params。
  void requestUsers({ page: 1 }, { params: { page: 2 } });
  // @ts-expect-error GET 声明不能提供 params。
  api.get<ListUsersQuery, unknown>('/users', { params: { page: 1 } });
  // @ts-expect-error HEAD 和 DELETE 遵循相同的查询配置边界。
  api.head<void, void>('/health', { params: {} });
  // @ts-expect-error DELETE 调用不能从配置提供查询参数。
  void api.delete<void, void>('/items')(undefined, { params: {} });
  // @ts-expect-error signal 只能在调用阶段提供。
  api.post<LoginRequest, Token>('/login', { signal: new AbortController().signal });
  const boundSignal = { signal: new AbortController().signal };
  // @ts-expect-error 已有变量也不能在声明时绑定 signal。
  api.get<void, unknown>('/workspace', boundSignal);
  // @ts-expect-error 无输入接口的配置仍位于第二个参数。
  void requestWorkspace({ timeout: 1000 });
  // @ts-expect-error GET 输入必须是查询对象、URLSearchParams 或 void。
  api.get<string, unknown>('/users');
}
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
    import { httpClient, createHttpApi, createAuthSession, flattenEnvelopeResponse, HttpClientError, HttpMethod, ResponseType } from 'stellarmesh-sdk';
    assert.equal(HttpMethod.GET, 'GET');
    assert.equal(ResponseType.JSON, 'json');
    const auth = createAuthSession({getSessionEpoch: () => 1, getAccessToken: () => null});
    assert.equal(typeof httpClient.withAuth(auth).withTimeout(10).post, 'function');
    assert.equal(typeof httpClient.post, 'function');
    assert.notEqual(httpClient.withTimeout(10), httpClient);
    assert.equal(flattenEnvelopeResponse()({ code: 0, message: '', data: 7 }, { status: 200, headers: {} }), 7);
    assert.equal(new HttpClientError('失败', {kind: 'timeout'}).kind, 'timeout');
    const calls = [];
    const api = createHttpApi({ request: async request => { calls.push(request); return 7; } });
    const declared = api.post('/items', { auth: false });
    assert.equal(calls.length, 0);
    assert.equal(await declared({ id: 1 }), 7);
    assert.equal(calls[0].method, 'POST');
    assert.deepEqual(calls[0].data, { id: 1 });
    assert.equal(calls[0].auth, false);
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
