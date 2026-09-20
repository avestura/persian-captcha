/**
 * Bundles a framework wrapper.
 *
 *   node build.mjs react
 *   node build.mjs vue
 *
 * The framework itself is left external: a wrapper that bundled its own copy
 * of React would break hooks in any app that already has one.
 */

import { build } from 'esbuild';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

const target = process.argv[2];
if (!target || !existsSync(resolve(target, 'package.json'))) {
  console.error('usage: node build.mjs <react|vue>');
  process.exit(1);
}

const entry = existsSync(resolve(target, 'src/index.tsx'))
  ? `${target}/src/index.tsx`
  : `${target}/src/index.ts`;

await build({
  entryPoints: [entry],
  outfile: `${target}/dist/index.js`,
  bundle: true,
  format: 'esm',
  platform: 'browser',
  target: ['es2020', 'chrome90', 'firefox90', 'safari15'],
  jsx: 'automatic',
  // Peer dependencies stay external; only the wrapper's own code is bundled.
  external: ['react', 'react/jsx-runtime', 'react-dom', 'vue'],
  minify: false,
  sourcemap: true,
  logLevel: 'info',
});

console.log(`built ${target}/dist/index.js`);
