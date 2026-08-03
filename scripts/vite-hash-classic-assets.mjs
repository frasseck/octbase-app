// Content-hash the classic scripts the bundler deliberately does not touch.
//
// WHY THIS EXISTS (37b stage 5). Vite fingerprints everything inside the module
// graph — `assets/index-<hash>.js`, `assets/index-<hash>.css` — so a changed
// file always gets a new URL and Caddy can serve `assets/` as `immutable` for a
// year. A handful of scripts are deliberately outside that graph and were
// therefore shipped under a stable name:
//
//   js/theme-init.js      classic, non-deferred, must run before first paint
//                         (CSP forbids inlining it: script-src stays 'self')
//   js/docs-init.js       the two single-script static pages
//   js/user-guide-nav.js
//
// Stage 5 also covered `js/purify.js` and `js/qrcode.js`, the two vendored UMD
// libraries copied out of @octbase/shared. **Stage 4 removed them** — they are
// the `dompurify` and `qrcode-generator` npm packages now, so they are inside
// the module graph and Vite hashes them like everything else. Mobile is down to
// a single entry (theme-init.js) as a result.
//
// Until this plugin those were cache-busted by a `?v=<sha256>` query that a
// Python script stamped into the HTML, kept correct by a git pre-commit hook, a
// post-merge hook and a custom merge driver — roughly 140 lines of tooling plus
// three pieces of git configuration whose only job was to imitate what the
// bundler already does for every other asset. Stage 5 deletes all of it and
// hashes these files the same way Vite hashes the rest.
//
// The alternative — leave them unhashed and exempt them from the immutable
// cache header — was rejected because theme-init.js is render-blocking: every
// cold load would pay a revalidation round-trip before first paint, which is
// the exact cost the synchronous <script> in <head> exists to avoid.
//
// WHAT IT DOES. Each entry is emitted into `assets/<name>-<hash>.js` and every
// `<script src="…">` in every HTML entry pointing at the original path is
// rewritten to the hashed one. The hash is the first 8 hex of the file's
// SHA-256 — same derivation as the query it replaces, same guarantee: the URL
// changes if and only if the bytes change.
//
// The `file://` standalone builds do NOT use this plugin. That artifact is
// opened from a filesystem where there is no HTTP cache to bust, and a stable
// name is what makes the copied folder re-openable — so those configs keep
// copying the same scripts verbatim.
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { basename } from 'node:path';
import { minifyClassicScript } from './vite-minify-classic.mjs';

/**
 * @param {{ref: string, file: string}[]} entries  `ref` is the path exactly as
 *   the HTML writes it (e.g. `js/theme-init.js`); `file` is the absolute path
 *   to read the bytes from. They are the same path for every current entry, but
 *   the two are kept separate deliberately: the pair existed because the
 *   vendored libraries were referenced as `js/purify.js` while living only in
 *   @octbase/shared, and the next asset that has to be emitted from outside the
 *   app directory needs the same seam.
 */
export function hashClassicAssets(entries) {
  /** ref → emitted file name, filled in at buildStart. */
  const emitted = new Map();
  /** refs an HTML entry actually pointed at — see the closeBundle check. */
  const rewritten = new Set();

  return {
    name: 'octbase-hash-classic-assets',
    // Build only. The dev server serves the source tree, where these files sit
    // at their original paths and no cache header applies.
    apply: 'build',

    // buildStart, not generateBundle: the map has to be complete before the
    // HTML transform runs, and emitFile is legal this early. Doing both here
    // means the two halves cannot get out of order.
    // Async since the comment strip runs the real JS parser (see
    // vite-minify-classic.mjs for why a regex is not an option here). Rollup
    // awaits buildStart, so the map is still complete before the HTML transform.
    async buildStart() {
      emitted.clear();
      for (const { ref, file } of entries) {
        // The hash is taken over the EMITTED bytes, not the source: the URL has
        // to change if and only if what the browser receives changes, and since
        // the strip runs first a comment-only edit now correctly leaves the URL
        // alone rather than busting every cache for a reworded sentence.
        const source = await minifyClassicScript(readFileSync(file, 'utf8'), basename(ref));
        const hash = createHash('sha256').update(source).digest('hex').slice(0, 8);
        const fileName = `assets/${basename(ref, '.js')}-${hash}.js`;
        this.emitFile({ type: 'asset', fileName, source });
        emitted.set(ref, fileName);
      }
    },

    transformIndexHtml: {
      // 'post' so Vite's own HTML processing has already run: it leaves classic
      // <script src> tags alone (it can only bundle type="module"), and running
      // after it means the URLs we write are final and never re-resolved.
      order: 'post',
      handler(html) {
        // The `rewritten` bookkeeping scans a comment-stripped copy: a
        // commented-out <script src> is not a live reference, and counting it
        // would mask the "nothing referenced" warning in closeBundle. Scan
        // only — the emitted HTML is not altered here (the comment-strip
        // plugin owns comment removal, and a rewrite inside a comment goes
        // away with the comment).
        for (const [, src] of html.replace(/<!--[\s\S]*?-->/g, '')
            .matchAll(/<script\b[^>]*\bsrc="([^"]+)"/g)) {
          if (emitted.has(src)) rewritten.add(src);
        }
        return html.replace(
          /(<script\b[^>]*\bsrc=")([^"]+)(")/g,
          (whole, before, src, after) => {
            const hashed = emitted.get(src);
            if (!hashed) return whole;
            return `${before}./${hashed}${after}`;
          },
        );
      },
    },

    // An entry nothing referenced is a dead line in the config: the file ships
    // into assets/ under a hashed name that no page ever loads. It is a warning
    // rather than an error only because it is inert — the opposite direction
    // (an HTML reference with no entry here) is the dangerous one, and it
    // cannot happen silently: that file is not copied into dist/ at all any
    // more, so the page 404s on it the first time the build is served.
    closeBundle() {
      const unused = [...emitted.keys()].filter((ref) => !rewritten.has(ref));
      if (unused.length) {
        this.warn(
          `hash-classic-assets: nothing referenced ${unused.join(', ')} — ` +
          'if the tag was removed, drop it from the config list too.',
        );
      }
    },
  };
}
