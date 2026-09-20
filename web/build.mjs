/**
 * Builds the browser assets with esbuild.
 *
 * The output in dist/ is committed to the repository on purpose. The Go
 * service embeds it with go:embed, so committing the bundles means `go build`
 * alone produces a working binary: a contributor who only touches Go, and a
 * CI job that only runs Go, never need a Node toolchain installed.
 *
 *   node build.mjs           build once
 *   node build.mjs --watch   rebuild on change
 */

import { build, context } from 'esbuild';
import { mkdir } from 'node:fs/promises';

const watch = process.argv.includes('--watch');
const dev = watch || process.argv.includes('--dev');

await mkdir('dist', { recursive: true });

/** Browsers the bundles must run on. Kept wide: a captcha that fails to load
 * is a login page nobody can get past. */
const target = ['es2020', 'chrome90', 'firefox90', 'safari15', 'edge90'];

const common = {
  bundle: true,
  minify: !dev,
  sourcemap: dev ? 'inline' : false,
  target,
  legalComments: 'none',
  logLevel: 'info',
};

/** Each entry is a standalone script: no shared chunks, no module loader. */
const builds = [
  {
    ...common,
    entryPoints: ['src/widget.ts'],
    outfile: 'dist/widget.js',
    // The loader runs on somebody else's page, so it must not leak a single
    // global beyond the two it deliberately defines.
    format: 'iife',
    banner: { js: '/* persian-captcha widget loader */' },
  },
  {
    ...common,
    entryPoints: ['src/frame/main.ts'],
    outfile: 'dist/frame.js',
    format: 'iife',
  },
  {
    ...common,
    entryPoints: ['src/pow-worker.ts'],
    outfile: 'dist/pow.js',
    // Classic worker script: `new Worker(url)` without { type: 'module' }
    // works in every browser the widget targets.
    format: 'iife',
  },
  {
    ...common,
    entryPoints: ['src/frame.css'],
    outfile: 'dist/frame.css',
  },
];

if (watch) {
  const contexts = await Promise.all(builds.map((options) => context(options)));
  await Promise.all(contexts.map((ctx) => ctx.watch()));
  console.log('watching for changes; press Ctrl+C to stop');
} else {
  await Promise.all(builds.map((options) => build(options)));
  console.log('built dist/widget.js, dist/frame.js, dist/pow.js, dist/frame.css');
}
