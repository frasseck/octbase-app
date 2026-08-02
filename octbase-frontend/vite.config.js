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

// The files the bundler must NOT touch but the site still needs at runtime:
// theme-init.js (classic, non-deferred, CSP/FOUC constraint) and the two
// single-script static pages. Vite warns it cannot bundle them — that is the
// intended outcome, not a problem to fix.
//
// Since 37b stage 5 they are not copied verbatim either: hashClassicAssets
// emits each into assets/<name>-<hash>.js and rewrites the HTML reference, so
// they are cache-busted by their filename exactly like the bundled output. That
// is what retired the `?v=` stamping script and its git hooks.
//
// This list used to be longer at both ends. The five octbase-shared modules
// left it at stage 3 (i18n/meta/richtext are imported now, so the bundler
// resolves them like any other module), and the package's two vendored UMD
// libraries left it at stage 4, when they became the `dompurify` and
// `qrcode-generator` npm packages — so what remains is only files that must
// genuinely stay outside the module graph, not files that could not get in.
const CLASSIC_ASSETS = [
  { ref: 'js/theme-init.js', file: resolve(__dirname, 'js/theme-init.js') },
  { ref: 'js/docs-init.js', file: resolve(__dirname, 'js/docs-init.js') },
  { ref: 'js/user-guide-nav.js', file: resolve(__dirname, 'js/user-guide-nav.js') },
];

function copyClassicAssets() {
  return {
    name: 'octbase-copy-classic-assets',
    closeBundle() {
      const out = resolve(__dirname, 'dist');
      // Directories Vite cannot rewrite for us, so they ship as they are:
      //   locales — @octbase/shared's i18n fetches locales/<lang>.json at
      //     runtime; locales stay per-SPA and are not part of the package.
      //   img     — referenced from JS template strings (`<img src="img/…">` in
      //     framework.js), which the bundler sees as opaque text. Without this
      //     the built site serves a broken logo.
      //   fonts   — app.css's @font-face URLs are hashed into assets/, but the
      //     preload in index.html and any copied CSS still expect the originals.
      for (const dir of ['locales', 'img', 'fonts']) {
        cpSync(resolve(__dirname, dir), resolve(out, dir), { recursive: true });
      }
    },
  };
}

// Vite config for the desktop SPA (37b stage 2).
//
// Multi-page on purpose: index.html is the SPA, and docs/user-guide/styleguide
// are single-script static pages that 37b says to leave as classic scripts.
// Listing them as entries keeps Caddy serving the same URLs after the build.
// Where `vite preview` forwards /api to. The preview server stands in for the
// Caddy front door (which reverse-proxies /api to the API container), so the
// previewed app is SAME-ORIGIN with its API exactly as the deployed one is.
// That is not a convenience: the session lives in an HttpOnly refresh cookie,
// and a cross-origin preview never gets it back, so every test that reloads the
// page lands on the login screen. Override for a stack on another port.
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
      // keepNames is LOAD-BEARING, not a nicety. Event delegation dispatches on
      // Function.prototype.name: `registerActions([showCreateTask, …], _A1)`
      // keys the registry by each function's own .name, and the markup says
      // data-act="showCreateTask" (js/README.md "Delegation registration").
      // Minification renames top-level functions, so without this every
      // delegated click, change, input, keydown and submit in the app silently
      // stops working — no exception, no console output, just dead buttons.
      // Removing this is not a size optimization; it is a total loss of
      // interactivity.
      //
      // It lives on the OUTPUT options because Vite 8 bundles with rolldown and
      // no longer uses esbuild at all: the previous `esbuild: { keepNames: true }`
      // became a silent no-op on upgrade, and the built bundle registered its
      // actions as `w([qn,ci],Te)` — mangled. That is the exact failure this
      // comment describes, and it shipped past a green build. `npm run
      // test:unit` cannot see it either; the Playwright suite is what catches
      // it, because dead delegation means dead buttons.
      output: { keepNames: true },
      input: {
        index: resolve(__dirname, 'index.html'),
        docs: resolve(__dirname, 'docs.html'),
        userGuide: resolve(__dirname, 'user-guide.html'),
        styleguide: resolve(__dirname, 'styleguide.html'),
        privacy: resolve(__dirname, 'privacy.html'),
      },
    },
  },
});
