#!/usr/bin/env node
// Unit tests for the HTML-injection guard's rules (scripts/check-innerhtml.mjs).
// Importing the guard is side-effect-free; every case runs scanSource() on a
// fixture snippet. Run: node --test scripts/test-check-innerhtml.mjs

import test from 'node:test';
import assert from 'node:assert/strict';
import { scanSource } from './check-innerhtml.mjs';

const flags = (src, file = 'octbase-frontend/js/fixture.js') => scanSource(file, src);
const clean = (src) => assert.deepEqual(flags(src), [], `expected clean: ${src}`);
const flagged = (src, n = 1, file = undefined) => {
  const v = file ? scanSource(file, src) : flags(src);
  assert.equal(v.length, n, `expected ${n} violation(s), got ${JSON.stringify(v)} for: ${src}`);
};

// ── mixed expressions containing an html`` template (OCT-8 fix 1) ───────────
test('html`` template as the whole expression is allowed', () => {
  clean('el.innerHTML = `${cond ? html`<b>${esc(item.name)}</b>` : ""}`;');
});
test('user field concatenated NEXT TO an html`` template is flagged', () => {
  flagged('el.innerHTML = `${item.title + html`<i>x</i>`}`;');
});
test('user field inside the html`` template itself is allowed (tag escapes)', () => {
  clean('el.innerHTML = `${html`<b>${item.title}</b>`}`;');
});
test('user field outside, safe template inside ternary — still flagged', () => {
  flagged('el.innerHTML = `${cond ? html`<b>ok</b>` : item.description}`;');
});

// ── leading-operator continuation lines (OCT-8 fix 2) ───────────────────────
test('leading-plus continuation concat is flagged', () => {
  flagged('el.innerHTML = prefix\n  + \'<b>\' + item.title\n  + suffix;');
});
test('trailing-plus continuation concat still flagged', () => {
  flagged('el.innerHTML = \'<b>\' +\n  item.title;');
});
test('plain multi-statement code after the sink line is not swept in', () => {
  clean('el.innerHTML = tpl\nconst n = a + \'x\';');
});

// ── logical-assignment sinks (OCT-8 fix 3) ──────────────────────────────────
test('||= with unescaped user field is flagged', () => {
  flagged('el.innerHTML ||= `${item.title}`;');
});
test('??= with escaped interpolation is clean', () => {
  clean('el.innerHTML ??= `${esc(item.title)}`;');
});
test('&&= concat is flagged', () => {
  flagged('el.innerHTML &&= \'<b>\' + item.name;');
});
test('comparison is still not a sink', () => {
  clean('if (el.innerHTML === `${item.title}`) reset();');
});

// ── trusted-call arguments are not sink concatenation (OCT-8 fix 4) ─────────
test('concat inside esc() arguments is clean', () => {
  clean("el.innerHTML = esc(prefix + '!');");
});
test('concat of a literal with an esc() result is still flagged (style rule)', () => {
  flagged("el.innerHTML = '<b>' + esc(item.title);");
});

// ── regressions: cases the 2026-08-03 hardening already covered ─────────────
test('append-concatenation is forbidden', () => {
  flagged('el.innerHTML += x;');
});
test('template-spelled concat is flagged', () => {
  flagged('el.innerHTML = `<b>` + user;');
});
test('bare-backtick nested template does not blanket-allow', () => {
  flagged('el.innerHTML = `${item.title || `—`}`;');
});
test('i18n key strings do not trip the field check', () => {
  clean("el.innerHTML = `${t('form.title')}`;");
});
test('semicolon entity inside a template does not truncate the concat scan', () => {
  flagged('el.innerHTML = `&nbsp;` + item.text;');
});
test('untagged template in a strict file is flagged', () => {
  flagged('el.innerHTML = `<b>static</b>`;', 1, 'octbase-frontend/js/realtime.js');
});
test('document.write is forbidden', () => {
  flagged('document.write(x);');
});
