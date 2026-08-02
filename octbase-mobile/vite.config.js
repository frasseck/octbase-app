import { defineConfig } from 'vite';
import { resolve } from 'node:path';
import { cpSync } from 'node:fs';
import { hashClassicAssets } from '../scripts/vite-hash-classic-assets.mjs';
import { stripHtmlComments } from '../scripts/vite-strip-html-comments.mjs';

// Vite loads this file as ESM, where `__dirname` exists only because Vite's
// bundling config loader injects it. Vite 8 warns that the future default
// (`configLoader: 'native'`) will not, so the directory is derived from
// `import.meta.dirname` — available since Node 20.11, and the repo builds on 22.
const __dirname = import.meta.dirname;

// Vite config for the mobile SPA (37b stage 2). Deliberately the same shape as
// octbase-frontend/vite.config.js — two SPAs with one build convention.
//
// The one file the bundler must NOT touch but the site still needs at runtime:
// theme-init.js (classic, non-deferred, CSP/FOUC constraint). Vite warns it
// cannot bundle it; that is the intended outcome, not a problem to fix.
//
// Since 37b stage 5 it is not copied verbatim either: hashClassicAssets emits
// it into assets/<name>-<hash>.js and rewrites the HTML reference, so it is
// cache-busted by its filename exactly like the bundled output — which is what
// retired the `?v=` stamping script and its git hooks.
//
// It is the only entry left. The five octbase-shared modules left this list at
// stage 3 (i18n/meta/richtext are imported now), and the package's two vendored
// UMD libraries left it at stage 4 as the `dompurify` and `qrcode-generator`
// npm packages.
const CLASSIC_ASSETS = [
  { ref: 'js/theme-init.js', file: resolve(__dirname, 'js/theme-init.js') },
];

function copyClassicAssets() {
  return {
    name: 'octbase-mobile-copy-classic-assets',
    closeBundle() {
      const out = resolve(__dirname, 'dist');
      // Directories Vite cannot rewrite for us, so they ship as they are — the
      // same three as the desktop build, for the same reasons (js/app.js writes
      // `<img src="img/…">` into template strings). Mobile ships a reduced
      // locale set on purpose: locales and icons are NOT shared between the
      // SPAs, and stayed out of the @octbase/shared package.
      for (const dir of ['locales', 'img', 'fonts']) {
        cpSync(resolve(__dirname, dir), resolve(out, dir), { recursive: true });
      }
    },
  };
}

// Where `vite preview` forwards /api to. In production the mobile app is served
// under /m/ by the desktop Caddy, which reverse-proxies /api, so the previewed
// app must be SAME-ORIGIN with its API the same way.
const API_ORIGIN = process.env.OCTBASE_API_ORIGIN || 'http://127.0.0.1:8000';

export default defineConfig({
  root: __dirname,
  plugins: [hashClassicAssets(CLASSIC_ASSETS), copyClassicAssets(), stripHtmlComments()],
  base: './',
  preview: {
    proxy: { '/api': { target: API_ORIGIN, changeOrigin: false } },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      // keepNames is LOAD-BEARING here for the same reason as the desktop SPA:
      // `_register` (js/core.js) keys the delegation registries by each handler
      // function's own `.name`, and the markup says data-act="…". Minification
      // renames top-level functions, so without this every delegated tap in the
      // app silently stops working — no exception, no console output. Not a
      // size optimization; a correctness requirement. An OUTPUT option since
      // Vite 8 bundles with rolldown rather than esbuild — see the desktop
      // vite.config.js for what the silent no-op looked like.
      output: { keepNames: true },
      input: { index: resolve(__dirname, 'index.html') },
    },
  },
});
