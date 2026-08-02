// Unit tests for the report charts' unit handling. Plain Node, no build:
//   npm run test:unit -- views-agile.test.js
//
// The charts are strings, so their *geometry* is not what these assert — what
// they assert is the thing an effort burndown can silently get wrong: which
// unit the chart claims to be showing, and whether it admits that some of the
// committed work carries no estimate at all. Both are read from the API's
// response rather than from what the client asked for, so a server that
// ignored ?unit= cannot make the chart lie.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// views-agile.js registers its views at load time and calls t()/icon()/esc()
// while rendering. Stub exactly that surface: the tests are about which branch
// runs, not about the wording, so t() echoes its key and its params.
function fresh() {
  return loadModule('views-agile.js', {
    globals: {
      Views: { register() {} },
      t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
      icon: () => '<svg class="icon-svg"></svg>',
      esc: (s) => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'),
      S: { project: { id: 'p1', estimationUnit: 'POINTS' } },
      estimationUnit: (p) => (p && p.estimationUnit) || 'NONE',
      estimationEnabled: (p) => ((p && p.estimationUnit) || 'NONE') !== 'NONE',
      el: () => null,
      api: {},
      apiErrorMessage: (e) => String(e),
    },
  });
}

// burndown builds a minimal response of the shape the API returns.
function burndown(extra = {}) {
  return Object.assign({
    sprintId: 's1', name: 'Sprint 1', status: 'ACTIVE',
    startDate: '2026-07-01', endDate: '2026-07-03', committed: 6,
    points: [
      { date: '2026-07-01', remaining: 6, ideal: 4 },
      { date: '2026-07-02', remaining: 3, ideal: 2 },
      { date: '2026-07-03', remaining: null, ideal: 0 },
    ],
  }, extra);
}

test('the burndown title comes from the unit the API echoed back, not the request', () => {
  const { renderBurndownChart } = fresh();
  // No unit echoed = the task-counting series every client saw before effort
  // existed, whatever the caller believed it asked for.
  assert.match(renderBurndownChart(burndown()), /report\.burndownTitle(?!Points|Hours)/);
  assert.match(renderBurndownChart(burndown({ unit: 'points' })), /report\.burndownTitlePoints/);
  assert.match(renderBurndownChart(burndown({ unit: 'hours' })), /report\.burndownTitleHours/);
});

test('the burndown flags unestimated committed tasks, and stays quiet when there are none', () => {
  const { renderBurndownChart } = fresh();
  const withNone = renderBurndownChart(burndown({ unit: 'points', unestimated: 0 }));
  assert.ok(!withNone.includes('report.unestimated'),
    'a fully estimated sprint must not carry an unestimated warning');
  const withSome = renderBurndownChart(burndown({ unit: 'points', unestimated: 3 }));
  assert.match(withSome, /report\.unestimated:\{"count":3\}/);
});

test('a fractional hours axis is not rounded away to whole numbers', () => {
  const { renderBurndownChart } = fresh();
  // 3.75 committed hours over 4 gridline steps: labelling 0,1,2,3,4 would put
  // the top of the series off the top of the axis and mislabel every step.
  const svg = renderBurndownChart(burndown({
    unit: 'hours', committed: 3.75,
    points: [{ date: '2026-07-01', remaining: 3.75, ideal: 0 }],
  }));
  assert.match(svg, /class="report-axis-label">0\.94</, 'expected a fractional axis step');
});

test('burndown x-axis labels never print on top of each other', () => {
  const { renderBurndownChart } = fresh();
  // The reported bug: a 26-day sprint (2026-07-21 → 2026-08-15). The regular
  // ticks fall on indices 0/6/12/18/24 and the forced last-day tick on 25, one
  // day and ~18px apart, so "08-14" and "08-15" printed over each other.
  const days = [];
  for (let d = 0; d < 26; d++) {
    const t0 = new Date(Date.UTC(2026, 6, 21) + d * 86400000);
    days.push({ date: t0.toISOString().slice(0, 10), remaining: 30 - d, ideal: 30 - d });
  }
  const svg = renderBurndownChart(burndown({ committed: 30, points: days }));

  const xs = [...svg.matchAll(/<text x="([\d.]+)" y="\d+" text-anchor="middle"/g)]
    .map(m => Number(m[1]));
  assert.ok(xs.length >= 2, 'expected several x-axis labels');
  for (let i = 1; i < xs.length; i++) {
    assert.ok(xs[i] - xs[i - 1] >= 34,
      `x labels ${i - 1} and ${i} are ${xs[i] - xs[i - 1]}px apart — they overlap`);
  }
  // The last day must still be labelled; dropping the collision must not cost
  // the axis its end point.
  assert.match(svg, />08-15</, 'the last sprint day must still be labelled');
  assert.ok(!svg.includes('>08-14<'), 'the colliding neighbour should be dropped, not the forced last tick');
});

// velocity builds one completed-sprint entry.
function entry(name, extra = {}) {
  return Object.assign({
    sprintId: 's-' + name, name, endDate: '2026-07-01', committed: 5, completed: 4,
    committedEstimate: null, completedEstimate: null, estimateUnit: null,
  }, extra);
}

test('velocity measures effort only when the whole history shares one unit', () => {
  const { renderVelocityChart } = fresh();

  const points = [
    entry('S1', { committedEstimate: 20, completedEstimate: 18, estimateUnit: 'POINTS' }),
    entry('S2', { committedEstimate: 26, completedEstimate: 21, estimateUnit: 'POINTS' }),
  ];
  const uniform = renderVelocityChart(points);
  assert.match(uniform, /report\.velocityTitlePoints/);
  assert.ok(!uniform.includes('report.velocityMixedUnits'), 'a single-unit history is not mixed');

  // A project that switched POINTS → HOURS: 26 points and 26 hours are not the
  // same bar, so the chart must fall back to counting and say why.
  const mixed = renderVelocityChart([
    points[0],
    entry('S2', { committedEstimate: 26, completedEstimate: 21, estimateUnit: 'HOURS' }),
  ]);
  assert.match(mixed, /report\.velocityTitle(?!Points|Hours)/);
  assert.match(mixed, /report\.velocityMixedUnits/);

  // A history where an older sprint was completed before the snapshot existed
  // has nothing to sum for it — count tasks rather than plot a hole.
  const partial = renderVelocityChart([entry('S0'), points[0]]);
  assert.match(partial, /report\.velocityTitle(?!Points|Hours)/);
});

test('velocity keeps working for a project that has never estimated', () => {
  const { renderVelocityChart } = fresh();
  const out = renderVelocityChart([entry('S1'), entry('S2')]);
  assert.match(out, /report\.velocityTitle(?!Points|Hours)/);
  assert.match(out, /report-bar-committed/);
  assert.ok(!out.includes('report.velocityMixedUnits'));
});

test('the unit toggle appears only where the project estimates', () => {
  const off = loadModule('views-agile.js', {
    globals: {
      Views: { register() {} },
      t: (k) => k, icon: () => '', esc: (s) => String(s),
      S: { project: { id: 'p1', estimationUnit: 'NONE' } },
      estimationUnit: (p) => (p && p.estimationUnit) || 'NONE',
      estimationEnabled: (p) => ((p && p.estimationUnit) || 'NONE') !== 'NONE',
      el: () => null, api: {}, apiErrorMessage: String,
    },
  });
  // burndownUnitFor is the seam both the toggle and the statistics page use to
  // decide what to request; NONE must map to "no effort unit exists".
  assert.strictEqual(off.burndownUnitFor(off.S.project), '');

  const on = fresh();
  assert.strictEqual(on.burndownUnitFor({ estimationUnit: 'POINTS' }), 'points');
  assert.strictEqual(on.burndownUnitFor({ estimationUnit: 'HOURS' }), 'hours');
});
