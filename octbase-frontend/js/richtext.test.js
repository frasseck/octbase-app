// Unit tests for @octbase/shared/richtext.js's pure URL/format guards. Plain Node, no build:
//   node --test js/richtext.test.js
//
// rtSafeHref and rtSafeImageSrc are the client mirror of the server's
// sanitize.go safeHref / safeImageSrc. That parity was hand-verified once
// (2026-07-21) and is now enforced continuously by the shared case table at the
// bottom of this file. These are the security boundary against javascript:/data:/
// protocol-relative/control-character bypasses, so the adversarial cases below
// are the point of the file. DOMPurify-backed sanitizeRichText needs a real DOM
// and is left to the e2e suite.

import { test } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import { rtSafeHref, rtSafeImageSrc, looksLikeHTML } from '@octbase/shared/richtext.js';

test('rtSafeHref accepts http(s), mailto and relative URLs', () => {
  for (const ok of [
    'http://example.com/x',
    'https://example.com/x',
    'HTTPS://EXAMPLE.COM',        // scheme match is case-insensitive
    'mailto:a@b.com',
    '  https://example.com  ',    // trimmed before checking
    '/relative/path',
    'relative/path',
    '#anchor',
    '?query=1',
  ]) {
    assert.strictEqual(rtSafeHref(ok), true, `expected accept: ${JSON.stringify(ok)}`);
  }
});

test('rtSafeHref rejects dangerous schemes, control chars and empties', () => {
  for (const bad of [
    'javascript:alert(1)',
    'JavaScript:alert(1)',        // case cannot smuggle a scheme past it
    '  javascript:alert(1)',      // nor leading whitespace
    'data:text/html,<script>1</script>',
    'vbscript:msgbox(1)',
    'foo:bar',                    // any non-allowlisted scheme
    'java\tscript:alert(1)',      // tab
    'https://exa\nmple.com',      // newline
    'https://exa\x00mple.com',    // NUL
    '',
    '   ',
    null,
    undefined,
  ]) {
    assert.strictEqual(rtSafeHref(bad), false, `expected reject: ${JSON.stringify(bad)}`);
  }
});

test('rtSafeImageSrc accepts only our own attachment content endpoint', () => {
  assert.strictEqual(
    rtSafeImageSrc('/api/v1/tasks/abc/attachments/def/content'), true);
  assert.strictEqual(
    rtSafeImageSrc('  /api/v1/tasks/abc/attachments/def/content  '), true); // trimmed
});

test('rtSafeImageSrc rejects external, protocol-relative, data and malformed srcs', () => {
  for (const bad of [
    'https://evil.example.com/x.png',
    '//evil.example.com/x.png',                        // protocol-relative
    'data:image/png;base64,iVBORw0KGgo=',
    '/api/v1/tasks/abc/attachments/def/content'.replace('content', 'con\x00tent'), // control char
    '/other/path/content',                             // wrong prefix
    '/api/v1/tasks/abc/content',                       // no /attachments/
    '/api/v1/tasks/abc/attachments/def',               // no /content suffix
    '/api/v1/tasks/abc/attachments/def/content/../..',  // suffix not /content
    '',
    null,
  ]) {
    assert.strictEqual(rtSafeImageSrc(bad), false, `expected reject: ${JSON.stringify(bad)}`);
  }
});

test('looksLikeHTML detects allowlisted tags and ignores bare angle brackets', () => {
  for (const html of ['<p>hi</p>', '<br>', '<A href="x">y</A>', '<STRONG>b</STRONG>', 'text <em>x</em>', '</li>']) {
    assert.strictEqual(looksLikeHTML(html), true, `expected HTML: ${JSON.stringify(html)}`);
  }
  for (const plain of ['plain text', '5 < 10 and 20 > 15', 'a < b and c > d', '', null, undefined]) {
    // Bare angle brackets with no allowlisted tag name are treated as legacy text.
    // (Note 'a<b>c' WOULD match — '<b>' is the bold tag — so it is deliberately absent.)
    assert.strictEqual(looksLikeHTML(plain), false, `expected plain: ${JSON.stringify(plain)}`);
  }
});

// ── Parity with the server's Go implementation ────────────────────────────────
// rtSafeHref / rtSafeImageSrc mirror sanitize.go's safeHref / safeImageSrc. That
// claim used to live only in a comment. testdata/url-guard-cases.json at the
// repository root is now the contract: this file and
// octbase-api/internal/workmanagement/sanitize_parity_test.go both read it, so
// changing one implementation without the other fails a test on both sides
// instead of leaving the browser and the server disagreeing about which URLs
// are safe.
//
// Add a case to the table, never to only one of the two suites.
const GUARD_CASES = JSON.parse(
  fs.readFileSync(new URL('../../testdata/url-guard-cases.json', import.meta.url), 'utf8'),
);

test('rtSafeHref agrees with the server on every shared case', () => {
  assert.ok(GUARD_CASES.safeHref.length > 0, 'the shared case table must not be empty');
  for (const { input, want, why } of GUARD_CASES.safeHref) {
    assert.strictEqual(rtSafeHref(input), want,
      `rtSafeHref(${JSON.stringify(input)}) must be ${want} — ${why}`);
  }
});

test('rtSafeImageSrc agrees with the server on every shared case', () => {
  assert.ok(GUARD_CASES.safeImageSrc.length > 0, 'the shared case table must not be empty');
  for (const { input, want, why } of GUARD_CASES.safeImageSrc) {
    assert.strictEqual(rtSafeImageSrc(input), want,
      `rtSafeImageSrc(${JSON.stringify(input)}) must be ${want} — ${why}`);
  }
});
