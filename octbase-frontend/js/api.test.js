// Unit tests for the Prefetch hand-off. Plain Node, no build:
//   npm run test:unit -- api.test.js
//
// Prefetch lets boot and the router start a view's requests before its renderer
// runs (see the block comment in api.js). It is a hand-off, not a cache, and
// three rules are what keep it from serving the wrong data:
//   - take() consumes, so a re-render refetches instead of replaying;
//   - entries are per project, so navigating A → B cannot hand B's board A's
//     tasks (they would all be filtered out as belonging to no lane of B,
//     rendering B's board silently empty);
//   - entries expire, so one left unconsumed by an abandoned navigation cannot
//     surface minutes later under a navigation it was never started for.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// api.js's top-level code builds the api object from BASE_PATH and the http
// wrapper; the tests below drive Prefetch only, so those are stubs. `calls`
// records every task-list request the module would have issued, and `clock`
// drives the entry TTL — the module reads the `Date` of its own vm context, so
// the clock has to be injected as a global rather than patched out here.
function fresh() {
  const calls = [];
  const clock = { now: 1_000_000 };
  const win = loadModule('api.js', {
    globals: {
      Date: { now: () => clock.now },
      BASE_PATH: '/api/v1',
      Auth: { logout() {} },
      http: {
        get(path) { calls.push(path); return Promise.resolve({ path }); },
        post() { return Promise.resolve({}); },
        patch() { return Promise.resolve({}); },
        del() { return Promise.resolve({}); },
        getBlob() { return Promise.resolve({}); },
        upload() { return Promise.resolve({}); },
      },
      qs: (p = {}) => {
        const s = Object.entries(p).map(([k, v]) => `${k}=${v}`).join('&');
        return s ? '?' + s : '';
      },
    },
  });
  return { win, calls, clock };
}

test('take hands over the started request without issuing a second one', async () => {
  const { win, calls } = fresh();
  const started = win.prefetchProjectTasks('p1');
  const taken = win.takeProjectTasks('p1');
  assert.strictEqual(taken, started, 'take should return the prefetched promise itself');
  await taken;
  assert.strictEqual(calls.length, 1, 'exactly one request for one prefetch+take');
});

test('take consumes: a second take refetches rather than replaying', async () => {
  const { win, calls } = fresh();
  win.prefetchProjectTasks('p1');
  await win.takeProjectTasks('p1');
  await win.takeProjectTasks('p1');
  assert.strictEqual(calls.length, 2, 'the re-render must issue its own request');
});

test('take without a prefetch falls back to fetching', async () => {
  const { win, calls } = fresh();
  await win.takeProjectTasks('p1');
  assert.strictEqual(calls.length, 1);
  assert.match(calls[0], /\/projects\/p1\/tasks/);
});

test('a prefetch for one project is never handed to another', async () => {
  const { win, calls } = fresh();
  win.prefetchProjectTasks('p1');      // navigation to p1, abandoned before render
  await win.takeProjectTasks('p2');    // board for p2 opens moments later
  assert.strictEqual(calls.length, 2, 'p2 must issue its own request');
  assert.match(calls[1], /\/projects\/p2\/tasks/, 'p2 must not receive p1 tasks');
});

test('repeated prefetch of the same project does not duplicate the request', async () => {
  const { win, calls } = fresh();
  const a = win.prefetchProjectTasks('p1');
  const b = win.prefetchProjectTasks('p1');
  assert.strictEqual(a, b);
  await win.takeProjectTasks('p1');
  assert.strictEqual(calls.length, 1);
});

test('an entry left unconsumed expires instead of being handed over later', async () => {
  const { win, calls, clock } = fresh();
  win.prefetchProjectTasks('p1');
  clock.now += 60_000;                 // the navigation was abandoned a minute ago
  await win.takeProjectTasks('p1');
  assert.strictEqual(calls.length, 2, 'the stale entry must not be reused');
});

test('an entry is still handed over inside the freshness window', async () => {
  const { win, calls, clock } = fresh();
  win.prefetchProjectTasks('p1');
  clock.now += 500;                    // the render follows the prefetch immediately
  await win.takeProjectTasks('p1');
  assert.strictEqual(calls.length, 1, 'the in-flight request must be reused');
});

test('a rejected prefetch left unconsumed does not go unhandled', async () => {
  const { win } = fresh();
  // start() attaches its own catch; the rejection must still reach a taker.
  win.Prefetch.start('boom', () => Promise.reject(new Error('nope')));
  await assert.rejects(() => win.Prefetch.take('boom', () => Promise.resolve('fallback')),
    /nope/, 'the taker sees the original rejection');
});

// ── tasks.listAll — paging past the API's 200-row page cap ────────────────────
// The API caps a page at 200 and sorts created_at DESC, so one read of a larger
// project silently drops its OLDEST tasks: exactly the epics and stories the
// newer ones hang from. The mindmap did not look truncated when that happened —
// the orphans fell into its "stories without epic" branch, so missing data read
// as a badly-parented backlog.

// pagedFresh builds api.js over an http stub that serves `total` tasks in
// 200-row pages, honouring the page/size the module asks for.
function pagedFresh(total) {
  const calls = [];
  const win = loadModule('api.js', {
    globals: {
      Date: { now: () => 1_000_000 },
      BASE_PATH: '/api/v1',
      Auth: { logout() {} },
      http: {
        get(path) {
          calls.push(path);
          const page = Number(/[?&]page=(\d+)/.exec(path)?.[1] ?? 0);
          const size = Number(/[?&]size=(\d+)/.exec(path)?.[1] ?? 20);
          const start = page * size;
          const rows = [];
          for (let i = start; i < Math.min(start + size, total); i++) rows.push({ id: 't' + i });
          return Promise.resolve(rows);
        },
        post() { return Promise.resolve({}); },
        patch() { return Promise.resolve({}); },
        del() { return Promise.resolve({}); },
        getBlob() { return Promise.resolve({}); },
        upload() { return Promise.resolve({}); },
      },
      qs: (p = {}) => {
        const s = Object.entries(p).map(([k, v]) => `${k}=${v}`).join('&');
        return s ? '?' + s : '';
      },
    },
  });
  return { win, calls };
}

test('listAll costs one request for a project inside a single page', async () => {
  const { win, calls } = pagedFresh(42);
  const tasks = await win.api.tasks.listAll('p1');
  assert.strictEqual(tasks.length, 42);
  assert.strictEqual(calls.length, 1, 'the common case must not pay for paging');
});

test('listAll pages until the project is covered', async () => {
  const { win, calls } = pagedFresh(430);
  const tasks = await win.api.tasks.listAll('p1');
  assert.strictEqual(tasks.length, 430, 'no task may be dropped');
  assert.strictEqual(calls.length, 3, '200 + 200 + 30');
  assert.strictEqual(new Set(tasks.map(t => t.id)).size, 430, 'and none duplicated');
});

test('listAll stops on an exactly-full last page', async () => {
  // 400 rows means page 2 comes back empty — the loop must end there rather
  // than treating "empty" as a reason to keep going.
  const { win, calls } = pagedFresh(400);
  const tasks = await win.api.tasks.listAll('p1');
  assert.strictEqual(tasks.length, 400);
  assert.strictEqual(calls.length, 3, 'two full pages plus the empty probe');
});

test('listAll is bounded, so a server answering full pages forever cannot spin', async () => {
  const { win, calls } = pagedFresh(Infinity);
  const tasks = await win.api.tasks.listAll('p1');
  assert.strictEqual(calls.length, 25, 'MAX_PAGES caps the loop');
  assert.strictEqual(tasks.length, 25 * 200);
});

test('listAll passes its filters through to every page', async () => {
  const { win, calls } = pagedFresh(250);
  await win.api.tasks.listAll('p1', { status: 'ARCHIVED' });
  assert.strictEqual(calls.length, 2);
  for (const call of calls) {
    assert.match(call, /status=ARCHIVED/, 'the filter must survive paging');
  }
});
