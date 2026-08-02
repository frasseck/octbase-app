// Shared JS scope analysis — the free-variable resolver.
//
// Extracted from scripts/check-exports.mjs (2026-07-30, 37b stage 2) so the
// export guard and the ESM codemod cross-reference identifiers with ONE
// implementation. Two resolvers that disagree would let the codemod generate
// imports the guard does not believe in, or vice versa; the runbook calls for
// reusing this resolver rather than writing a second one.
//
// A hand-rolled free-variable walker over the ESTree AST. Scopes are pushed for
// functions (var + params hoist here) and blocks (let/const/class). References
// that resolve to no scope in the file are "free": they must be satisfied by
// another file's exports or a browser global.

const BROWSER_GLOBALS = new Set([
  // ECMAScript
  'Array', 'ArrayBuffer', 'BigInt', 'Boolean', 'DataView', 'Date', 'Error',
  'EvalError', 'Function', 'Infinity', 'Intl', 'JSON', 'Map', 'Math', 'NaN',
  'Number', 'Object', 'Promise', 'Proxy', 'RangeError', 'ReferenceError',
  'Reflect', 'RegExp', 'Set', 'String', 'Symbol', 'SyntaxError', 'TypeError',
  'URIError', 'WeakMap', 'WeakSet', 'decodeURIComponent', 'encodeURIComponent',
  'decodeURI', 'encodeURI', 'eval', 'globalThis', 'isFinite', 'isNaN',
  'parseFloat', 'parseInt', 'undefined', 'structuredClone',
  'Int8Array', 'Uint8Array', 'Uint8ClampedArray', 'Int16Array', 'Uint16Array',
  'Int32Array', 'Uint32Array', 'Float32Array', 'Float64Array', 'BigInt64Array',
  'BigUint64Array',
  // Browser platform
  'window', 'document', 'navigator', 'location', 'history', 'screen',
  'localStorage', 'sessionStorage', 'console', 'alert', 'confirm', 'prompt',
  'fetch', 'Headers', 'Request', 'Response', 'AbortController', 'AbortSignal',
  'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval',
  'requestAnimationFrame', 'cancelAnimationFrame', 'queueMicrotask',
  'addEventListener', 'removeEventListener', 'dispatchEvent',
  'atob', 'btoa', 'Blob', 'File', 'FileReader', 'FormData', 'URL',
  'URLSearchParams', 'TextEncoder', 'TextDecoder', 'WebSocket', 'EventSource',
  'XMLHttpRequest', 'DOMParser', 'XMLSerializer', 'MutationObserver',
  'ResizeObserver', 'IntersectionObserver', 'performance', 'crypto',
  'CustomEvent', 'Event', 'KeyboardEvent', 'MouseEvent', 'PointerEvent',
  'DragEvent', 'ClipboardEvent', 'StorageEvent', 'HashChangeEvent',
  'PopStateEvent', 'ErrorEvent', 'PromiseRejectionEvent', 'BeforeUnloadEvent',
  'Node', 'Element', 'HTMLElement', 'HTMLInputElement', 'HTMLTextAreaElement',
  'HTMLSelectElement', 'HTMLAnchorElement', 'HTMLImageElement', 'DocumentFragment',
  'NodeFilter', 'Range', 'Selection', 'getComputedStyle', 'matchMedia',
  'scrollTo', 'scrollBy', 'open', 'close', 'focus', 'blur', 'print',
  'innerWidth', 'innerHeight', 'devicePixelRatio', 'frameElement',
  'CSS', 'Image', 'Audio', 'Notification', 'IDBKeyRange', 'indexedDB',
  'ReadableStream', 'WritableStream', 'TransformStream', 'CompressionStream',
  'DecompressionStream', 'reportError', 'self', 'top', 'parent', 'origin',
  'name', 'status', 'event', 'closed', 'isSecureContext', 'visualViewport',
  'getSelection', 'clipboardData',
]);


function collectPatternNames(pattern, out) {
  switch (pattern.type) {
    case 'Identifier': out.push(pattern.name); break;
    case 'ObjectPattern':
      for (const p of pattern.properties) {
        if (p.type === 'RestElement') collectPatternNames(p.argument, out);
        else collectPatternNames(p.value, out);
      }
      break;
    case 'ArrayPattern':
      for (const el of pattern.elements) if (el) collectPatternNames(el, out);
      break;
    case 'AssignmentPattern': collectPatternNames(pattern.left, out); break;
    case 'RestElement': collectPatternNames(pattern.argument, out); break;
    default: break;
  }
}

// Hoist `var` declarations and function declarations into a function scope:
// walk the subtree without descending into nested functions.
function hoistVarScope(node, scope) {
  const visit = (n) => {
    if (!n || typeof n.type !== 'string') return;
    switch (n.type) {
      case 'FunctionDeclaration':
        if (n.id) scope.add(n.id.name);
        return; // do not descend
      case 'FunctionExpression':
      case 'ArrowFunctionExpression':
        return; // separate scope
      case 'VariableDeclaration':
        if (n.kind === 'var') {
          for (const d of n.declarations) {
            const names = [];
            collectPatternNames(d.id, names);
            for (const name of names) scope.add(name);
          }
        }
        break;
      default: break;
    }
    for (const key of Object.keys(n)) {
      if (key === 'type' || key === 'start' || key === 'end') continue;
      const v = n[key];
      if (Array.isArray(v)) v.forEach(visit);
      else if (v && typeof v.type === 'string') visit(v);
    }
  };
  visit(node);
}

// Pre-declare block-scoped names (let/const/class + function decls, which are
// block-scoped in strict mode) for a statement list.
function hoistLexical(statements, scope) {
  for (const s of statements) {
    if (s.type === 'VariableDeclaration' && s.kind !== 'var') {
      for (const d of s.declarations) {
        const names = [];
        collectPatternNames(d.id, names);
        for (const name of names) scope.add(name);
      }
    } else if (s.type === 'ClassDeclaration' && s.id) {
      scope.add(s.id.name);
    } else if (s.type === 'FunctionDeclaration' && s.id) {
      scope.add(s.id.name);
    }
  }
}

/**
 * Analyze one parsed file.
 * @returns {{freeRefs: Map<string, {loadTime: boolean}>, assignedNames: Set<string>,
 *            topLevelDecls: Map<string, string>, explicitExports: Map<string, number>,
 *            windowAssigns: Set<string>, iife: boolean}}
 */
function analyze(ast, code) {
  const freeRefs = new Map();   // name -> { loadTime }
  const assignedNames = new Set(); // identifiers that are assignment targets anywhere
  const topLevelDecls = new Map(); // name -> kind ('var'|'let'|'const'|'function'|'class')
  const explicitExports = new Map(); // name -> line (from Object.assign(window,{…}))
  const windowAssigns = new Set(); // window.X = / global.X = style exports

  // Detect the IIFE wrapper convention: a Program whose only non-directive
  // statement is `(() => { … })()` / `(function () { … })()`.
  let iifeBody = null;
  const realStatements = ast.body.filter(
    (s) => !(s.type === 'ExpressionStatement' && s.expression.type === 'Literal'),
  );
  if (realStatements.length === 1) {
    const s = realStatements[0];
    if (s.type === 'ExpressionStatement' && s.expression.type === 'CallExpression') {
      const callee = s.expression.callee;
      if (callee.type === 'ArrowFunctionExpression' || callee.type === 'FunctionExpression') {
        iifeBody = callee.body.type === 'BlockStatement' ? callee.body : null;
      }
    }
  }
  const iife = iifeBody !== null;

  // Record the "module top level" declarations (the IIFE body, or the Program
  // body for plain scripts) — these are the names an export block may publish
  // (IIFE) or that implicitly become global (plain script).
  const moduleTop = iife ? iifeBody.body : ast.body;
  for (const s of moduleTop) {
    if (s.type === 'VariableDeclaration') {
      for (const d of s.declarations) {
        const names = [];
        collectPatternNames(d.id, names);
        for (const name of names) topLevelDecls.set(name, s.kind);
      }
    } else if (s.type === 'FunctionDeclaration' && s.id) {
      topLevelDecls.set(s.id.name, 'function');
    } else if (s.type === 'ClassDeclaration' && s.id) {
      topLevelDecls.set(s.id.name, 'class');
    }
  }

  const scopes = [];
  const pushScope = () => { const s = new Set(); scopes.push(s); return s; };
  const popScope = () => scopes.pop();
  const declared = (name) => scopes.some((s) => s.has(name));
  const line = (node) => code.slice(0, node.start).split('\n').length;

  // funcDepth counts function bodies we are inside; code directly inside the
  // IIFE wrapper (or at Program level for plain scripts) executes at load time.
  const loadDepth = iife ? 1 : 0;
  let funcDepth = 0;

  const reference = (node) => {
    const n = node.name;
    if (declared(n) || BROWSER_GLOBALS.has(n)) return;
    const loadTime = funcDepth <= loadDepth;
    const prev = freeRefs.get(n);
    if (!prev) freeRefs.set(n, { loadTime, line: line(node) });
    else if (loadTime && !prev.loadTime) { prev.loadTime = true; prev.line = line(node); }
  };

  const visitFunction = (node) => {
    funcDepth++;
    const scope = pushScope();
    if (node.type !== 'ArrowFunctionExpression') scope.add('arguments');
    if (node.id) scope.add(node.id.name);
    for (const p of node.params) {
      const names = [];
      collectPatternNames(p, names);
      for (const name of names) scope.add(name);
      // default values / computed keys inside patterns still reference things:
      visitPatternExpressions(p);
    }
    if (node.body.type === 'BlockStatement') {
      hoistVarScope(node.body, scope);
      hoistLexical(node.body.body, scope);
      node.body.body.forEach(visit);
    } else {
      visit(node.body);
    }
    popScope();
    funcDepth--;
  };

  const visitPatternExpressions = (pattern) => {
    switch (pattern.type) {
      case 'AssignmentPattern': visit(pattern.right); visitPatternExpressions(pattern.left); break;
      case 'ObjectPattern':
        for (const p of pattern.properties) {
          if (p.type === 'RestElement') visitPatternExpressions(p.argument);
          else { if (p.computed) visit(p.key); visitPatternExpressions(p.value); }
        }
        break;
      case 'ArrayPattern':
        for (const el of pattern.elements) if (el) visitPatternExpressions(el);
        break;
      case 'RestElement': visitPatternExpressions(pattern.argument); break;
      default: break;
    }
  };

  const visitChildren = (n) => {
    for (const key of Object.keys(n)) {
      if (key === 'type' || key === 'start' || key === 'end') continue;
      const v = n[key];
      if (Array.isArray(v)) v.forEach(visit);
      else if (v && typeof v.type === 'string') visit(v);
    }
  };

  const visit = (n) => {
    if (!n || typeof n.type !== 'string') return;
    switch (n.type) {
      case 'Identifier': reference(n); return;
      case 'PrivateIdentifier': return;
      case 'MemberExpression':
        visit(n.object);
        if (n.computed) visit(n.property);
        return;
      case 'Property':
        if (n.computed) visit(n.key);
        // shorthand `{ a }`: value is the reference; key === value node
        visit(n.value);
        return;
      case 'MethodDefinition':
      case 'PropertyDefinition':
        if (n.computed) visit(n.key);
        if (n.value) visit(n.value);
        return;
      case 'LabeledStatement': visit(n.body); return;
      case 'BreakStatement': case 'ContinueStatement': return;
      case 'MetaProperty': return;
      case 'FunctionDeclaration': case 'FunctionExpression': case 'ArrowFunctionExpression':
        visitFunction(n); return;
      case 'ClassDeclaration': case 'ClassExpression': {
        const scope = pushScope();
        if (n.id) scope.add(n.id.name);
        if (n.superClass) visit(n.superClass);
        visit(n.body);
        popScope();
        return;
      }
      case 'BlockStatement': {
        const scope = pushScope();
        hoistLexical(n.body, scope);
        n.body.forEach(visit);
        popScope();
        return;
      }
      case 'ForStatement': case 'ForInStatement': case 'ForOfStatement': {
        const scope = pushScope();
        const init = n.init || n.left;
        if (init && init.type === 'VariableDeclaration' && init.kind !== 'var') {
          for (const d of init.declarations) {
            const names = [];
            collectPatternNames(d.id, names);
            for (const name of names) scope.add(name);
          }
        }
        visitChildren(n);
        popScope();
        return;
      }
      case 'CatchClause': {
        const scope = pushScope();
        if (n.param) {
          const names = [];
          collectPatternNames(n.param, names);
          for (const name of names) scope.add(name);
          visitPatternExpressions(n.param);
        }
        visit(n.body);
        popScope();
        return;
      }
      case 'VariableDeclarator':
        // The id is a declaration (already hoisted), not a reference — but
        // default values inside destructuring patterns are expressions.
        visitPatternExpressions(n.id);
        if (n.init) visit(n.init);
        return;
      case 'AssignmentExpression': {
        // Record simple identifier targets (for the reassigned-export check),
        // then visit both sides (the left is also a reference when it's a bare
        // identifier — it must resolve somewhere).
        if (n.left.type === 'Identifier') assignedNames.add(n.left.name);
        // window.X = … / global.X = … at any depth is an export.
        if (n.left.type === 'MemberExpression' && !n.left.computed
            && n.left.object.type === 'Identifier'
            && ['window', 'global', 'globalThis'].includes(n.left.object.name)
            && n.left.property.type === 'Identifier') {
          windowAssigns.add(n.left.property.name);
        }
        visitChildren(n);
        return;
      }
      case 'UpdateExpression':
        if (n.argument.type === 'Identifier') assignedNames.add(n.argument.name);
        visitChildren(n);
        return;
      case 'CallExpression': {
        // Object.assign(window, { … }) → the explicit export block.
        const c = n.callee;
        if (c.type === 'MemberExpression' && !c.computed
            && c.object.type === 'Identifier' && c.object.name === 'Object'
            && c.property.type === 'Identifier' && c.property.name === 'assign'
            && n.arguments.length >= 2
            && n.arguments[0].type === 'Identifier' && n.arguments[0].name === 'window'
            && n.arguments[1].type === 'ObjectExpression') {
          for (const p of n.arguments[1].properties) {
            if (p.type === 'Property' && !p.computed) {
              const key = p.key.type === 'Identifier' ? p.key.name : String(p.key.value);
              explicitExports.set(key, line(p.key));
            }
          }
        }
        visitChildren(n);
        return;
      }
      default:
        visitChildren(n);
        return;
    }
  };

  // Program scope: top-level declarations of the file itself.
  const programScope = pushScope();
  hoistVarScope(ast, programScope);
  hoistLexical(ast.body, programScope);
  ast.body.forEach(visit);
  popScope();

  return { freeRefs, assignedNames, topLevelDecls, explicitExports, windowAssigns, iife };
}

export { BROWSER_GLOBALS, collectPatternNames, analyze };
