import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { copyFile, mkdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import ts from 'typescript';

/** 验证真实安装包的 IDE 注释与文档源码；不构建或替换传入制品。 */
export async function verifyExamples(root, directory, installedRoot) {
  const source = ts.createSourceFile(
    'index.d.ts',
    await readFile(join(installedRoot, 'dist/index.d.ts'), 'utf8'),
    ts.ScriptTarget.Latest,
    true,
  );
  const documented = new Map();
  function visit(node) {
    if (node.name && ts.isIdentifier(node.name) && node.jsDoc?.length) {
      documented.set(
        node.name.text,
        node.jsDoc.map((doc) => doc.getText()).join('\n'),
      );
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
  for (const [name, text] of [
    ['withMetadata', '转换后的'],
    ['withTimeout', '毫秒'],
    ['SseRequest', '单次消费'],
    ['createAuthSession', 'TypeError'],
    ['shouldRefresh', '401'],
  ]) {
    assert(
      documented.get(name)?.includes(text),
      `${name} 的公共说明应保留在声明制品中`,
    );
  }
  await mkdir(join(directory, 'examples'));
  const examples = ['quickstart.ts', 'auth-and-sse.ts'];
  for (const file of examples) {
    await copyFile(
      join(root, 'examples', file),
      join(directory, 'examples', file),
    );
  }
  for (const [module, resolution] of [
    ['NodeNext', 'NodeNext'],
    ['ESNext', 'Bundler'],
  ]) {
    execFileSync(
      process.execPath,
      [
        join(root, 'node_modules/typescript/bin/tsc'),
        '--strict',
        '--noEmit',
        '--skipLibCheck',
        'false',
        '--target',
        'ES2022',
        '--module',
        module,
        '--moduleResolution',
        resolution,
        ...examples.map((file) => `examples/${file}`),
      ],
      { cwd: directory, stdio: 'inherit' },
    );
  }
  execFileSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import assert from 'node:assert/strict';
    import { createServer } from 'node:http';
    import { listItems } from './examples/quickstart.ts';
    import { readEvents } from './examples/auth-and-sse.ts';
    let refreshes = 0;
    let streams = 0;
    const server = createServer((req, res) => {
      if (req.url === '/items') {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ items: ['示例'] }));
      } else if (req.url === '/refresh' && req.method === 'POST') {
        refreshes++;
        res.end(JSON.stringify({ token: 'current-example-token' }));
      } else if (req.url === '/events') {
        streams++;
        if (req.headers.authorization !== 'Bearer current-example-token') {
          res.writeHead(401, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ message: '示例会话已过期' }));
          return;
        }
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.end('data: 片段\\n\\nevent: completed\\ndata: 完成\\n\\n');
      } else {
        res.writeHead(404).end();
      }
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    try {
      const baseURL = 'http://127.0.0.1:' + server.address().port;
      assert.deepEqual(await listItems(baseURL), ['示例']);
      assert.deepEqual(await readEvents(baseURL), ['片段']);
      assert.equal(refreshes, 1);
      assert.equal(streams, 2);
    } finally {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  `,
    ],
    { cwd: directory, stdio: 'inherit' },
  );
}
