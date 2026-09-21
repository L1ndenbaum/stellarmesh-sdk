import assert from 'node:assert/strict';
import { createServer } from 'node:http';

/** 真实浏览器验证分块交付、断开连接和 Cookie/CORS，协议边界另由单元测试覆盖。 */
export async function testBrowserSse(browser, bundle) {
  const servers = [];
  const context = await browser.newContext();
  let appOrigin;
  let foreignOrigin;
  let progressive;
  let closed;
  const disconnected = new Promise((resolve) => {
    closed = resolve;
  });
  let redirectTargets = 0;
  const requests = [];
  async function serve() {
    const server = createServer((req, res) => {
      res.setHeader('Access-Control-Allow-Origin', appOrigin);
      res.setHeader('Access-Control-Allow-Credentials', 'true');
      res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
      res.setHeader(
        'Access-Control-Allow-Headers',
        'Content-Type, X-CSRF, Authorization',
      );
      if (req.method === 'OPTIONS') {
        res.writeHead(204);
        res.end();
        return;
      }
      if (req.url === '/sdk.js') {
        res.setHeader('Content-Type', 'text/javascript');
        res.end(bundle);
        return;
      }
      if (req.url === '/') {
        res.end('<!doctype html><script src="/sdk.js"></script>');
        return;
      }
      if (req.url === '/api/progressive') {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.write('data: first\n\n');
        progressive = res;
        return;
      }
      if (req.url === '/release') {
        progressive.end('data: second\n\n');
        res.end();
        return;
      }
      if (req.url === '/api/disconnect') {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.write('data: first\n\n');
        res.on('close', closed);
        return;
      }
      if (req.url === '/api/redirect') {
        res.writeHead(307, { Location: `${foreignOrigin}/redirect-target` });
        res.end();
        return;
      }
      if (req.url === '/redirect-target') redirectTargets++;
      const [, scope, action] = /^\/(same|cross)\/(.+)$/.exec(req.url) ?? [];
      if (!scope) {
        res.writeHead(404);
        res.end();
        return;
      }
      const cookie =
        /(?:^|; )sse_session=(old|fresh)(?:;|$)/.exec(
          req.headers.cookie ?? '',
        )?.[1] ?? null;
      const seen = {
        cookie,
        csrf: req.headers['x-csrf'] ?? null,
        xsrf: req.headers['x-xsrf-token'] ?? null,
        authorization: req.headers.authorization ?? null,
      };
      requests.push({ scope, action, ...seen });
      if (action === 'login' || action === 'refresh') {
        res.setHeader(
          'Set-Cookie',
          `sse_session=${action === 'login' ? 'old' : 'fresh'}; HttpOnly; SameSite=Lax; Path=/${scope}`,
        );
        res.setHeader('Content-Type', 'application/json');
        res.end('{}');
        return;
      }
      if (action === 'stream' && cookie !== 'fresh') {
        res.writeHead(401, { 'Content-Type': 'application/json' });
        res.end('{"error_code":"EXPIRED"}');
        return;
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(`data: ${JSON.stringify(seen)}\n\n`);
    });
    servers.push(server);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    return `http://127.0.0.1:${server.address().port}`;
  }
  try {
    appOrigin = await serve();
    const crossOrigin = await serve();
    foreignOrigin = await serve();
    const page = await context.newPage();
    await page.goto(appOrigin);
    const result = await page.evaluate(
      async ({ crossOrigin, foreignOrigin }) => {
        const { http, createAuthSession, AuthRefreshResult } = globalThis.SDK;
        const api = http.withBaseURL('/api');
        const messages = [];
        for await (const message of api.sse.get('/progressive')()) {
          messages.push(message.data);
          // 只有前端已收到首条事件，测试服务才发送后续数据并结束响应。
          if (messages.length === 1) await fetch('/release');
        }
        for await (const message of api.sse.get('/disconnect')()) {
          if (message.data !== 'first') throw new Error('首条事件错误');
          break;
        }
        let redirectError;
        try {
          for await (const message of api.sse.post('/redirect')({ run: true }))
            void message;
        } catch (error) {
          redirectError = error.kind;
        }
        document.cookie = 'XSRF-TOKEN=implicit; Path=/; SameSite=Lax';
        const results = [];
        for (const scope of ['same', 'cross']) {
          let refreshes = 0;
          let predicates = 0;
          let explicitHeaders = false;
          const auth = createAuthSession({
            getSessionEpoch: () => 1,
            getAuthHeaders: () =>
              explicitHeaders ? { 'X-CSRF': 'explicit' } : {},
            shouldRefresh: (error) => {
              predicates++;
              return (
                error.status === 401 && error.data.error_code === 'EXPIRED'
              );
            },
            refreshSession: async () => {
              refreshes++;
              await refresh();
              return AuthRefreshResult.REFRESHED;
            },
          });
          const client = http
            .withBaseURL(scope === 'same' ? '/same' : `${crossOrigin}/cross`)
            .withAuth(auth, { withCredentials: scope === 'cross' });
          const login = client.post('/login', { authRecovery: false });
          const refresh = client.post('/refresh', { authRecovery: false });
          async function read(request) {
            const received = [];
            for await (const message of request)
              received.push(JSON.parse(message.data));
            return received;
          }
          await login();
          let disabledError;
          try {
            await read(client.sse.post('/stream', { authRecovery: false })({}));
          } catch (error) {
            disabledError = error.status;
          }
          const recovered = await read(client.sse.post('/stream')({}));
          explicitHeaders = true;
          const csrf = await read(client.sse.get('/inspect')());
          const anonymous = await read(
            client.sse.get('/inspect', {
              auth: false,
              headers: { Authorization: 'manual' },
            })(),
          );
          const foreign = await read(
            client.sse.get(`${foreignOrigin}/${scope}/inspect`)(),
          );
          results.push({
            scope,
            disabledError,
            recovered,
            csrf,
            anonymous,
            foreign,
            refreshes,
            predicates,
          });
        }
        return { messages, redirectError, results };
      },
      { crossOrigin, foreignOrigin },
    );
    assert.deepEqual(result.messages, ['first', 'second']);
    assert.equal(result.redirectError, 'network');
    assert.equal(redirectTargets, 0);
    await Promise.race([
      disconnected,
      new Promise((_, reject) => {
        const timer = setTimeout(
          () => reject(new Error('提前退出后服务端连接未关闭')),
          3000,
        );
        timer.unref();
      }),
    ]);
    for (const entry of result.results) {
      assert.equal(entry.disabledError, 401);
      assert.equal(entry.refreshes, 1);
      assert.equal(entry.predicates, 1);
      assert.deepEqual(entry.recovered, [
        { cookie: 'fresh', csrf: null, xsrf: null, authorization: null },
      ]);
      assert.equal(entry.csrf[0].csrf, 'explicit');
      assert.equal(entry.csrf[0].xsrf, null);
      assert.deepEqual(entry.anonymous, [
        {
          cookie: entry.scope === 'same' ? 'fresh' : null,
          csrf: null,
          xsrf: null,
          authorization: 'manual',
        },
      ]);
      assert.deepEqual(entry.foreign, [
        { cookie: null, csrf: null, xsrf: null, authorization: null },
      ]);
    }
    assert.equal(
      requests.filter((entry) => entry.action === 'stream').length,
      6,
    );
    console.log(
      'SSE 浏览器验证通过：渐进接收、提前断开、禁止重定向、同源/跨源 Cookie 恢复与可信来源隔离',
    );
  } finally {
    await context.close();
    await Promise.all(
      servers.map(
        (server) =>
          new Promise((resolve) => {
            server.closeAllConnections();
            server.close(resolve);
          }),
      ),
    );
  }
}
