// Drop HTML comments from the built pages.
//
// WHY THIS EXISTS. This repo documents the *why* of a file inside the file, and
// the HTML entries are no exception: index.html carries the reason theme-init.js
// is a synchronous external script (CSP keeps `script-src 'self'`, so it cannot
// be inlined), and the history of which script tags stopped existing at each
// stage of the ES-module migration. That is exactly the note the next editor
// needs — and exactly the note a visitor should never receive.
//
// Vite does not strip comments from HTML entries (it only touches the tags it
// resolves), so every one of them was being served: 3 blocks in the desktop
// shell, 2 in mobile, 15 in the styleguide. The build now removes them and the
// source keeps them, which is the only arrangement where both readers are
// served.
//
// WHY A REGEX IS SAFE HERE, and stays safe. The obvious worry is a documentation
// page showing HTML comment syntax as an example — eat that and you eat the
// prose around it. It cannot happen: for `<!--` to *render* as text it must be
// escaped in the source (`&lt;!--`), and escaped text does not match this
// pattern. A literal `<!--` in an HTML file is a comment by definition; there is
// no third case. (Inside <script> or <style> the sequence is not a comment, but
// nothing here writes one, and Vite has already extracted module scripts by the
// time this runs.)
//
// Conditional comments (`<!--[if IE]>`) are not a consideration: they are a
// dead feature no supported browser honours, and nothing in either SPA uses one.

// Inline <script>/<style> bodies are carved out before stripping: inside them
// `<!--` is not an HTML comment (it is JS/CSS text), so eating it would change
// the program. Nothing in either SPA writes one today — Vite has extracted the
// module scripts before this runs, and theme-init is external — but the carve-
// out is what makes that a fact about the input rather than a load-bearing
// assumption of this file.
//
// Comments and raw-text blocks are found in ONE left-to-right scan, so
// whichever starts first claims the overlap. Carving out script blocks alone
// (the first version of the carve-out) let a comment that merely MENTIONS
// "<script>" in prose — index.html's own head comment does — start a bogus
// raw block mid-comment, swallow the comment's `-->`, and leave the `<!--`
// opener in the shipped page. Found by this plugin's own unit test in CI.
const TOKEN = /<!--[\s\S]*?-->|<script\b[\s\S]*?<\/script\s*>|<style\b[\s\S]*?<\/style\s*>/gi;

/** The transform itself. Exported because the two `file://` standalone builds
 *  cannot use the plugin below: they are `lib` builds with no HTML entry, so
 *  Vite never runs transformIndexHtml for them — their configs derive
 *  index.html by hand in closeBundle() and call this before writing it. Those
 *  bundles are what the standalone demo ships and what the Playwright mobile
 *  suite loads from disk, so leaving them out would have left the comments in
 *  the one artifact nobody re-reads. */
export function stripComments(html) {
  // Pass 1: tokenize. Raw-text blocks are parked behind NUL-framed
  // placeholders (NUL cannot appear in source HTML) so the comment strip
  // below cannot see into them; comments met first in the scan stay put —
  // including any "<script>" prose inside them — for pass 2 to remove with
  // its line-level cleanup intact.
  const raws = [];
  const tokenized = html.replace(TOKEN, (m) => {
    if (m.startsWith('<!--')) return m;
    raws.push(m);
    return `\x00${raws.length - 1}\x00`;
  });
  const stripped = tokenized
    // A comment that occupies whole lines takes those lines with it, leading
    // indentation and closing newline included. Deleting only the comment text
    // leaves the blank line and the indent that led up to it, which is what the
    // first version of this shipped: the built <head> came out with a ragged
    // gap wherever a comment used to be. Anchored per line with `m` so an
    // inline trailing comment (`<div> <!-- note --></div>`) still loses only
    // itself and never the markup sharing its line.
    .replace(/^[ \t]*<!--[\s\S]*?-->[ \t]*\r?\n/gm, '')
    // Whatever is left is inline: drop the comment, keep the line.
    .replace(/<!--[\s\S]*?-->/g, '')
    // EVERY run of consecutive blank lines collapses to a single blank line —
    // not only the runs the comment removal created. That flattens an
    // author-written double blank too, which is accepted: the built page is
    // for visitors, and one blank line separates sections just as well. A
    // single blank line survives as itself.
    .replace(/(\r?\n)[ \t]*(?:\r?\n)+/g, '$1$1');
  return stripped.replace(/\x00(\d+)\x00/g, (_, i) => raws[+i]);
}

// Vite emits the tags it injects — the module entry, its modulepreloads, the
// extracted stylesheet — indented two spaces, regardless of how the surrounding
// document is written. Both SPA shells keep <head> flush-left, so the built page
// came out with three tags stepped in under tags that are not their parents.
// This was always true; removing the comments is what made it the only ragged
// thing left on the page.
//
// Alignment is to the PREVIOUS non-blank line, not to a fixed column, so this
// stays correct for a document whose <head> is indented (and does nothing at all
// to one Vite already matched).
//
// Deliberately narrow. It rewrites a line only when the line is nothing but one
// of those three tags — never a tag sharing a line with content, and never
// anything else. A blanket "re-indent <head>" would reformat the inline <style>
// block in user-guide.html and styleguide.html, where the nesting is the
// readability. Documentation pages that *show* these tags as examples are safe
// for the same reason the comment strip is: to render as text they must be
// escaped, and `&lt;link` does not match `<link`.
const INJECTED_TAG = /^[ \t]*<(?:script\s+type="module"|link\s+rel="(?:modulepreload|stylesheet)")[^>]*>(?:<\/script>)?[ \t]*$/;

export function alignInjectedTags(html) {
  const lines = html.split('\n');
  let indent = '';
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].trim()) continue;
    if (INJECTED_TAG.test(lines[i])) lines[i] = indent + lines[i].trim();
    else indent = lines[i].match(/^[ \t]*/)[0];
  }
  return lines.join('\n');
}

/** Both passes, in the order the built page needs them: comments go first, so a
 *  tag that followed one is aligned against its real neighbour rather than
 *  against the comment's indentation. */
export function tidyBuiltHtml(html) {
  return alignInjectedTags(stripComments(html));
}

/**
 * Vite plugin: strip every HTML comment from each generated HTML entry.
 *
 * Build only. The dev server serves the source tree, where the comments are
 * the point — and stripping there would make the served page differ from the
 * file being edited, which is the one place that costs more than it buys.
 */
export function stripHtmlComments() {
  return {
    name: 'octbase-strip-html-comments',
    apply: 'build',
    transformIndexHtml: {
      // 'post' so this runs after Vite's own HTML processing and after the
      // asset-hashing plugin has rewritten its <script src> tags. Anything a
      // plugin injects earlier — including comments Vite itself might emit — is
      // therefore covered too, which an earlier hook could not promise.
      order: 'post',
      handler: tidyBuiltHtml,
    },
  };
}
