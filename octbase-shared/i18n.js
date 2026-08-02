// i18n core module: loads locale JSON files, provides t(), setLocale(), getLocale().
//
// Part of the @octbase/shared package (37b stage 3): a real ES module imported
// by both SPAs, no longer an IIFE that published t()/getLocale() onto `window`.
// The desktop SPA still puts those two on `window` deliberately — the Playwright
// suite calls them — but that is now namespace.js's decision to make, not this
// module's side effect.
// Only English and German are offered. A previously-stored 'fr' preference
// (French was removed) is not in AVAILABLE_LOCALES, so it falls back to English.
const AVAILABLE_LOCALES = ['en', 'de'];
const FALLBACK_LOCALE = 'en';
const STORAGE_KEY = 'octbase.lang';

// Vocabulary, orthogonal to language: AGILE is what the locale files say
// outright, CLASSIC is an overlay of the terms classic project management
// uses instead (phase, task pool, work package, effort points). It is a
// display preference — nothing about the data, the API or the stored values
// changes with it. A term with no classic variant simply keeps its agile
// wording, which is why velocity and burndown are absent from the overlay:
// they are agile metrics with no classic counterpart, and inventing one
// would be worse than leaving them named.
const TERMINOLOGIES = ['AGILE', 'CLASSIC'];
const DEFAULT_TERMINOLOGY = 'AGILE';
const OVERLAY_NAMESPACE = { CLASSIC: 'classic' };
const TERMINOLOGY_KEY = 'octbase.terminology';

const cache = {};
let activeLocale = FALLBACK_LOCALE;
let activeTerminology = DEFAULT_TERMINOLOGY;
let onChangeCallback = null;

function detectInitialLocale() {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored && AVAILABLE_LOCALES.includes(stored)) return stored;
  const nav = (navigator.language || '').slice(0, 2).toLowerCase();
  if (AVAILABLE_LOCALES.includes(nav)) return nav;
  return FALLBACK_LOCALE;
}

function fetchViaXhr(url) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('GET', url, true);
    xhr.onload = () => {
      if (xhr.status === 0 || xhr.status === 200) {
        try {
          resolve(JSON.parse(xhr.responseText));
        } catch (e) {
          reject(e);
        }
      } else {
        reject(new Error(`Failed to load locale: ${url} (status ${xhr.status})`));
      }
    };
    xhr.onerror = () => reject(new Error(`Failed to load locale: ${url}`));
    xhr.send();
  });
}

async function loadLocale(lang) {
  if (cache[lang]) return cache[lang];
  const url = `locales/${lang}.json`;
  let data;
  if (location.protocol === 'file:') {
    data = await fetchViaXhr(url);
  } else {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`Failed to load locale: ${lang}`);
    data = await res.json();
  }
  cache[lang] = data;
  return data;
}

function lookup(data, key) {
  const parts = key.split('.');
  let node = data;
  for (const part of parts) {
    if (node == null || typeof node !== 'object' || !(part in node)) return undefined;
    node = node[part];
  }
  return node;
}

function interpolate(str, vars) {
  return str.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (match, name) => {
    return Object.prototype.hasOwnProperty.call(vars, name) ? String(vars[name]) : match;
  });
}

// resolve walks: the active vocabulary's overlay in the active language, then
// the plain key in the active language, then the plain key in English. The
// overlay is deliberately NOT consulted in the fallback language — for a
// German reader a missing classic term is better served by the German agile
// word than by an English classic one.
function resolve(key, vars) {
  let node;
  const overlay = OVERLAY_NAMESPACE[activeTerminology];
  if (overlay) node = lookup(cache[activeLocale], `${overlay}.${key}`);
  if (node === undefined) node = lookup(cache[activeLocale], key);
  if (node === undefined && activeLocale !== FALLBACK_LOCALE) {
    node = lookup(cache[FALLBACK_LOCALE], key);
  }
  if (node === undefined) {
    console.warn(`[i18n] Missing translation key: ${key}`);
    return key;
  }
  if (typeof node === 'object' && node !== null) {
    if ('count' in vars) {
      const plural = Number(vars.count) === 1 ? 'one' : 'other';
      node = node[plural];
      if (node === undefined) {
        console.warn(`[i18n] Missing plural form "${plural}" for key: ${key}`);
        return key;
      }
    } else {
      console.warn(`[i18n] Translation key "${key}" resolved to an object`);
      return key;
    }
  }
  return interpolate(String(node), vars);
}

function t(key, vars = {}) {
  return resolve(key, vars);
}

function getLocale() {
  return activeLocale;
}

async function setLocale(lang) {
  if (!AVAILABLE_LOCALES.includes(lang)) lang = FALLBACK_LOCALE;
  try {
    await loadLocale(lang);
  } catch (e) {
    console.error(e);
    if (lang !== FALLBACK_LOCALE) {
      await loadLocale(FALLBACK_LOCALE);
      lang = FALLBACK_LOCALE;
    }
  }
  activeLocale = lang;
  localStorage.setItem(STORAGE_KEY, lang);
  document.documentElement.lang = lang;
  if (onChangeCallback) onChangeCallback(lang);
}

function onLocaleChange(cb) {
  onChangeCallback = cb;
}

function getTerminology() {
  return activeTerminology;
}

// setTerminology switches the vocabulary. Unlike setLocale it loads nothing —
// the overlay ships inside the locale file that is already in memory — so it
// is synchronous, and callers repaint the view themselves.
function setTerminology(value) {
  activeTerminology = TERMINOLOGIES.includes(value) ? value : DEFAULT_TERMINOLOGY;
  localStorage.setItem(TERMINOLOGY_KEY, activeTerminology);
  return activeTerminology;
}

function detectInitialTerminology() {
  const stored = localStorage.getItem(TERMINOLOGY_KEY);
  return TERMINOLOGIES.includes(stored) ? stored : DEFAULT_TERMINOLOGY;
}

async function initI18n() {
  // Read the stored vocabulary before the first render, so a classic-mode
  // user never sees the agile words flash by on the way in. The server's copy
  // reconciles afterwards, exactly as the language preference does.
  activeTerminology = detectInitialTerminology();
  const initial = detectInitialLocale();
  if (initial !== FALLBACK_LOCALE) {
    try {
      await loadLocale(FALLBACK_LOCALE);
    } catch (e) {
      console.error(e);
    }
  }
  try {
    await loadLocale(initial);
    activeLocale = initial;
  } catch (e) {
    console.error(e);
    activeLocale = FALLBACK_LOCALE;
  }
  document.documentElement.lang = activeLocale;
}

// The named exports are the module's surface; `i18n` is the same surface as one
// object, kept because several callers pass the whole thing around (the settings
// views read AVAILABLE_LOCALES/TERMINOLOGIES off it and call setLocale through
// it). Both are live views of the same functions — not two implementations.
const i18n = {
  AVAILABLE_LOCALES,
  FALLBACK_LOCALE,
  TERMINOLOGIES,
  DEFAULT_TERMINOLOGY,
  t,
  getLocale,
  setLocale,
  getTerminology,
  setTerminology,
  initI18n,
  onLocaleChange,
};

export { AVAILABLE_LOCALES, DEFAULT_TERMINOLOGY, FALLBACK_LOCALE, TERMINOLOGIES, getLocale, getTerminology, i18n, initI18n, onLocaleChange, setLocale, setTerminology, t };
