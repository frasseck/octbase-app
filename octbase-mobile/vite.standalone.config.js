import { defineConfig } from 'vite';
import { resolve } from 'node:path';
import { cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tidyBuiltHtml } from '../scripts/vite-strip-html-comments.mjs';
import { minifyClassicScript } from '../scripts/vite-minify-classic.mjs';

// Vite loads this file as ESM, where `__dirname` exists only because Vite's
// bundling config loader injects it. Vite 8 warns that the future default
// (`configLoader: 'native'`) will not, so the directory is derived from
// `import.meta.dirname` — available since Node 20.11, and the repo builds on 22.
const __dirname = import.meta.dirname;

// The mobile standalone build (37b stage 2) — same rationale and shape as
// octbase-frontend/vite.standalone.config.js, read that one for the full
// argument. In short: a browser refuses `import` from a `file://` origin, and
// mobile has its own `USE_STANDALONE_DEMO_AUTH` (js/core.js) keyed off
// `location.protocol === 'file:'`, so the module build alone cannot be opened
// from disk. This target emits one self-contained IIFE bundle.
//
// It is also what the Playwright mobile suite loads: tests/test_mobile.py drives
// the app from `file://` on purpose (that is the code path the standalone demo
// takes), so it points at this artifact.
const OUT = 'dist-standalone';

// theme-init.js must run before first paint, so it stays a classic script
// beside the bundle — and since 37b stage 4 it is the only one. The five
// octbase-shared modules joined the bundle at stage 3, and the vendored
// DOMPurify/qrcode UMD builds joined it at stage 4 as the `dompurify` and
// `qrcode-generator` npm packages.
const CLASSIC = [
  'js/theme-init.js',
];

// Not content-hashed on purpose: opened from a filesystem, where there is no
// HTTP cache to bust and a stable name keeps a copied folder re-openable.
const BUNDLE = 'js/octbase-mobile-standalone.js';

function standaloneHtmlAndAssets() {
  return {
    name: 'octbase-mobile-standalone-html',
    async closeBundle() {
      const root = __dirname;
      const out = resolve(root, OUT);
      mkdirSync(resolve(out, 'js'), { recursive: true });

      // Copied through the comment strip rather than verbatim: this artifact is
      // opened from disk by real users (the standalone demo) and by the mobile
      // Playwright suite, so it ships the same JS the served build does.
      for (const f of CLASSIC) {
        writeFileSync(resolve(out, f), await minifyClassicScript(readFileSync(resolve(root, f), 'utf8'), f));
      }
      for (const dir of ['locales', 'img', 'css', 'fonts']) {
        try {
          cpSync(resolve(root, dir), resolve(out, dir), { recursive: true });
        } catch { /* mobile does not ship every directory the desktop does */ }
      }
      for (const f of ['favicon.svg', 'favicon.ico']) {
        try { cpSync(resolve(root, f), resolve(out, f)); } catch { /* optional */ }
      }

      // Derived from the real index.html so the theme-init ordering and the shell
      // markup cannot drift from the served app.
      const src = readFileSync(resolve(root, 'index.html'), 'utf8');
      const html = src.replace(
        /<script type="module" src="js\/app\.js"><\/script>/,
        `<script src="${BUNDLE}" defer></script>`,
      );
      if (html === src) {
        throw new Error(
          'mobile standalone build: could not find the module entry <script> in ' +
          'index.html — the tag shape changed, so this build would silently ship ' +
          'an app with no JS (and the Playwright mobile suite loads this file).',
        );
      }
      // Comments are stripped here rather than by the shared plugin: this is a
      // lib build, so Vite runs no transformIndexHtml hook over this file.
      writeFileSync(resolve(out, 'index.html'), tidyBuiltHtml(html));
    },
  };
}

export default defineConfig({
  root: __dirname,
  plugins: [standaloneHtmlAndAssets()],
  build: {
    outDir: OUT,
    emptyOutDir: true,
    cssCodeSplit: false,
    lib: {
      entry: resolve(__dirname, 'js/app.js'),
      formats: ['iife'],
      name: 'OctbaseMobile',
      fileName: () => BUNDLE.replace(/^js\//, ''),
    },
    rollupOptions: {
      output: {
        // The entry must export NOTHING — see
        // octbase-frontend/vite.standalone.config.js for the full argument. In
        // short: an export makes rollup wrap the bundle as
        // `function (exports) { … }`, which puts a real `exports` binding back
        // in scope for every bundled CommonJS/UMD module (`qrcode-generator`
        // is one), and that killed the whole bundle at load — from disk, where
        // only the standalone tests would see it. 'none' turns a stray export
        // into a build error naming it.
        //
        // keepNames is the same correctness requirement as the main build:
        // `_register` keys the delegation registries by each handler's own
        // `.name`, so mangling leaves every tap dead. It sits HERE rather than
        // in a second `rollupOptions` key because a duplicate key in an object
        // literal silently wins and drops the first — which is how this bundle
        // was built with mangled names while the config appeared to say
        // otherwise, and eight test_mobile.py cases caught it.
        keepNames: true,
        exports: 'none',
        dir: resolve(__dirname, OUT, 'js'),
        entryFileNames: 'octbase-mobile-standalone.js',
      },
    },
  },
});
