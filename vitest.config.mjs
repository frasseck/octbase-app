import { defineConfig } from 'vitest/config';
export default defineConfig({
  // octbase-mobile/js is included too: mobile keeps its own locale files, so it
  // needs its own tests, and a suite that only globs the desktop directory
  // silently ignores any file added there (which is how the first version of
  // octbase-mobile/js/locales.test.js ran zero times and still reported green).
  test: { include: ['octbase-frontend/js/*.test.js', 'octbase-mobile/js/*.test.js'], environment: 'node' },
});
