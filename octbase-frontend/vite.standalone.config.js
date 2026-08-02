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

// The standalone-demo build (37b stage 2).
//
// WHY A SECOND TARGET EXISTS AT ALL. The demo is delivered by opening
// index.html from disk, and `USE_STANDALONE_DEMO_AUTH` (js/env.js) keys off
// `location.protocol === 'file:'` to auto-sign-in as the seeded demo user. A
// browser will not evaluate `import` from a file:// origin — every module fetch
// is a cross-origin request from origin `null` and is refused — so the normal
// module build cannot be opened from disk at all: it stops at "Loading Octbase…"
// with the entry blocked by CORS. This target produces ONE self-contained IIFE
// bundle, which has no imports at runtime and therefore loads from disk exactly
// as the classic scripts did before the conversion.
//
// It is a packaging difference only: same source, same modules, same escaping
// producers. Do not let it become a second app — if something is true only in
// the standalone bundle, that is a bug in this config.
const OUT = 'dist-standalone';

// The one classic script that is not part of the module graph and must sit
// beside the bundle: theme-init.js (must run before first paint). The five
// octbase-shared modules joined the bundle at 37b stage 3, and the vendored
// DOMPurify/qrcode UMD builds — which stage 3 had to keep beside it as classic
// scripts — joined it at stage 4 as the `dompurify` and `qrcode-generator` npm
// packages. The standalone artifact is now one bundle plus theme-init.
const CLASSIC = [
  'js/theme-init.js',
];

// The bundle filename is deliberately NOT content-hashed: this artifact is
// opened from a filesystem, where there is no HTTP cache to bust and a stable
// name is what makes the copied folder re-openable.
const BUNDLE = 'js/octbase-standalone.js';

function standaloneHtmlAndAssets() {
  return {
    name: 'octbase-standalone-html',
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
      for (const dir of ['locales', 'img', 'fonts', 'css']) {
        cpSync(resolve(root, dir), resolve(out, dir), { recursive: true });
      }
      for (const f of ['favicon.svg', 'favicon.ico']) {
        try { cpSync(resolve(root, f), resolve(out, f)); } catch { /* optional */ }
      }

      // Derive the HTML from the real index.html rather than keeping a second
      // copy: one `<script type="module">` becomes one classic `<script>`, and
      // everything else — the theme-init ordering, the CSP-safe external
      // scripts, the shell div — stays byte-identical by construction.
      const src = readFileSync(resolve(root, 'index.html'), 'utf8');
      const html = src.replace(
        /<script type="module" src="js\/main\.js"><\/script>/,
        `<script src="${BUNDLE}" defer></script>`,
      );
      if (html === src) {
        throw new Error(
          'standalone build: could not find the module entry <script> in index.html — ' +
          'the tag shape changed, so this build would silently ship an app with no JS.',
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
    // CSS stays the plain css/app.css the copied <link> already points at, so
    // the bundle carries JS only.
    cssCodeSplit: false,
    lib: {
      entry: resolve(__dirname, 'js/main.js'),
      formats: ['iife'],
      name: 'Octbase',
      fileName: () => BUNDLE.replace(/^js\//, ''),
    },
    rollupOptions: {
      output: {
        // The entry must export NOTHING. An export makes rollup wrap this
        // bundle as `function (exports) { … }`, which reintroduces a real
        // `exports` binding into the scope every bundled CommonJS/UMD module
        // sees — `qrcode-generator` is still a UMD file, and that is exactly
        // the branch confusion that killed the whole bundle at load, from
        // disk, where no test but the standalone ones would see it. Keeping
        // this at 'none' turns a stray export into a build error naming it
        // rather than a broken demo. (Stage 4 moved the UMD from a copied
        // classic script to an npm dependency, so rollup's interop now handles
        // it — but the entry-export hazard is unchanged, so the guard stays.)
        //
        // keepNames is the same correctness requirement as the main build:
        // delegated dispatch keys handlers by `fn.name`, so mangling leaves
        // every button dead. It sits HERE rather than in a second
        // `rollupOptions` key because a duplicate key in an object literal
        // silently wins and drops the first — which is how this bundle was
        // built with mangled names while the config appeared to say otherwise.
        keepNames: true,
        exports: 'none', dir: resolve(__dirname, OUT, 'js'), entryFileNames: 'octbase-standalone.js' },
    },
  },
});
