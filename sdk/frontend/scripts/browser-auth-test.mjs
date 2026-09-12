import assert from 'node:assert/strict';
import { createServer } from 'node:http';

/** 使用真实 HttpOnly Cookie 验证会话恢复，避免用请求头替代浏览器行为。 */
export async function testBrowserAuth(browser, bundle) {
  const servers = [];
  const context = await browser.newContext();
  let appOrigin;
  async function serve() {
    const server = createServer((req, res) => {
      res.setHeader('Access-Control-Allow-Origin', appOrigin);
      res.setHeader('Access-Control-Allow-Credentials', 'true');
      res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
      res.setHeader(
        'Access-Control-Allow-Headers',
        'Content-Type, X-CSRF, X-XSRF-TOKEN',
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
      const match = /^\/(same|cross)\/(.+)$/.exec(req.url);
      if (!match) {
        res.setHeader('Content-Type', 'text/html');
        res.end('<!doctype html><script src="/sdk.js"></script>');
        return;
      }
      const [, scope, action] = match;
      const cookie =
        /(?:^|; )sdk_session=(old|fresh)(?:;|$)/.exec(
          req.headers.cookie ?? '',
        )?.[1] ?? null;
      const result = {
        cookie,
        authorization: req.headers.authorization ?? null,
        csrf: req.headers['x-csrf'] ?? null,
        xsrf: req.headers['x-xsrf-token'] ?? null,
      };
      res.setHeader('Content-Type', 'application/json');
      if (action === 'login' || action === 'refresh') {
        if (action === 'refresh' && cookie === null) {
          res.writeHead(401);
          res.end(JSON.stringify({ error: { code: 'SESSION_EXPIRED' } }));
          return;
        }
        res.setHeader(
          'Set-Cookie',
          `sdk_session=${action === 'login' ? 'old' : 'fresh'}; HttpOnly; SameSite=Lax; Path=/${scope}`,
        );
      } else if (
        (action === 'workspace' && cookie !== 'fresh') ||
        action === 'expired' ||
        action === 'denied'
      ) {
        res.writeHead(401);
        res.end(
          JSON.stringify({
            error: {
              code: action === 'denied' ? 'ACCESS_DENIED' : 'SESSION_EXPIRED',
            },
          }),
        );
        return;
      }
      res.end(JSON.stringify(result));
    });
    servers.push(server);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    return `http://127.0.0.1:${server.address().port}`;
  }
  try {
    appOrigin = await serve();
    const crossOrigin = await serve();
    const foreignOrigin = await serve();
    const page = await context.newPage();
    await page.goto(appOrigin);
    const results = await page.evaluate(
      async ({ crossOrigin, foreignOrigin }) => {
        const { http, createAuthSession, AuthRefreshResult } = globalThis.SDK;
        document.cookie = 'XSRF-TOKEN=implicit-value; Path=/; SameSite=Lax';
        const results = [];
        for (const scenario of [
          { scope: 'same', baseURL: '/same', withCredentials: false },
          {
            scope: 'cross',
            baseURL: `${crossOrigin}/cross`,
            withCredentials: true,
          },
        ]) {
          let refreshes = 0;
          let notifications = 0;
          let predicates = 0;
          // 回调在请求执行时才调用，因此可以引用随后声明的刷新接口。
          const auth = createAuthSession({
            getSessionEpoch: () => 'login-a',
            shouldRefresh: (error) => {
              predicates++;
              return (
                error.status === 401 &&
                error.data?.error?.code === 'SESSION_EXPIRED'
              );
            },
            refreshSession: async () => {
              refreshes++;
              await refresh();
              return AuthRefreshResult.REFRESHED;
            },
            onUnauthorized: () => {
              notifications++;
            },
          });
          const binding = { withCredentials: scenario.withCredentials };
          const api = http
            .withBaseURL(scenario.baseURL)
            .withAuth(auth, binding);
          const refresh = api.post('/refresh', { authRecovery: false });
          const login = api.post('/login', { authRecovery: false });
          await login();
          const before = await api.get('/echo')();
          const workspace = await api.withMetadata().get('/workspace')();
          const after = await api.get('/echo')();
          const anonymous = await api.get('/echo', { auth: false })();
          const disabled = await api
            .get('/expired', { authRecovery: false })()
            .catch((error) => error.status);
          const denied = await api
            .get('/denied')()
            .catch((error) => error.status);
          const afterRecovery = { refreshes, notifications, predicates };
          const expired = await api
            .get('/expired')()
            .catch((error) => error.status);
          const afterExpired = { refreshes, notifications, predicates };
          const csrfApi = api.withAuth(
            createAuthSession({
              getSessionEpoch: () => 'login-a',
              getAuthHeaders: () => ({
                'X-CSRF': 'explicit-csrf',
                'X-XSRF-TOKEN': 'explicit-xsrf',
              }),
            }),
            binding,
          );
          const explicit = await csrfApi.get('/echo')();
          const csrfAnonymous = await csrfApi.get('/echo', { auth: false })();
          // 同一主机的 Cookie 不按端口隔离；此处另一 origin 仍能匹配 Cookie，
          // 必须由 SDK 的可信来源判断关闭主动携带，而不能靠 Cookie Path 隐藏问题。
          const foreign = await csrfApi.get(
            `${foreignOrigin}/${scenario.scope}/echo`,
          )();
          const trustedNone = await api
            .withAuth(auth, { withCredentials: true, trustedOrigins: [] })
            .get(`${crossOrigin}/${scenario.scope}/echo`)();
          const defaultCredentials = await api
            .withAuth(auth)
            .get(`${crossOrigin}/${scenario.scope}/echo`)();
          results.push({
            scope: scenario.scope,
            before,
            workspace,
            after,
            anonymous,
            disabled,
            denied,
            afterRecovery,
            expired,
            afterExpired,
            explicit,
            csrfAnonymous,
            foreign,
            trustedNone,
            defaultCredentials,
          });
        }
        return { scenarios: results, visibleCookies: document.cookie };
      },
      { crossOrigin, foreignOrigin },
    );
    assert(!results.visibleCookies.includes('sdk_session'));
    for (const result of results.scenarios) {
      assert.equal(result.before.cookie, 'old');
      assert.equal(result.workspace.status, 200);
      assert.equal(result.workspace.data.cookie, 'fresh');
      assert.equal(result.after.cookie, 'fresh');
      assert.equal(result.after.authorization, null);
      assert.equal(result.after.xsrf, null);
      assert.equal(
        result.anonymous.cookie,
        result.scope === 'same' ? 'fresh' : null,
      );
      assert.equal(result.disabled, 401);
      assert.equal(result.denied, 401);
      assert.deepEqual(result.afterRecovery, {
        refreshes: 1,
        notifications: 0,
        predicates: 2,
      });
      assert.equal(result.expired, 401);
      assert.deepEqual(result.afterExpired, {
        refreshes: 2,
        notifications: 1,
        predicates: 4,
      });
      assert.equal(result.explicit.csrf, 'explicit-csrf');
      assert.equal(result.explicit.xsrf, 'explicit-xsrf');
      assert.equal(result.explicit.cookie, 'fresh');
      assert.equal(result.csrfAnonymous.csrf, null);
      assert.equal(result.csrfAnonymous.xsrf, null);
      assert.equal(result.foreign.cookie, null);
      assert.equal(result.foreign.csrf, null);
      assert.equal(result.trustedNone.cookie, null);
      assert.equal(result.defaultCredentials.cookie, null);
    }
    console.log(
      '浏览器会话验证通过：同源及跨源 HttpOnly Cookie、显式刷新、单次重放、认证开关、可信来源与显式 CSRF',
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
