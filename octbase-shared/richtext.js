// Octbase shared module — part of the @octbase/shared package (37b stage 3),
// imported by both SPAs. There is no second copy to drift any more, and
// `'use strict'` is gone because an ES module is always strict.
//
// Rich-text (task description) sanitizer. Descriptions are stored as a
// constrained HTML subset. The SERVER is the source of truth and always
// sanitizes on write (octbase-api internal/workmanagement/sanitize.go); this
// client-side sanitizer mirrors the same policy as defense-in-depth and for
// immediate editor feedback. Rendering never assigns raw API HTML to
// innerHTML — it always passes through sanitizeRichText() first.
//
// The HTML tree-walking is delegated to DOMPurify, imported from the pinned
// `dompurify` npm package since 37b stage 4 — before that it was a vendored
// UMD file both SPAs loaded as a classic <script>, and this module trusted the
// `DOMPurify` global to already be there. Everything Octbase-specific — the
// tag/attribute allowlist, per-tag attribute scoping, the strict href/src
// validators mirroring the server, and link/image hardening — lives in
// RT_DOMPURIFY_CONFIG and the afterSanitizeAttributes hook below.

import DOMPurify from 'dompurify';

// Tag allowlist — keep in sync with the server's allowedTags (sanitize.go).
const RT_ALLOWED_TAGS = [
  'p', 'br', 'h3', 'h4', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code',
  'strong', 'b', 'em', 'i', 'a', 'img',
];

const RT_DOMPURIFY_CONFIG = {
  ALLOWED_TAGS: RT_ALLOWED_TAGS,
  // DOMPurify's ALLOWED_ATTR is global (any attribute on any allowed tag);
  // the per-tag scoping the server applies (href only on <a>; src/alt only
  // on <img>) is enforced in the afterSanitizeAttributes hook below.
  ALLOWED_ATTR: ['href', 'src', 'alt'],
  ALLOW_DATA_ATTR: false,
  ALLOW_ARIA_ATTR: false,
};

// rtSafeHref mirrors the server (internal/shared/htmlsafe.go SafeHref):
// http(s)/mailto/relative only — no javascript:, data:, or any other scheme,
// and no control characters that could obscure one.
//
// The server decodes one layer of character references before validating, so an
// entity-obfuscated scheme is judged as a browser would decode it. This side
// deliberately has NO counterpart: the hook runs on a parsed DOM, so
// getAttribute('href') has already returned the decoded value —
// "&#106;avascript:alert(1)" arrives here as "javascript:alert(1)". Adding a
// decode step here would decode a second layer the server never does and break
// the parity the case table pins.
function rtSafeHref(v) {
  const t = (v || '').trim();
  if (!t) return false;
  const lower = t.toLowerCase();
  if (/[\x00\n\r\t]/.test(lower)) return false;
  if (lower.startsWith('http://') || lower.startsWith('https://') || lower.startsWith('mailto:')) return true;
  // Relative URL: reject if it carries a scheme.
  return !/^[a-z0-9+.\-]+:/.test(lower);
}

// rtSafeImageSrc mirrors the server (internal/workmanagement/sanitize.go
// safeImageSrc — this one stays per-module, because internal/docs allows any
// rooted path for a wiki page image): only our own
// attachment content endpoint (a relative path), never external/data/
// protocol-relative sources. The mirroring is enforced, not asserted:
// testdata/url-guard-cases.json is read by both this package's unit tests and
// the Go ones, so changing one side alone fails both.
function rtSafeImageSrc(v) {
  const t = (v || '').trim();
  if (!t) return false;
  const lower = t.toLowerCase();
  if (/[\x00\n\r\t]/.test(lower)) return false;
  if (t.startsWith('//') || /^[a-z0-9+.\-]+:/.test(lower)) return false;
  return t.startsWith('/api/v1/tasks/') && t.includes('/attachments/') && t.endsWith('/content');
}

// The hook is installed lazily on first use so this file only defines symbols
// at load time and never touches the imported DOMPurify while the module graph
// is still evaluating (the TDZ hazard scripts/check-tdz.mjs guards). It began
// as a workaround for something narrower — the vendored purify.js was a classic
// <script> that might not have run yet — which stage 4 retired; laziness is
// worth keeping on its own terms.
let _rtHookInstalled = false;
function _rtInstallHook() {
  if (_rtHookInstalled) return;
  _rtHookInstalled = true;
  // DOMPurify hooks apply to every sanitize() call; sanitizeRichText is the
  // only DOMPurify caller in both SPAs, so the hook is effectively scoped to
  // the rich-text policy. Revisit if another caller is ever added.
  DOMPurify.addHook('afterSanitizeAttributes', node => {
    const tag = node.tagName;
    // Per-tag attribute scoping (ALLOWED_ATTR above is global).
    if (tag !== 'A' && node.hasAttribute('href')) node.removeAttribute('href');
    if (tag !== 'IMG') {
      node.removeAttribute('src');
      node.removeAttribute('alt');
    }
    if (tag === 'A') {
      if (node.hasAttribute('href') && !rtSafeHref(node.getAttribute('href'))) node.removeAttribute('href');
      node.setAttribute('rel', 'noopener noreferrer');
      node.setAttribute('target', '_blank');
    }
    if (tag === 'IMG') {
      if (node.hasAttribute('src') && !rtSafeImageSrc(node.getAttribute('src'))) node.removeAttribute('src');
      node.setAttribute('loading', 'lazy');
      if (!node.getAttribute('alt')) node.setAttribute('alt', '');
      // An <img> whose src was stripped (failed validation) is removed.
      if (!node.getAttribute('src')) node.remove();
    }
  });
}

// sanitizeRichText parses untrusted HTML and returns a cleaned HTML string
// containing only allowlisted tags/attributes. Disallowed elements are
// unwrapped (their text is kept); dangerous elements (script/style/iframe/…)
// are dropped entirely — DOMPurify's default behavior, matching the previous
// hand-rolled sanitizer.
function sanitizeRichText(input) {
  if (!input) return '';
  _rtInstallHook();
  return DOMPurify.sanitize(input, RT_DOMPURIFY_CONFIG);
}

// looksLikeHTML heuristically detects whether a stored description is HTML
// (new format) vs. a legacy plain-text description.
function looksLikeHTML(s) {
  return /<\/?(p|br|h3|h4|ul|ol|li|blockquote|pre|code|strong|b|em|i|a|img)\b/i.test(s || '');
}

export { sanitizeRichText, rtSafeHref, rtSafeImageSrc, looksLikeHTML };
