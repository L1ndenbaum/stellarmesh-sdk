import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import { chromium } from '@playwright/test';

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
      httpClient,
      createHttpApi,
      createAuthSession,
      flattenEnvelopeResponse,
    } = globalThis.SDK;
    const api = httpClient
      .withBaseURL('/api')
      .withAuth(
        createAuthSession({
          getSessionEpoch: () => 1,
          getAccessToken: () => 'browser-token',
        }),
      )
      .withResponseTransform(flattenEnvelopeResponse());
    const requestEcho = createHttpApi(api).post('/echo');
    const echo = await requestEcho({ id: 7 });
    const anonymousEcho = await createHttpApi(api).post('/echo', {
      auth: false,
    })({ id: 8 });
    const storageClient = httpClient.withTimeout(5000);
    const uploaded = [];
    const downloaded = [];
    const data = new Blob([new Uint8Array(256 * 1024).fill(65)], {
      type: 'application/octet-stream',
    });
    const upload = await storageClient.requestWithMetadata({
      method: 'PUT',
      url: `${storage}/object?signature=unchanged`,
      data,
      responseType: 'text',
      headers: { 'Content-Type': data.type },
      onUploadProgress: (event) => uploaded.push(event.loaded),
    });
    const blob = await storageClient.get(
      `${storage}/object?signature=unchanged`,
      {
        responseType: 'blob',
        onDownloadProgress: (event) => downloaded.push(event.loaded),
      },
    );
    const bytes = await storageClient.get(
      `${storage}/object?signature=unchanged`,
      { responseType: 'arraybuffer' },
    );
    // 带鉴权的业务实例面对 //foreign-host 时同样不得注入 Token。
    const requestExternal = createHttpApi(api).get(
      storage.replace('http:', '') + '/public',
      {
        responseType: 'text',
      },
    );
    const external = await requestExternal();
    const controller = new AbortController();
    const request = storageClient.get(`${storage}/slow`, {
      signal: controller.signal,
    });
    const cancel = request.catch((error) => error.kind);
    await new Promise((resolve) => setTimeout(resolve, 30));
    controller.abort();
    let timeout;
    try {
      await storageClient.get(`${storage}/slow`, { timeout: 20 });
    } catch (error) {
      timeout = error.kind;
    }
    return {
      echo,
      anonymousEcho,
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
  assert(stored.has('/object?signature=unchanged'));
  console.log(
    '浏览器验证通过：声明式调用、信封、鉴权、跨域隔离、上传下载、进度、ETag、超时与取消',
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
