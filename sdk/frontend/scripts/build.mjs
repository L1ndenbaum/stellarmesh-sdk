import { execFileSync } from 'node:child_process';
import { rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
// tsc 不删除已移除模块的旧输出；每次构建只保留当前源码对应的制品。
await rm(new URL('../dist', import.meta.url), { recursive: true, force: true });
execFileSync(
  process.execPath,
  ['node_modules/typescript/bin/tsc', '-p', 'tsconfig.json'],
  {
    cwd: root,
    stdio: 'inherit',
  },
);
