// Unit tests for framework.js's esc() — the core HTML-escaping producer every
// render path relies on. Plain Node, no build:
//   npm run test:unit -- framework.test.js

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

const { esc, segSwitch } = loadModule('framework.js', {
  // icon() lives in icons.js, loaded before framework.js in the browser; the
  // marker keeps the assertions below about *which* segment carries it.
  globals: { icon: () => '<svg class="icon"></svg>' },
});

test('esc escapes the four HTML-significant characters', () => {
  assert.strictEqual(esc('&<>"'), '&amp;&lt;&gt;&quot;');
  assert.strictEqual(esc('<script>alert(1)</script>'),
    '&lt;script&gt;alert(1)&lt;/script&gt;');
  assert.strictEqual(esc('a & b'), 'a &amp; b');
});

test('esc returns empty string for null/undefined and stringifies others', () => {
  assert.strictEqual(esc(null), '');
  assert.strictEqual(esc(undefined), '');
  assert.strictEqual(esc(0), '0');       // 0 is not null — must stringify, not blank
  assert.strictEqual(esc(42), '42');
  assert.strictEqual(esc(false), 'false');
});

test('esc escapes & first so it does not double-encode entities in one pass', () => {
  // & is replaced before <>" so "&lt;" from user input becomes "&amp;lt;",
  // never collapsing an attacker-supplied entity into a live one.
  assert.strictEqual(esc('&amp;'), '&amp;amp;');
  assert.strictEqual(esc('&lt;script&gt;'), '&amp;lt;script&amp;gt;');
});

test("esc leaves the single quote unescaped (attributes must use double quotes)", () => {
  // Documents the contract: esc guards &<>\" only; templates quote attributes
  // with double quotes, so a single quote is not an escape target.
  assert.strictEqual(esc("it's"), "it's");
});

// ── segSwitch ───────────────────────────────────────────────────────────────
// The app's one picker control for a small fixed set of options (styleguide
// `.seg-switch`): personal preferences (language, theme, terminology) and the
// project's estimation unit share it, so its contract is asserted once here.

const UNITS = [
  { value: 'NONE',   label: 'No estimation' },
  { value: 'POINTS', label: 'Story points' },
  { value: 'HOURS',  label: 'Hours' },
];

test('segSwitch marks exactly the selected option, and only it carries the check', () => {
  const html = segSwitch(UNITS, 'POINTS', 'taskSettingsSetEstimationUnit', 'Estimation unit');
  assert.match(html, /role="radiogroup"/);
  assert.match(html, /aria-label="Estimation unit"/);
  assert.strictEqual((html.match(/role="radio"/g) || []).length, 3);
  assert.strictEqual((html.match(/aria-checked="true"/g) || []).length, 1);
  assert.strictEqual((html.match(/<svg class="icon">/g) || []).length, 1);
  assert.match(html, /aria-checked="true" data-act="taskSettingsSetEstimationUnit" data-a0="POINTS"><svg/);
  assert.match(html, /aria-checked="false" data-act="taskSettingsSetEstimationUnit" data-a0="NONE"><span/);
});

test('segSwitch with no option selected leaves every segment unchecked', () => {
  // A project whose unit the server has not answered for yet must not paint a
  // checked segment that lies about the stored value.
  const html = segSwitch(UNITS, null, 'act', 'Estimation unit');
  assert.strictEqual((html.match(/aria-checked="true"/g) || []).length, 0);
});

test('segSwitch escapes the label, the value and the aria-label', () => {
  // Labels can be admin-authored (custom names reach t() interpolation), so the
  // control escapes all three sinks rather than trusting its caller.
  const html = segSwitch([{ value: 'a"b', label: '<img src=x>' }], 'x', 'act', 'a "quoted" group');
  assert.match(html, /data-a0="a&quot;b"/);
  assert.match(html, /<span>&lt;img src=x&gt;<\/span>/);
  assert.match(html, /aria-label="a &quot;quoted&quot; group"/);
  assert.ok(!html.includes('<img src=x>'));
});
