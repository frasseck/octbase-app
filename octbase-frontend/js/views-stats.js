import { t } from '@octbase/shared/i18n.js';
import { STATUS_META, TYPE_META, estimationEnabled, priorityMeta } from '@octbase/shared/meta.js';
import { api } from './api.js';
import { el, esc, fmtDate, html, raw } from './framework.js';
import { apiErrorMessage } from './http.js';
import { Views } from './registry.js';
import { S } from './state.js';
import { burndownUnitFor, renderBurndownChart, renderVelocityChart, reportEffortLabel } from './views-agile.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and it exports nothing — main.js side-effect-imports it for what it
// registers as it evaluates. Imports carry the dependencies, so there is no
// load order to keep in step (js/README.md).
//
// ═══════════════════════════════════════════════════════════
// PROJECT STATISTICS — the project-manager overview
// ═══════════════════════════════════════════════════════════
// One page answering the questions asked before a standup or a steering
// meeting: how much is open, what is late, how fast work is finishing, who is
// carrying it, and — where the project estimates — how the current sprint is
// burning down in effort rather than ticket count.
//
// Reached from the chart icon beside the project-settings gear in the topbar,
// not from the sidebar: the sidebar lists the places work is *done*; this is a
// view onto the project as a whole. The registry entry therefore has no
// `sidebar` key (that is the documented "routable-only view" shape — see
// registry.js), so the route /projects/:id/statistics works, is bookmarkable,
// and adds no nav clutter.
//
// Charts are hand-rolled inline SVG in the house style established by the
// sprint reports (views-agile.js): theme tokens via CSS variables, no chart
// library, a <title> on every mark for the hover read-out. Every chart here is
// a SINGLE series drawn in one hue — the categories are told apart by their
// row labels, never by colour alone — except the burndown and velocity, which
// carry two series and therefore carry a legend. The four themes redefine
// --md-primary, so a hard-coded categorical palette would break in three of
// them; single-hue magnitude bars are the only encoding that survives.

// statsUnitLabel is the axis word for the project's effort unit.
function statsUnitLabel() { return reportEffortLabel(S.project); }

// renderStatistics builds the whole page. Data comes from three reads issued
// together: the statistics aggregate, the velocity series, and the sprint list
// (needed only to pick a burndown subject when no sprint is running).
async function renderStatistics() {
  const c = el('#content');
  if (!c || !S.project) return;
  const gen = S.contentGen;
  const pid = S.project.id;

  const [stats, velocity, sprints] = await Promise.all([
    api.reports.statistics(pid),
    api.reports.velocity(pid).catch(() => []),
    api.sprints.list(pid).catch(() => []),
  ]);
  if (gen !== S.contentGen) return;

  // The burndown needs a started sprint. Prefer the running one; fall back to
  // the most recently completed, so a project between sprints still shows its
  // last one instead of an empty panel.
  const subject = stats.sprint
    ? { id: stats.sprint.sprintId, name: stats.sprint.name }
    : lastCompletedSprint(sprints);

  c.innerHTML = html`
    <div class="stats-page">
      <h2 class="stats-page-title">${t('stats.title')}</h2>
      ${raw(renderStatTiles(stats))}
      <div class="stats-grid">
        ${raw(statsCard('', `<div id="stats-burndown">
          <div class="report-title">${t('stats.burndownSection')}</div>
          <div class="text-muted text-sm">${subject ? t('app.loadingEllipsis') : t('stats.noSprint')}</div>
        </div>`, 'wide'))}
        ${raw(statsCard(t('stats.throughputTitle'), renderThroughputChart(stats)))}
        ${raw(statsCard(t('stats.cycleTimeTitle'), renderCycleTime(stats)))}
        ${raw(statsCard(t('stats.byStatus'), renderDistribution(stats.tasks.byStatus, k => statusLabel(k))))}
        ${raw(statsCard(t('stats.byType'), renderDistribution(stats.tasks.byType, k => typeLabel(k))))}
        ${raw(statsCard(t('stats.byPriority'), renderDistribution(stats.tasks.byPriority, k => priorityLabel(k)), '', t('stats.byPriorityHint')))}
        ${raw(statsCard(t('stats.workloadTitle'), renderWorkload(stats)))}
        ${raw(statsCard(t('stats.velocityTitle'), renderVelocityChart(velocity)))}
        ${raw(statsCard(t('stats.planTitle'), renderPlanSummary(stats)))}
      </div>
    </div>`;

  // The burndown is a second round trip (it is per sprint, not per project),
  // so the page paints first and the chart drops in when it arrives.
  if (subject) loadStatsBurndown(subject.id, gen);
}

// lastCompletedSprint picks the newest COMPLETED sprint by end date, or null.
function lastCompletedSprint(sprints) {
  const done = (sprints || []).filter(s => s.status === 'COMPLETED' && s.endDate);
  if (!done.length) return null;
  return done.slice().sort((a, b) => (a.endDate < b.endDate ? 1 : -1))[0];
}

// loadStatsBurndown fetches and paints the burndown for one sprint. It asks
// for effort wherever the project estimates — that is what "activate
// estimation" is *for* — and falls back to the task count if the server
// refuses the unit (a project switched to NONE between the two requests).
async function loadStatsBurndown(sprintId, gen) {
  const unit = estimationEnabled(S.project) ? burndownUnitFor(S.project) : 'tasks';
  let burndown;
  try {
    burndown = await api.sprints.burndown(sprintId, unit);
  } catch (e) {
    if (unit !== 'tasks' && e && e.code === 'ESTIMATION_UNIT_INACTIVE') {
      try { burndown = await api.sprints.burndown(sprintId, 'tasks'); } catch (e2) { return failBurndown(e2, gen); }
    } else {
      return failBurndown(e, gen);
    }
  }
  if (gen !== S.contentGen) return;
  const target = el('#stats-burndown');
  if (target) target.innerHTML = renderBurndownChart(burndown) + burndownFooter(burndown);
}

function failBurndown(e, gen) {
  if (gen !== S.contentGen) return;
  const host = el('#stats-burndown');
  if (host) host.innerHTML = html`<div class="text-muted text-sm">${apiErrorMessage(e)}</div>`;
}

// burndownFooter names the sprint the chart belongs to and, for a finished
// sprint, says so — otherwise a completed sprint's flat tail reads as a team
// that stopped working.
function burndownFooter(bd) {
  const label = bd.status === 'COMPLETED'
    ? t('stats.burndownOfCompleted', { name: bd.name })
    : t('stats.burndownOf', { name: bd.name });
  return html`<div class="report-note">${label}</div>`;
}

// ── The headline tiles ──────────────────────────────────────────────────────
// A single number with a label is a stat tile, not a chart: there is nothing
// to compare it against inside the tile. Attention-worthy states (overdue
// work, an overrunning sprint) carry a status colour *and* their label, never
// colour alone.

function renderStatTiles(stats) {
  const s = stats.tasks;
  const tiles = [
    statTile(s.open, t('stats.tileOpen'), s.total ? t('stats.ofTotal', { total: s.total }) : ''),
    statTile(s.inProgress, t('stats.tileInProgress')),
    statTile(s.completedLast30, t('stats.tileCompleted30'), t('stats.created30', { count: s.createdLast30 })),
    statTile(s.overdue, t('stats.tileOverdue'), '', s.overdue > 0 ? 'warn' : ''),
    statTile(s.unassigned, t('stats.tileUnassigned'), '', s.unassigned > 0 ? 'muted-accent' : ''),
  ];
  if (stats.effort) {
    tiles.push(statTile(
      stats.effort.remaining,
      t('stats.tileEffortRemaining', { unit: statsUnitLabel() }),
      t('stats.effortDoneOfTotal', { done: stats.effort.done, total: stats.effort.total })));
    if (stats.effort.unestimated > 0) {
      tiles.push(statTile(stats.effort.unestimated, t('stats.tileUnestimated'), '', 'warn'));
    }
  }
  if (stats.sprint) {
    const sp = stats.sprint;
    const left = sp.daysRemaining === null || sp.daysRemaining === undefined
      ? '' : t('stats.sprintDaysLeft', { count: sp.daysRemaining });
    tiles.push(statTile(`${sp.completed}/${sp.committed}`, t('stats.tileSprint', { name: sp.name }), left,
      sp.daysRemaining === 0 ? 'warn' : ''));
  }
  return `<div class="stat-tiles">${tiles.join('')}</div>`;
}

// statTile renders one tile. `tone` adds a status accent; it is always
// accompanied by the tile's own label, so the colour is reinforcement rather
// than the only signal.
function statTile(value, label, hint = '', tone = '') {
  return html`<div class="stat-tile ${tone ? 'stat-tile-' + tone : ''}">
      <div class="stat-tile-value">${value}</div>
      <div class="stat-tile-label">${label}</div>
      ${raw(hint ? html`<div class="stat-tile-hint">${hint}</div>` : '')}
    </div>`;
}

// statsCard wraps a chart in the page's card chrome. `body` is already-built
// trusted HTML from the render helpers below. An empty title means the body
// brings its own heading — the burndown does, because the chart names the unit
// it is measuring and a card title above it would only say it twice.
function statsCard(title, body, cls = '', hint = '') {
  return html`<section class="stats-card ${cls}">
      ${raw(title ? html`<div class="report-title">${title}</div>` : '')}
      ${raw(hint ? html`<div class="stats-card-hint">${hint}</div>` : '')}
      ${raw(body)}
    </section>`;
}

// ── Distributions (status / type / priority) ────────────────────────────────
// Horizontal bars, one hue: the quantity is the message and the row label
// carries the identity. Empty buckets are kept (the API sends them) so the
// chart holds its shape as work moves between states.

function renderDistribution(entries, labelFor) {
  const list = entries || [];
  const max = Math.max(1, ...list.map(e => e.count));
  const total = list.reduce((sum, e) => sum + e.count, 0);
  if (!total) return html`<div class="text-muted text-sm">${t('stats.noData')}</div>`;
  const rows = list.map(e => html`
    <div class="dist-row">
      <div class="dist-label">${labelFor(e.key)}</div>
      <div class="dist-track" role="img" aria-label="${labelFor(e.key)}: ${e.count}">
        <div class="dist-bar" style="width:${Math.round((e.count / max) * 100)}%"></div>
      </div>
      <div class="dist-value">${e.count}</div>
    </div>`);
  return `<div class="dist-chart">${rows.join('')}</div>`;
}

// The bucket labels come from the canonical metadata maps in meta.js, so the
// chart uses the same words as every badge in the app. Project-specific values
// the maps do not know (a custom priority, a board-lane status) fall back to
// the raw key — which is already the human-readable name for both.
function statusLabel(key)   { return (STATUS_META[key] || {}).label || key; }
function typeLabel(key)     { return (TYPE_META[key] || {}).label || key; }
function priorityLabel(key) { return priorityMeta(key).label; }

// ── Throughput ──────────────────────────────────────────────────────────────
// Vertical bars, one per ISO week, oldest left. A quiet week is a zero-height
// bar rather than a missing one, so the gaps are visible as gaps.

function renderThroughputChart(stats) {
  const weeks = stats.throughput || [];
  if (!weeks.length) return html`<div class="text-muted text-sm">${t('stats.noData')}</div>`;
  const effort = !!stats.effort;
  const valueOf = w => (effort && w.effort !== null && w.effort !== undefined ? w.effort : w.completed);
  const max = Math.max(1, ...weeks.map(valueOf));
  // A narrower viewBox than the sprint reports' 520: this chart sits in a
  // grid column, and the SVG scales to the card, so a wide viewBox would
  // shrink the 10px axis text to something unreadable.
  const W = 340, H = 170, L = 26, R = 8, T = 10, B = 26;
  const plotW = W - L - R, plotH = H - T - B;
  const y = v => T + plotH - (v / max) * plotH;
  const slot = plotW / weeks.length;
  const barW = Math.min(34, slot - 6);

  const step = Math.max(1, Math.ceil(max / 4));
  let grid = '', yLabels = '';
  for (let v = 0; v <= max; v += step) {
    grid += `<line x1="${L}" y1="${y(v)}" x2="${W - R}" y2="${y(v)}" class="report-grid"/>`;
    yLabels += `<text x="${L - 6}" y="${y(v) + 3}" text-anchor="end" class="report-axis-label">${v}</text>`;
  }

  let bars = '', xLabels = '';
  weeks.forEach((w, i) => {
    const v = valueOf(w);
    const cx = L + slot * i + slot / 2;
    const barH = Math.max(0, T + plotH - y(v));
    const tip = effort
      ? t('stats.throughputTipEffort', { week: w.weekStart, count: w.completed, effort: w.effort === null || w.effort === undefined ? 0 : w.effort, unit: statsUnitLabel() })
      : t('stats.throughputTip', { week: w.weekStart, count: w.completed });
    // rx rounds the data end; a zero-height bar draws nothing at all rather
    // than a stub that would read as "one delivered".
    bars += `<g><title>${esc(tip)}</title>${barH > 0
      ? `<rect x="${cx - barW / 2}" y="${y(v)}" width="${barW}" height="${barH}" rx="4" class="report-bar-completed"/>`
      : ''}</g>`;
    xLabels += `<text x="${cx}" y="${H - 10}" text-anchor="middle" class="report-axis-label">${esc(w.weekStart.slice(5))}</text>`;
  });

  const caption = effort ? t('stats.throughputEffortCaption', { unit: statsUnitLabel() }) : t('stats.throughputCaption');
  return `
    <svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(caption)}">
      ${grid}${yLabels}${xLabels}${bars}
    </svg>
    ${html`<div class="report-note">${caption}</div>`}`;
}

// ── Cycle time ──────────────────────────────────────────────────────────────
// Two numbers, not a chart. Median sits beside the average because one
// six-month straggler drags the average somewhere no real task lives.

function renderCycleTime(stats) {
  const ct = stats.cycleTime || {};
  if (!ct.sampleSize) return html`<div class="text-muted text-sm">${t('stats.noCycleTime')}</div>`;
  return html`
    <div class="stat-pair">
      <div><div class="stat-tile-value">${ct.medianDays}</div><div class="stat-tile-label">${t('stats.cycleMedian')}</div></div>
      <div><div class="stat-tile-value">${ct.averageDays}</div><div class="stat-tile-label">${t('stats.cycleAverage')}</div></div>
    </div>
    <div class="report-note">${t('stats.cycleSample', { count: ct.sampleSize })}</div>`;
}

// ── Workload ────────────────────────────────────────────────────────────────

function renderWorkload(stats) {
  const list = stats.workload || [];
  if (!list.length) return html`<div class="text-muted text-sm">${t('stats.noWorkload')}</div>`;
  const max = Math.max(1, ...list.map(w => w.open));
  const rows = list.map(w => {
    const effort = w.effort === null || w.effort === undefined ? '' : ` · ${w.effort} ${statsUnitLabel()}`;
    return html`
      <div class="dist-row">
        <div class="dist-label">${w.name || t('stats.unknownUser')}</div>
        <div class="dist-track" role="img" aria-label="${w.name}: ${w.open}">
          <div class="dist-bar" style="width:${Math.round((w.open / max) * 100)}%"></div>
        </div>
        <div class="dist-value">${w.open}${effort}</div>
      </div>`;
  });
  const unassigned = stats.tasks.unassigned > 0
    ? html`<div class="report-note">${t('stats.workloadUnassigned', { count: stats.tasks.unassigned })}</div>`
    : '';
  return `<div class="dist-chart">${rows.join('')}</div>${unassigned}`;
}

// ── Release plan ────────────────────────────────────────────────────────────

function renderPlanSummary(stats) {
  const r = stats.releases || {};
  const next = r.nextDue
    ? html`<div class="stats-plan-row">${t('stats.nextRelease')}<strong>${r.nextDueName || ''}</strong><span class="text-muted">${fmtDate(r.nextDue)}</span></div>`
    : html`<div class="stats-plan-row text-muted">${t('stats.noOpenRelease')}</div>`;
  const overdue = r.overdueOpen > 0
    ? html`<div class="report-note">${t('stats.releasesOverdue', { count: r.overdueOpen })}</div>`
    : '';
  return html`
    <div class="stat-pair">
      <div><div class="stat-tile-value">${r.open || 0}</div><div class="stat-tile-label">${t('stats.releasesOpen')}</div></div>
      <div><div class="stat-tile-value">${r.closed || 0}</div><div class="stat-tile-label">${t('stats.releasesClosed')}</div></div>
    </div>
    ${raw(next)}${raw(overdue)}`;
}

// ── view registration (see registry.js for the contract) ──
// No `sidebar` key: this view is routable and reached from the topbar chart
// icon only. `liveRefresh` is on because the numbers are a projection of live
// project content — a co-worker finishing a task makes this page stale.
Views.register('statistics', {
  render: renderStatistics,
  liveRefresh: true,
});

// This file exports nothing: it registers its view above and is reached only
// through the registry, exactly like views-mindmap.js (see js/README.md,
// "File scope & exports" — zero-export files are valid and desirable).
