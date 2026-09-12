import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  appendFile,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const pkg = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
const lock = JSON.parse(
  await readFile(join(root, 'package-lock.json'), 'utf8'),
);
assert(
  process.argv.length <= 3,
  '用法：prepare-release.mjs [sdk/frontend/vX.Y.Z]',
);
assert.equal(pkg.name, '@l1ndenbaum/stellarmesh-sdk');
assert.equal(pkg.license, 'MIT');
assert.match(
  pkg.version,
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/,
  '当前发布准备仅接受正式版本号',
);
assert.equal(lock.name, pkg.name);
assert.equal(lock.packages[''].name, pkg.name);
assert.equal(lock.version, pkg.version);
assert.equal(lock.packages[''].version, pkg.version);
assert.equal(lock.packages[''].license, pkg.license);
assert.deepEqual(pkg.publishConfig, {
  registry: 'https://registry.npmjs.org/',
  access: 'public',
});
if (process.argv[2]) {
  assert.equal(
    process.argv[2],
    `sdk/frontend/v${pkg.version}`,
    '组件 tag 与包版本必须一致',
  );
}
for (const script of ['check', 'test', 'build', 'test:browser']) {
  execFileSync('npm', ['run', script], { cwd: root, stdio: 'inherit' });
}
const artifacts = join(root, '.artifacts');
await mkdir(artifacts, { recursive: true });
const directory = await mkdtemp(join(artifacts, `${pkg.version}-`));
try {
  // build 已经完成，打包阶段不再次运行 prepack，以免替换刚验证的构建。
  const [packed] = JSON.parse(
    execFileSync(
      'npm',
      ['pack', '--ignore-scripts', '--json', '--pack-destination', directory],
      { cwd: root, encoding: 'utf8' },
    ),
  );
  const tarball = join(directory, packed.filename);
  execFileSync(process.execPath, ['scripts/consumer-test.mjs', tarball], {
    cwd: root,
    stdio: 'inherit',
  });
  const bytes = await readFile(tarball);
  const integrity = `sha512-${createHash('sha512').update(bytes).digest('base64')}`;
  assert.equal(integrity, packed.integrity);
  const manifest = {
    name: pkg.name,
    version: pkg.version,
    filename: packed.filename,
    integrity,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    registry: pkg.publishConfig.registry,
    access: pkg.publishConfig.access,
    tag: 'latest',
  };
  await writeFile(
    join(directory, 'release.json'),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  // CI 只上传这次完整验证的目录；本地重复准备会生成新目录，不覆盖旧制品。
  if (process.env.GITHUB_OUTPUT) {
    await appendFile(process.env.GITHUB_OUTPUT, `directory=${directory}\n`);
  }
  console.log(`发布准备完成（未发布）：${tarball}`);
  console.log(`制品校验信息：${join(directory, 'release.json')}`);
} catch (error) {
  await rm(directory, { recursive: true, force: true });
  throw error;
}
