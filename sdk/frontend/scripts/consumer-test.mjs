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
import { httpClient, flattenEnvelopeResponse } from 'stellarmesh-sdk';
import type { HttpClient, HttpResponse } from 'stellarmesh-sdk';
const client: HttpClient = httpClient.withResponseTransform(flattenEnvelopeResponse());
export function check(): Promise<{ id: number }> {
  return client.post<{ name: string }, { id: number }>('/items', { name: 'a' });
}
export function metadata(): Promise<HttpResponse<string>> {
  return client.requestWithMetadata<Blob, string>({method: 'PUT', url: '/', data: new Blob()});
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
    import { httpClient, flattenEnvelopeResponse, HttpClientError } from 'stellarmesh-sdk';
    assert.equal(typeof httpClient.post, 'function');
    assert.notEqual(httpClient.withTimeout(10), httpClient);
    assert.equal(flattenEnvelopeResponse()({ code: 0, message: '', data: 7 }, { status: 200, headers: {} }), 7);
    assert.equal(new HttpClientError('失败', {kind: 'timeout'}).kind, 'timeout');
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
