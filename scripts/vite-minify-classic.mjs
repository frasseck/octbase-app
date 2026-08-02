// Strip comments from the classic scripts the bundler never sees.
//
// WHY THESE FOUR AND NOTHING ELSE. Everything inside the module graph is already
// comment-free: Vite minifies it, so `index-<hash>.js` and both standalone
// bundles ship none. The exceptions are the handful of scripts that are
// deliberately OUTSIDE that graph and are copied through verbatim —
// theme-init.js in each SPA, plus docs-init.js and user-guide-nav.js on the two
// static pages. Nothing minified them, so they shipped their comments: the
// reason theme-init is external (the Caddy CSP stays `script-src 'self'`), what
// it mirrors in framework.js, what boots Swagger UI. Same argument as the HTML
// comments — the note belongs in the repo, not in the response.
//
// WHY NOT A REGEX. Stripping JS comments textually is a different problem from
// stripping HTML ones and a far worse one: `//` appears inside every URL string
// in the file, `/*` can open inside a template literal, and a regex literal can
// contain either. There is no pattern that gets this right, so this runs the
// real parser instead.
//
// WHY mangle AND compress ARE OFF. Comment removal is what was asked for; the
// rest of minification is a behaviour change these files do not need. Keeping
// mangle off in particular is deliberate house policy — the SPAs' event
// delegation keys on `Function.prototype.name` (see the keepNames notes in both
// vite.config.js), so renaming identifiers in shipped code is a trap this repo
// has already fallen into once. The output is the same program with the same
// names, minus the comments and the slack whitespace.
import { rolldown } from 'rolldown';

/**
 * @param {string} code   the source of one classic script
 * @param {string} name   a label used for the virtual module id (diagnostics only)
 * @returns {Promise<string>} the same program with its comments gone
 */
export async function minifyClassicScript(code, name = 'classic') {
  const id = `\0octbase-classic:${name}`;
  const bundle = await rolldown({
    input: id,
    // A virtual module, so nothing is written to a temp file and the plugin
    // works identically in the image build (where /tmp is not somewhere to
    // scatter artifacts) and on a host.
    plugins: [{
      name: 'octbase-classic-virtual',
      resolveId: (source) => (source === id ? id : null),
      load: (loaded) => (loaded === id ? code : null),
    }],
    // These scripts are leaves — they reference globals (SwaggerUIBundle,
    // localStorage) and import nothing. A resolver warning here would mean the
    // file grew an import and needs to join the module graph properly instead.
    logLevel: 'warn',
  });
  const { output } = await bundle.generate({
    // IIFE, matching how a classic <script> already behaves: these files declare
    // top-level bindings nothing else reads (`_t`, `sections`), and the one
    // global any of them publishes (`window.ui` in docs-init) is written to
    // `window` explicitly, so wrapping changes nothing observable.
    format: 'iife',
    minify: { mangle: false, compress: false },
  });
  await bundle.close();
  return output[0].code;
}
