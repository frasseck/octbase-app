// Unit tests for scripts/vite-strip-html-comments.mjs:
//   npm run test:unit -- strip-html-comments.test.js
//
// The build strips HTML comments so the rationale each HTML entry carries — why
// theme-init.js is a synchronous external script, which script tags each
// migration stage removed — stays in the repo without being served to visitors.
//
// The failure mode worth pinning is not "did it remove the comment" but the two
// ways a comment stripper eats something it should not: an escaped example of
// comment syntax on a documentation page, and content that merely looks like a
// comment boundary.

import { test } from 'vitest';
import assert from 'node:assert';
import { stripComments } from '../../scripts/vite-strip-html-comments.mjs';

test('a comment goes, the markup around it stays', () => {
  const html = '<head>\n<!-- why this tag is external -->\n<script src="js/theme-init.js"></script>\n</head>';
  const out = stripComments(html);
  assert.ok(!out.includes('<!--'));
  assert.ok(out.includes('<script src="js/theme-init.js"></script>'));
  assert.ok(out.includes('<head>') && out.includes('</head>'));
});

test('a multi-line comment goes entirely', () => {
  // Every comment in the real entries spans several lines; a single-line-only
  // pattern would leave the body behind as visible page text.
  const out = stripComments('<body>\n<!-- line one\n     line two\n     line three -->\n<div id="app"></div>\n</body>');
  assert.ok(!out.includes('line two'), 'comment body leaked into the page');
  assert.ok(out.includes('<div id="app"></div>'));
});

test('two comments do not swallow the markup between them', () => {
  // A greedy pattern matches from the first `<!--` to the LAST `-->` and takes
  // the page with it. This is the assertion that catches that.
  const out = stripComments('<!-- a --><main>keep me</main><!-- b -->');
  assert.strictEqual(out, '<main>keep me</main>');
});

test('an escaped example of comment syntax survives', () => {
  // The styleguide and user guide are documentation pages. For `<!--` to RENDER
  // as text it has to be written escaped, and escaped text is not a comment —
  // which is exactly why stripping with a pattern is safe on these files.
  const html = '<pre><code>&lt;!-- this is how you write a comment --&gt;</code></pre>';
  assert.strictEqual(stripComments(html), html);
});

test('a page with no comments is returned unchanged', () => {
  const html = '<!doctype html><html lang="en"><body><div id="app"></div></body></html>';
  assert.strictEqual(stripComments(html), html);
});

test('a comment that mentions <script> in prose is still a comment', () => {
  // The carve-out that protects inline script bodies must not let a comment's
  // PROSE start a raw block: index.html's head comment says "classic <script>
  // tags" mid-sentence, and the first carve-out matched from there to the next
  // real </script>, swallowing the comment's closer and shipping the `<!--`.
  // Whichever token starts first — comment or raw block — must win.
  const html = '<!-- the last two classic <script> tags here -->\n<script type="module" src="js/main.js"></script>\n';
  const out = stripComments(html);
  assert.ok(!out.includes('<!--'), 'comment opener survived the strip');
  assert.ok(out.includes('<script type="module" src="js/main.js"></script>'));
});

test('a real inline script body keeps its `<!--` text', () => {
  // The other direction of the same scan: inside a script, `<!--` is JS text,
  // and eating it would change the program.
  const html = '<script>\nconst marker = "<!--";\n</script>\n<!-- gone -->\n';
  const out = stripComments(html);
  assert.ok(out.includes('const marker = "<!--";'), 'script body was mangled');
  assert.ok(!out.includes('gone'));
});

test('the real HTML entries end up comment-free', async () => {
  // The end-to-end statement: run the transform over the shipped sources rather
  // than a fixture, so a new comment shape in a real file is covered by this
  // test the moment it is added.
  const fs = await import('node:fs');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
  const entries = [
    'octbase-frontend/index.html',
    'octbase-frontend/user-guide.html',
    'octbase-frontend/styleguide.html',
    'octbase-frontend/docs.html',
    'octbase-mobile/index.html',
  ];
  for (const rel of entries) {
    const file = path.join(root, rel);
    if (!fs.existsSync(file)) continue;
    const out = stripComments(fs.readFileSync(file, 'utf8'));
    assert.ok(!out.includes('<!--'), `${rel} still has a comment after stripping`);
    // The page must survive the strip — a pattern that ate the document would
    // pass the assertion above and fail this one.
    assert.ok(out.includes('<body') || out.includes('<BODY'), `${rel} lost its body`);
  }
});

// ── The whitespace the strip leaves behind ─────────────────────────────────
// Deleting only the comment text leaves the line it sat on, blank and still
// indented. The first version shipped exactly that: a ragged gap in the built
// <head> wherever a comment used to be.

test('a comment on its own line takes the line with it', async () => {
  const { stripComments } = await import('../../scripts/vite-strip-html-comments.mjs');
  const out = stripComments('<title>x</title>\n  <!-- note -->\n<script src="a.js"></script>');
  assert.strictEqual(out, '<title>x</title>\n<script src="a.js"></script>');
});

test('a comment sharing a line loses only itself', async () => {
  const { stripComments } = await import('../../scripts/vite-strip-html-comments.mjs');
  assert.strictEqual(stripComments('<div>keep</div> <!-- note -->\n'), '<div>keep</div> \n');
});

test('a run of blank lines collapses, a single authored one survives', async () => {
  const { stripComments } = await import('../../scripts/vite-strip-html-comments.mjs');
  assert.strictEqual(stripComments('<a>\n\n\n\n<b>'), '<a>\n\n<b>');
  assert.strictEqual(stripComments('<a>\n\n<b>'), '<a>\n\n<b>');
});

// ── Aligning the tags Vite injects ────────────────────────────────────────
// Vite emits its module entry, modulepreloads and extracted stylesheet indented
// two spaces whatever the surrounding document does, which left three tags
// stepped in under tags that are not their parents.

test('injected tags align to the previous non-blank line', async () => {
  const { alignInjectedTags } = await import('../../scripts/vite-strip-html-comments.mjs');
  const out = alignInjectedTags(
    '<head>\n<script src="theme.js"></script>\n  <script type="module" crossorigin src="a.js"></script>\n' +
    '  <link rel="modulepreload" crossorigin href="b.js">\n  <link rel="stylesheet" crossorigin href="c.css">\n</head>');
  for (const line of out.split('\n')) {
    assert.ok(!line.startsWith('  '), `line still indented: ${line}`);
  }
  assert.ok(out.includes('<link rel="stylesheet" crossorigin href="c.css">'));
});

test('an indented document keeps its own indentation', async () => {
  // Alignment is to the neighbouring line, not to column zero — a <head> that
  // is genuinely nested must stay nested.
  const { alignInjectedTags } = await import('../../scripts/vite-strip-html-comments.mjs');
  const out = alignInjectedTags('  <head>\n    <title>x</title>\n  <script type="module" src="a.js"></script>');
  assert.ok(out.endsWith('    <script type="module" src="a.js"></script>'), out);
});

test('a tag sharing a line with content is left alone', async () => {
  const { alignInjectedTags } = await import('../../scripts/vite-strip-html-comments.mjs');
  const html = '<p>see <link rel="stylesheet" href="a.css"> here</p>';
  assert.strictEqual(alignInjectedTags(html), html);
});

test('tidyBuiltHtml strips before it aligns', async () => {
  // Order matters: a tag that followed a comment must align against its real
  // neighbour, not against the indentation the comment had.
  const { tidyBuiltHtml } = await import('../../scripts/vite-strip-html-comments.mjs');
  const out = tidyBuiltHtml('<meta a>\n    <!-- gone -->\n  <script type="module" src="a.js"></script>');
  assert.strictEqual(out, '<meta a>\n<script type="module" src="a.js"></script>');
});
