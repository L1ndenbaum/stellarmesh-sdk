import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import { chromium } from '@playwright/test';
import { testBrowserAuth } from './browser-auth-test.mjs';

const directory = await mkdtemp(join(tmpdir(), 'stellarmesh-browser-'));
const servers = [];
let browser;

async function serve(handler) {
  const server = createServer(handler);
  servers.push(server);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return `http://127.0.0.1:${server.address().port}`;
}

try {
  await build({
    entryPoints: ['src/index.ts'],
    bundle: true,
    format: 'iife',
    globalName: 'SDK',
    outfile: join(directory, 'sdk.js'),
    platform: 'browser',
  });
  const bundle = await readFile(join(directory, 'sdk.js'));
  const stored = new Map();
  const seenAuth = [];
  let appOrigin;
  const storageOrigin = await serve(async (req, res) => {
    res.setHeader('Access-Control-Allow-Origin', appOrigin);
    res.setHeader('Access-Control-Allow-Methods', 'GET, PUT, OPTIONS');
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type');
    res.setHeader('Access-Control-Expose-Headers', 'ETag');
    if (req.method === 'OPTIONS') {
      res.writeHead(204);
      res.end();
      return;
    }
    seenAuth.push(req.headers.authorization);
    if (req.url === '/slow') return;
    if (req.method === 'PUT') {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      stored.set(req.url, Buffer.concat(chunks));
      res.setHeader('ETag', '"browser-part"');
      res.end('');
    } else {
      res.setHeader('Content-Type', 'application/octet-stream');
      res.end(stored.get(req.url) ?? Buffer.from('external'));
    }
  });
  appOrigin = await serve(async (req, res) => {
    if (req.url === '/sdk.js') {
      res.setHeader('Content-Type', 'text/javascript');
      res.end(bundle);
      return;
    }
    if (req.url === '/api/echo') {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      res.setHeader('Content-Type', 'application/json');
      res.end(
        JSON.stringify({
          code: 0,
          message: '',
          data: {
            token: req.headers.authorization,
            payload: JSON.parse(Buffer.concat(chunks).toString()),
          },
        }),
      );
      return;
    }
    if (
      req.url === '/api/http-failure' ||
      req.url === '/api/business-failure'
    ) {
      res.writeHead(req.url === '/api/http-failure' ? 401 : 200, {
        'Content-Type': 'application/json',
        'X-Reason': 'browser',
      });
      res.end(
        JSON.stringify({
          code: 401,
          error_code: 'EXPIRED',
          message: '会话失效',
          data: null,
        }),
      );
      return;
    }
    res.setHeader('Content-Type', 'text/html');
    res.end(
      '<!doctype html><meta charset="utf-8"><script src="/sdk.js"></script>',
    );
  });
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.goto(appOrigin);
  const result = await page.evaluate(async (storage) => {
    const {
      http,
      HttpMethod,
      HttpErrorKind,
      createAuthSession,
      flattenEnvelopeResponse,
    } = globalThis.SDK;
    const api = http
      .withBaseURL('/api')
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAuthHeaders: () => ({ Authorization: 'Bearer browser-token' }),
        }),
      )
      .withResponseTransform(flattenEnvelopeResponse());
    const requestEcho = api.post('/echo');
    const echo = await requestEcho({ id: 7 });
    const anonymousEcho = await api.post('/echo', { auth: false })({ id: 8 });
    let extractions = 0;
    const errorApi = api.withErrorCodeExtractor((data, context) => {
      extractions++;
      if (context.headers['x-reason'] !== 'browser')
        throw new Error('缺少诊断头');
      return data.error_code;
    });
    const errors = [];
    for (const path of ['/http-failure', '/business-failure']) {
      try {
        await errorApi.get(path)();
        throw new Error('请求应当失败');
      } catch (error) {
        if (
          error.kind !== HttpErrorKind.HTTP &&
          error.kind !== HttpErrorKind.BUSINESS
        )
          throw error;
        errors.push({
          kind: error.kind,
          status: error.status,
          apiCode: error.apiCode,
          message: error.message,
          data: error.data,
        });
      }
    }
    const storageApi = http.withTimeout(5000);
    const uploaded = [];
    const downloaded = [];
    const data = new Blob([new Uint8Array(256 * 1024).fill(65)], {
      type: 'application/octet-stream',
    });
    const url = `${storage}/object?signature=a%2Fb%2Bc&part=1`;
    const requestUpload = storageApi.withMetadata().request(
      ({ url, file }) => ({
        method: HttpMethod.PUT,
        url,
        data: file,
        headers: { 'Content-Type': file.type },
      }),
      { responseType: 'text' },
    );
    const upload = await requestUpload(
      { url, file: data },
      { onUploadProgress: (event) => uploaded.push(event.loaded) },
    );
    const requestBlob = storageApi.request(
      (url) => ({ method: HttpMethod.GET, url }),
      { responseType: 'blob' },
    );
    const blob = await requestBlob(url, {
      onDownloadProgress: (event) => downloaded.push(event.loaded),
    });
    const bytes = await storageApi.request(
      (url) => ({ method: HttpMethod.GET, url }),
      { responseType: 'arraybuffer' },
    )(url);
    // 动态 //foreign-host 同样受可信来源限制，不能继承业务 Token。
    const requestExternal = api.request(
      (url) => ({ method: HttpMethod.GET, url }),
      { responseType: 'text' },
    );
    const external = await requestExternal(
      storage.replace('http:', '') + '/public',
    );
    const requestSlow = storageApi.get(`${storage}/slow`);
    const controller = new AbortController();
    const cancel = requestSlow(undefined, { signal: controller.signal }).catch(
      (error) => error.kind,
    );
    await new Promise((resolve) => setTimeout(resolve, 30));
    controller.abort();
    let timeout;
    try {
      await requestSlow(undefined, { timeout: 20 });
    } catch (error) {
      timeout = error.kind;
    }
    return {
      echo,
      anonymousEcho,
      errors,
      extractions,
      etag: upload.headers.etag,
      status: upload.status,
      size: blob.size,
      byteLength: bytes.byteLength,
      uploaded,
      downloaded,
      external,
      canceled: await cancel,
      timeout,
    };
  }, storageOrigin);
  assert.deepEqual(result.echo, {
    token: 'Bearer browser-token',
    payload: { id: 7 },
  });
  assert.deepEqual(result.anonymousEcho, { payload: { id: 8 } });
  assert.equal(result.extractions, 2);
  assert.deepEqual(
    result.errors,
    ['http', 'business'].map((kind) => ({
      kind,
      status: kind === 'http' ? 401 : 200,
      apiCode: 'EXPIRED',
      message: '会话失效',
      data: {
        code: 401,
        error_code: 'EXPIRED',
        message: '会话失效',
        data: null,
      },
    })),
  );
  assert.equal(result.etag, '"browser-part"');
  assert.equal(result.status, 200);
  assert.equal(result.size, 256 * 1024);
  assert.equal(result.byteLength, 256 * 1024);
  assert(result.uploaded.includes(256 * 1024));
  assert(result.downloaded.includes(256 * 1024));
  assert.equal(result.external, 'external');
  assert.equal(result.canceled, 'canceled');
  assert.equal(result.timeout, 'timeout');
  assert(seenAuth.every((value) => value === undefined));
  assert(stored.has('/object?signature=a%2Fb%2Bc&part=1'));
  await testBrowserAuth(browser, bundle);
  console.log(
    '浏览器验证通过：声明式调用、信封、错误码提取、鉴权、跨域隔离、上传下载、进度、ETag、超时与取消',
  );
} finally {
  await browser?.close();
  await Promise.all(
    servers.map(
      (server) =>
        new Promise((resolve) => {
          server.closeAllConnections();
          server.close(resolve);
        }),
    ),
  );
  await rm(directory, { recursive: true, force: true });
}
