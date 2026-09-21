import { defineConfig } from 'tsdown';

export default defineConfig({
  entry: ['src/index.ts'],
  format: 'esm',
  platform: 'neutral',
  target: 'es2022',
  outDir: 'dist',
  fixedExtension: false,
  dts: true,
  clean: true,
  minify: false,
  sourcemap: false,
  // 保留包导入，由消费者选择 Axios 的浏览器或 Node 实现。
  deps: { neverBundle: ['axios'] },
});
