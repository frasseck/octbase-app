import { t } from '@octbase/shared/i18n.js';
import { estimationEnabled, estimationUnit } from '@octbase/shared/meta.js';
import { api } from './api.js';
import { _A0, _A1, _A2, registerActions } from './delegation.js';
import { confirmDelete, el, esc, fmtDate, releaseName, showModal, toast } from './framework.js';
import { apiErrorMessage } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { Views } from './registry.js';
import { S } from './state.js';
import { contentToolbar, renderSidebar } from './views-shell.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
async function renderReleases() {
  // The releases and the task list are independent reads that were awaited one
  // after the other — two serial round trips before anything could paint.
  //
  // Only the serialization is fixed here. Reusing the copy loadProject already
  // put in S.releases was tried and reverted: loadProject runs once per
  // navigation into the project, and this view can render arbitrarily later and
  // after arbitrary writes (this client's, another client's, a direct API call),
  // so any cached copy can be missing a release that exists. It is also what the
  // five release-mutation handlers below rely on — each writes and then calls
  // straight back into this function precisely to see the server's version.
  // Four e2e tests in test_milestones.py fail on the stale copy; a user would see
  // a release they had just created simply not appear.
  const [releases, tasks] = await Promise.all([
    api.releases.list(S.project.id),
    // Every page — these tasks are counted per release, and a truncated set
    // reports counts that are quietly too low.
    api.tasks.listAll(S.project.id).catch(() => []),
  ]);
  S.releases = releases;
  const releaseCounts = new Map();
  tasks.filter(task => task.releaseId && task.status !== 'ARCHIVED').forEach(task => {
    const counts = releaseCounts.get(task.releaseId) || { total:0, done:0 };
    counts.total += 1;
    if (task.status === 'DONE') counts.done += 1;
    releaseCounts.set(task.releaseId, counts);
  });
  const c = el('#content');
  if(!S.releases.length) {
    // Same gate as viewCreateButton — the empty state replaces that button, so
    // a viewer would otherwise be offered "New release" here and nowhere else.
    c.innerHTML=`<div class="empty"><div class="empty-icon">${icon('release',{size:'hero'})}</div><div class="empty-title">${t('release.emptyTitle')}</div>${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary" data-act="showCreateRelease">${icon('add',{size:'md'})} ${t('release.new')}</button>`}</div>`;
    return;
  }
  c.innerHTML=`${contentToolbar('releases', false)}<div class="releases-list grid-2col">
    ${S.releases.map(m => {
      const counts = releaseCounts.get(m.id) || { total:0, done:0 };
      const percent = counts.total ? Math.round((counts.done / counts.total) * 100) : 0;
      return `
      <div class="release-card">
        <div class="release-header">
          <div>
            <div class="release-name">${esc(m.name)}</div>
            <div class="text-muted text-sm">${m.dueDate?t('release.due',{date:fmtDate(m.dueDate)}):t('release.noDueDate')}</div>
          </div>
          <span class="badge ${m.status==='CLOSED'?'badge-done':'badge-in-progress'}">${t('release.status.'+m.status)}</span>
        </div>
        ${m.goal?`<div class="release-goal">${esc(m.goal)}</div>`:''}
        <div class="release-progress-wrap">
          <div class="release-progress-meta">
            <span>${t('task.progressCount',{done:counts.done,total:counts.total})}</span>
            <span>${percent}%</span>
          </div>
          <progress class="release-progress" max="100" value="${percent}"></progress>
        </div>
        <div class="release-actions">
          <button class="btn btn-secondary btn-sm" data-act="editRelease" data-a0="${esc(m.id)}">${t('form.edit')}</button>
          ${m.status==='PLANNED'
            ?`<button class="btn btn-success btn-sm" data-act="closeRelease" data-a0="${esc(m.id)}">${t('release.ship')}</button>`
            :`<button class="btn btn-secondary btn-sm" data-act="reopenRelease" data-a0="${esc(m.id)}">${t('task.reopen')}</button>`}
          <button class="btn btn-danger btn-sm" data-act="deleteRelease" data-a0="${esc(m.id)}" data-a1="${esc((m.name))}">${t('form.delete')}</button>
        </div>
      </div>`;
    }).join('')}
  </div>`;
}

function showCreateRelease() {
  showModal(t('release.new'), `
    <div class="form-group"><label class="form-label" for="ms-name">${t('form.name')}</label><input class="form-input" id="ms-name" placeholder="${t('release.namePlaceholder')}" autofocus></div>
    <div class="form-group"><label class="form-label" for="ms-goal">${t('release.goal')}</label><textarea class="form-input" id="ms-goal" rows="2" placeholder="${t('release.goalPlaceholder')}"></textarea></div>
    <div class="form-group"><label class="form-label" for="ms-due">${t('task.dueDateLabel')}</label><input class="form-input" id="ms-due" type="date"></div>`,
    async () => {
      const name = el('#ms-name')?.value?.trim();
      if(!name) throw new Error(t('validation.nameRequired'));
      const d = {name, goal: el('#ms-goal')?.value||''};
      const due = el('#ms-due')?.value;
      if(due) d.dueDate = due;
      await api.releases.create(S.project.id, d);
      toast(t('release.created'),'success');
      await renderReleases();
    });
}

function editRelease(id) {
  const m = S.releases.find(m=>m.id===id);
  if(!m) return;
  showModal(t('release.edit'), `
    <div class="form-group"><label class="form-label" for="ms-name">${t('form.name')}</label><input class="form-input" id="ms-name" value="${esc(m.name)}"></div>
    <div class="form-group"><label class="form-label" for="ms-goal">${t('release.goal')}</label><textarea class="form-input" id="ms-goal" rows="2">${esc(m.goal)}</textarea></div>
    <div class="form-group"><label class="form-label" for="ms-due">${t('task.dueDateLabel')}</label><input class="form-input" id="ms-due" type="date" value="${m.dueDate?m.dueDate.slice(0,10):''}"></div>`,
    async () => {
      // Carry the loaded snapshot's version so a stale edit gets a 409 instead
      // of overwriting a concurrent editor (renderReleases refetches after).
      const d = { name:el('#ms-name')?.value?.trim(), goal:el('#ms-goal')?.value||'', version: m.version };
      const due = el('#ms-due')?.value;
      if(due) d.dueDate = due;
      await api.releases.update(id, d);
      toast(t('release.updated'),'success');
      await renderReleases();
    });
}

async function closeRelease(id) {
  try { await api.releases.close(id); toast(t('release.closed'),'success'); await renderReleases(); } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function reopenRelease(id) {
  try { await api.releases.reopen(id); toast(t('release.reopened'),'success'); await renderReleases(); } catch(e) { toast(apiErrorMessage(e),'error'); }
}
function deleteRelease(id,name) {
  confirmDelete(t('release.deleteTitle'), t('form.deleteNamedConfirm',{name}), async()=>{
    await api.releases.del(id); toast(t('form.deleted'),'success'); await renderReleases();
  });
}

// ═══════════════════════════════════════════════════════════
// SPRINTS
// ═══════════════════════════════════════════════════════════
async function renderSprints() {
  S.sprints = await api.sprints.list(S.project.id);
  const c = el('#content');

  if (!S.sprints.length) {
    c.innerHTML = `<div class="empty"><div class="empty-icon">${icon('sprint',{size:'hero'})}</div><div class="empty-title">${t('sprint.emptyTitle')}</div><p class="empty-body">${t('sprint.emptyBody')}</p>${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary" data-act="showCreateSprint">${icon('add',{size:'md'})} ${t('sprint.new')}</button>`}</div>`;
    return;
  }

  const activeSprints    = S.sprints.filter(s => s.status === 'ACTIVE');
  const plannedSprints   = S.sprints.filter(s => s.status === 'PLANNED');
  const completedSprints = S.sprints.filter(s => s.status === 'COMPLETED');

  function sprintCard(s) {
    // committedCount/completedCount are the board scope: for active/planned
    // sprints the API fills them live from current board membership, and for a
    // completed sprint they hold the totals snapshotted at completion. Either
    // way they match what was on the board, so a closed sprint reads e.g. 2/5.
    const cnt = { total: s.committedCount || 0, done: s.completedCount || 0 };
    const percent = cnt.total ? Math.round((cnt.done / cnt.total) * 100) : 0;
    const releaseLabel = s.releaseId ? ` → ${esc(releaseName(s.releaseId))}` : '';
    const dateRange = (s.startDate || s.endDate)
      ? `${s.startDate ? fmtDate(s.startDate) : '?'} – ${s.endDate ? fmtDate(s.endDate) : '?'}`
      : t('sprint.noDatesSet');
    const statusBadge = {
      ACTIVE:    `<span class="badge badge-in-progress">${t('sprint.status.ACTIVE')}</span>`,
      PLANNED:   `<span class="badge badge-planned">${t('sprint.status.PLANNED')}</span>`,
      COMPLETED: `<span class="badge badge-done">${t('sprint.status.COMPLETED')}</span>`,
    }[s.status] || '';

    // PLANNED sprints open their board to plan (drag tasks from the backlog);
    // ACTIVE sprints open it to work the lanes. The board label reflects which.
    const boardBtn = `<button class="btn btn-secondary btn-sm" data-act="openSprintBoard" data-a0="${esc(s.id)}">${s.status === 'PLANNED' ? t('sprint.planBoard') : t('sprint.openBoard')}</button>`;
    // Reports need a started sprint (the burndown endpoint answers 422 for
    // PLANNED sprints), so the button renders for ACTIVE and COMPLETED only.
    const reportBtn = s.status === 'PLANNED' ? '' :
      `<button class="btn btn-secondary btn-sm" data-act="toggleSprintReport" data-a0="${esc(s.id)}" aria-expanded="false" aria-controls="sprint-report-${esc(s.id)}">${t('report.button')}</button>`;
    const actions = s.status === 'COMPLETED' ? `
      ${reportBtn}
      <button class="btn btn-danger btn-sm" data-act="deleteSprint" data-a0="${esc(s.id)}" data-a1="${esc((s.name))}">${t('form.delete')}</button>` : `
      ${boardBtn}
      ${reportBtn}
      <button class="btn btn-secondary btn-sm" data-act="editSprint" data-a0="${esc(s.id)}">${t('form.edit')}</button>
      ${s.status === 'PLANNED'
        ? `<button class="btn btn-success btn-sm" data-act="startSprint" data-a0="${esc(s.id)}">${t('sprint.start')}</button>`
        : `<button class="btn btn-warning btn-sm" data-act="completeSprint" data-a0="${esc(s.id)}" data-a1="${esc((s.name))}">${t('sprint.complete')}</button>`}
      <button class="btn btn-danger btn-sm" data-act="deleteSprint" data-a0="${esc(s.id)}" data-a1="${esc((s.name))}">${t('form.delete')}</button>`;

    return `
      <div class="sprint-card ${s.status === 'ACTIVE' ? 'sprint-card-active' : ''}">
        <div class="sprint-header">
          <div>
            <div class="sprint-name">${esc(s.name)}${releaseLabel ? `<span class="sprint-release-label">${releaseLabel}</span>` : ''}</div>
            <div class="text-muted text-sm">${dateRange}</div>
          </div>
          ${statusBadge}
        </div>
        ${s.goal ? `<div class="sprint-goal">${esc(s.goal)}</div>` : ''}
        <div class="release-progress-wrap">
          <div class="release-progress-meta">
            <span>${t('task.progressCount',{done:cnt.done,total:cnt.total})}</span>
            <span>${percent}%</span>
          </div>
          <progress class="release-progress" max="100" value="${percent}"></progress>
        </div>
        <div class="sprint-actions">${actions}</div>
        <div class="sprint-report hidden" id="sprint-report-${esc(s.id)}"></div>
      </div>`;
  }

  let html = contentToolbar('sprints', false) + '<div class="sprints-list grid-2col">';
  if (activeSprints.length) {
    html += `<div class="sprint-section-label">${t('sprint.sectionActive')}</div>`;
    html += activeSprints.map(sprintCard).join('');
  }
  if (plannedSprints.length) {
    html += `<div class="sprint-section-label">${t('sprint.sectionPlanned')}</div>`;
    html += plannedSprints.map(sprintCard).join('');
  }
  if (completedSprints.length) {
    html += `<div class="sprint-section-label">${t('sprint.sectionCompleted')}</div>`;
    html += completedSprints.map(sprintCard).join('');
  }
  html += '</div>';
  c.innerHTML = html;
}

function showCreateSprint() {
  showModal(t('sprint.new'), `
    <div class="form-group"><label class="form-label" for="sp-name">${t('form.name')}</label><input class="form-input" id="sp-name" placeholder="${t('sprint.namePlaceholder')}" autofocus></div>
    <div class="form-group"><label class="form-label" for="sp-goal">${t('sprint.goal')}</label><textarea class="form-input" id="sp-goal" rows="2" placeholder="${t('sprint.goalPlaceholder')}"></textarea></div>
    <div class="form-group"><label class="form-label" for="sp-start">${t('sprint.startDate')}</label><input class="form-input" id="sp-start" type="date"></div>
    <div class="form-group"><label class="form-label" for="sp-end">${t('sprint.endDate')}</label><input class="form-input" id="sp-end" type="date"></div>
    <div class="form-group"><label class="form-label" for="sp-release">${t('sprint.releaseOptional')}</label>
      <select class="form-select" id="sp-release">
        <option value="">${t('sprint.notLinked')}</option>
        ${S.releases.filter(m=>m.status!=='CLOSED').map(m=>`<option value="${m.id}">${esc(m.name)}</option>`).join('')}
      </select>
    </div>`,
    async () => {
      const name = el('#sp-name')?.value?.trim();
      if (!name) throw new Error(t('validation.sprintNameRequired'));
      const d = { name, goal: el('#sp-goal')?.value || '' };
      const start = el('#sp-start')?.value;
      const end   = el('#sp-end')?.value;
      const rel   = el('#sp-release')?.value;
      if (start) d.startDate = start;
      if (end)   d.endDate   = end;
      if (rel)   d.releaseId = rel;
      const sp = await api.sprints.create(S.project.id, d);
      S.sprints.push(sp);
      toast(t('sprint.created'), 'success');
      await renderSprints();
    });
}

function editSprint(id) {
  const s = S.sprints.find(s => s.id === id);
  if (!s) return;
  showModal(t('sprint.edit'), `
    <div class="form-group"><label class="form-label" for="sp-name">${t('form.name')}</label><input class="form-input" id="sp-name" value="${esc(s.name)}" autofocus></div>
    <div class="form-group"><label class="form-label" for="sp-goal">${t('sprint.goal')}</label><textarea class="form-input" id="sp-goal" rows="2">${esc(s.goal)}</textarea></div>
    <div class="form-group"><label class="form-label" for="sp-start">${t('sprint.startDate')}</label><input class="form-input" id="sp-start" type="date" value="${s.startDate?s.startDate.slice(0,10):''}"></div>
    <div class="form-group"><label class="form-label" for="sp-end">${t('sprint.endDate')}</label><input class="form-input" id="sp-end" type="date" value="${s.endDate?s.endDate.slice(0,10):''}"></div>
    <div class="form-group"><label class="form-label" for="sp-release">${t('sprint.releaseOptional')}</label>
      <select class="form-select" id="sp-release">
        <option value="">${t('sprint.notLinked')}</option>
        ${S.releases.filter(m=>m.status!=='CLOSED').map(m=>`<option value="${m.id}" ${s.releaseId===m.id?'selected':''}>${esc(m.name)}</option>`).join('')}
      </select>
    </div>`,
    async () => {
      const name = el('#sp-name')?.value?.trim();
      if (!name) throw new Error(t('validation.sprintNameRequired'));
      // version: reject stale edits with 409 (renderSprints refetches after).
      const d = { name, goal: el('#sp-goal')?.value || '', version: s.version };
      const start = el('#sp-start')?.value;
      const end   = el('#sp-end')?.value;
      const rel   = el('#sp-release')?.value;
      if (start) d.startDate = start; else d.startDate = null;
      if (end)   d.endDate   = end;   else d.endDate   = null;
      d.releaseId = rel || null;
      await api.sprints.update(id, d);
      toast(t('sprint.updated'), 'success');
      await renderSprints();
    });
}

async function startSprint(id) {
  try {
    await api.sprints.start(id);
    toast(t('sprint.started'), 'success');
    S.sprints = await api.sprints.list(S.project.id);
    renderSidebar();
    await renderSprints();
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}

function completeSprint(id, name) {
  confirmDelete(t('sprint.complete'),
    t('sprint.completeConfirm',{name}),
    async () => {
      await api.sprints.complete(id);
      toast(t('sprint.completedToast'), 'success');
      S.sprints = await api.sprints.list(S.project.id);
      // The sprint board was torn down; drop it from the nav and any cache.
      if (S.board && S.board.isSprintBoard) S.board = null;
      if (S.view === 'sprintBoard') S.view = 'sprints';
      renderSidebar();
      await renderSprints();
    });
  const sub = el('#modal-submit');
  if (sub) { sub.textContent = t('sprint.complete'); sub.className = 'btn btn-warning'; }
}

function deleteSprint(id, name) {
  confirmDelete(t('sprint.deleteTitle'), t('sprint.deleteConfirm',{name}), async () => {
    await api.sprints.del(id);
    S.sprints = S.sprints.filter(s => s.id !== id);
    toast(t('sprint.deletedToast'), 'success');
    if (S.board && S.board.isSprintBoard) S.board = null;
    if (S.view === 'sprintBoard') S.view = 'sprints';
    renderSidebar();
    await renderSprints();
  });
}

// ═══════════════════════════════════════════════════════════
// SPRINT REPORTS — burndown + velocity, hand-rolled inline SVG
// (house style: icons.js-like inline SVG, theme tokens via CSS
// variables/currentColor, no chart library)
// ═══════════════════════════════════════════════════════════

// toggleSprintReport expands/collapses the report panel on a sprint card,
// fetching the burndown for this sprint and the project velocity on first open.
async function toggleSprintReport(sprintId) {
  const panel = el('#sprint-report-' + sprintId);
  if (!panel) return;
  const btn = document.querySelector(`[data-act="toggleSprintReport"][data-a0="${sprintId}"]`);
  if (!panel.classList.contains('hidden')) {
    panel.classList.add('hidden');
    btn?.setAttribute('aria-expanded', 'false');
    return;
  }
  panel.classList.remove('hidden');
  btn?.setAttribute('aria-expanded', 'true');
  if (panel.dataset.loaded) return;
  await loadSprintReport(sprintId, defaultReportUnit());
}

// loadSprintReport (re)fills a sprint's report panel in the given unit. It is
// also the handler behind the tasks ⇄ effort toggle, so switching units is a
// refetch of the burndown only — the velocity report carries both series at
// once and is fetched alongside it.
async function loadSprintReport(sprintId, unit) {
  const panel = el('#sprint-report-' + sprintId);
  if (!panel) return;
  panel.innerHTML = `<div class="text-muted text-sm">${t('app.loadingEllipsis')}</div>`;
  try {
    const [burndown, velocity] = await Promise.all([
      api.sprints.burndown(sprintId, unit),
      api.reports.velocity(S.project.id),
    ]);
    panel.innerHTML = reportUnitToggle(sprintId, unit)
      + renderBurndownChart(burndown)
      + renderVelocityChart(velocity);
    panel.dataset.loaded = '1';
  } catch (e) {
    panel.innerHTML = `<div class="text-muted text-sm">${esc(apiErrorMessage(e))}</div>`;
  }
}

// setSprintReportUnit is the toggle's action handler.
function setSprintReportUnit(sprintId, unit) { return loadSprintReport(sprintId, unit); }

// defaultReportUnit is what a report opens in: effort where the project
// estimates (that is the reason estimation was switched on), tasks otherwise.
function defaultReportUnit() {
  return estimationEnabled(S.project) ? burndownUnitFor(S.project) : 'tasks';
}

// burndownUnitFor maps the project's estimation unit onto the ?unit= value the
// burndown endpoint expects ('' when the project does not estimate).
function burndownUnitFor(project) {
  const u = estimationUnit(project);
  return u === 'POINTS' ? 'points' : u === 'HOURS' ? 'hours' : '';
}

// reportEffortLabel is the project's unit as a short axis/toggle label
// ("Story points" / "Hours") — estimateLabel is the editor's phrasing
// ("Estimate (hours)"), which reads wrong on a chart axis.
function reportEffortLabel(project) {
  return estimationUnit(project) === 'HOURS' ? t('report.unit.hours') : t('report.unit.points');
}

// reportUnitToggle renders the tasks ⇄ effort switch, labelled with the
// project's actual unit. It renders nothing at all where estimation is off —
// there is no second thing to switch to.
function reportUnitToggle(sprintId, unit) {
  if (!estimationEnabled(S.project)) return '';
  const effortUnit = burndownUnitFor(S.project);
  const opt = (value, label) =>
    `<button type="button" class="btn btn-sm ${unit === value ? 'btn-primary' : 'btn-secondary'}"
       data-act="setSprintReportUnit" data-a0="${esc(sprintId)}" data-a1="${esc(value)}"
       aria-pressed="${unit === value}">${label}</button>`;
  return `<div class="report-unit-toggle" role="group" aria-label="${t('report.unit.label')}">
      ${opt('tasks', t('report.unit.tasks'))}
      ${opt(effortUnit, reportEffortLabel(S.project))}
    </div>`;
}

// renderBurndownChart draws remaining-vs-ideal as an SVG line chart: one point
// per sprint day (remaining is null for future days of a running sprint), the
// ideal line dashed. Colors come from theme tokens so all four themes work.
function renderBurndownChart(bd) {
  const pts = bd.points || [];
  const n = pts.length;
  if (!n) return '';
  const W = 520, H = 200, L = 34, R = 24, T = 10, B = 26;
  const plotW = W - L - R, plotH = H - T - B;
  const maxY = Math.max(bd.committed, 1);
  const x = i => L + (n === 1 ? plotW / 2 : (i * plotW) / (n - 1));
  const y = v => T + plotH - (v / maxY) * plotH;

  // Recessive horizontal gridlines at ~4 steps. Effort can be fractional
  // (hours), so the step is only forced to a whole number where the series is
  // whole — a 3.75h sprint must not be labelled 0, 1, 2, 3, 4.
  const fractional = maxY < 4 && bd.unit === 'hours';
  const step = fractional ? maxY / 4 : Math.max(1, Math.ceil(maxY / 4));
  let grid = '', yLabels = '';
  for (let v = 0; v <= maxY + 1e-9; v += step) {
    const label = fractional ? Math.round(v * 100) / 100 : v;
    grid += `<line x1="${L}" y1="${y(v)}" x2="${W - R}" y2="${y(v)}" class="report-grid"/>`;
    yLabels += `<text x="${L - 6}" y="${y(v) + 3}" text-anchor="end" class="report-axis-label">${label}</text>`;
  }

  const ideal = pts.map((p, i) => `${x(i)},${y(p.ideal)}`).join(' ');
  const actualPts = pts.map((p, i) => ({ p, i })).filter(o => o.p.remaining !== null && o.p.remaining !== undefined);
  const actual = actualPts.map(o => `${x(o.i)},${y(o.p.remaining)}`).join(' ');
  const markers = actualPts.map(o =>
    `<circle cx="${x(o.i)}" cy="${y(o.p.remaining)}" r="3" class="report-line-actual-marker"><title>${esc(o.p.date)}: ${o.p.remaining}</title></circle>`).join('');

  // Sparse x labels: at most ~5, always including the first and last day.
  //
  // The last day is forced, and that is what used to collide: on a 26-day
  // sprint the regular ticks land on 0, 6, 12, 18, 24 and the forced one on 25,
  // a single day apart — about 18px for a label some 30px wide, so the two
  // printed on top of each other and the axis read "08-1408-15". Drop a regular
  // tick that lands closer to the forced last one than a label is wide, rather
  // than letting them overlap; the gap it leaves is smaller than the one an
  // unreadable pair leaves.
  const every = Math.max(1, Math.ceil(n / 5));
  const MIN_LABEL_GAP = 34;   // viewBox units — a "MM-DD" label is ~30 wide
  const ticks = [];
  for (let i = 0; i < n; i += every) ticks.push(i);
  if (ticks[ticks.length - 1] !== n - 1) ticks.push(n - 1);
  while (ticks.length > 1 && x(ticks[ticks.length - 1]) - x(ticks[ticks.length - 2]) < MIN_LABEL_GAP) {
    ticks.splice(ticks.length - 2, 1);
  }
  const xLabels = ticks.map(i =>
    `<text x="${x(i)}" y="${H - 8}" text-anchor="middle" class="report-axis-label">${esc(pts[i].date.slice(5))}</text>`).join('');

  // The title names what the axis measures, read from the unit the API echoed
  // back rather than from what the client asked for — the chart can then never
  // mislabel itself, even against an older server that ignored ?unit=.
  const title = bd.unit === 'points' ? t('report.burndownTitlePoints')
    : bd.unit === 'hours' ? t('report.burndownTitleHours')
    : t('report.burndownTitle');
  // An unestimated task weighs nothing, which on the chart is indistinguishable
  // from finished work. Say so rather than let the line lie.
  const unestimated = bd.unestimated > 0
    ? `<div class="report-note">${icon('warning', { size: 'sm' })} ${t('report.unestimated', { count: bd.unestimated })}</div>`
    : '';

  return `
    <div class="report-section">
      <div class="report-title">${title}</div>
      <svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${t('report.burndownAria', { name: esc(bd.name) })}">
        ${grid}${yLabels}${xLabels}
        <polyline points="${ideal}" class="report-line-ideal" fill="none"/>
        ${actual ? `<polyline points="${actual}" class="report-line-actual" fill="none"/>` : ''}
        ${markers}
      </svg>
      <div class="report-legend">
        <span><span class="report-swatch report-swatch-actual"></span>${t('report.remaining')}</span>
        <span><span class="report-swatch report-swatch-ideal"></span>${t('report.ideal')}</span>
      </div>
      ${unestimated}
    </div>`;
}

// renderVelocityChart draws one committed/completed bar pair per completed
// sprint (oldest first, as delivered by the API).
//
// Effort: each entry carries its own estimateUnit, captured when that sprint
// was completed. The chart measures effort only while the whole history is in
// one unit — a project that switched POINTS → HOURS mid-history would
// otherwise plot 13 points and 13 hours as the same bar. In that case it falls
// back to the count series (always comparable) and says why.
function renderVelocityChart(entries) {
  if (!entries || !entries.length) {
    return `<div class="report-section">
      <div class="report-title">${t('report.velocityTitle')}</div>
      <div class="text-muted text-sm">${t('report.noVelocity')}</div>
    </div>`;
  }
  const units = [...new Set(entries.map(e => e.estimateUnit).filter(Boolean))];
  const effortComplete = entries.every(e => e.committedEstimate !== null && e.committedEstimate !== undefined);
  const useEffort = units.length === 1 && effortComplete;
  const mixedUnits = units.length > 1;
  const value = useEffort
    ? e => ({ committed: e.committedEstimate, completed: e.completedEstimate })
    : e => ({ committed: e.committed, completed: e.completed });

  const W = 520, H = 180, L = 34, R = 24, T = 10, B = 26;
  const plotW = W - L - R, plotH = H - T - B;
  const values = entries.flatMap(e => [value(e).committed, value(e).completed]);
  const maxY = Math.max(1, ...values);
  const y = v => T + plotH - (v / maxY) * plotH;
  const group = plotW / entries.length;
  const barW = Math.min(28, group / 3);

  const step = Math.max(1, Math.ceil(maxY / 4));
  let grid = '', yLabels = '';
  for (let v = 0; v <= maxY; v += step) {
    grid += `<line x1="${L}" y1="${y(v)}" x2="${W - R}" y2="${y(v)}" class="report-grid"/>`;
    yLabels += `<text x="${L - 6}" y="${y(v) + 3}" text-anchor="end" class="report-axis-label">${v}</text>`;
  }

  let bars = '', xLabels = '';
  entries.forEach((e2, i) => {
    const v = value(e2);
    const cx = L + group * i + group / 2;
    const title = `<title>${esc(e2.name)}: ${t('report.committed')} ${v.committed}, ${t('report.completed')} ${v.completed}</title>`;
    // The two bars are separated by a 2px surface gap (the ±1 offsets) so
    // adjacent fills never read as one shape.
    bars += `<g>${title}
      <rect x="${cx - barW - 1}" y="${y(v.committed)}" width="${barW}" height="${Math.max(0, T + plotH - y(v.committed))}" class="report-bar-committed"/>
      <rect x="${cx + 1}" y="${y(v.completed)}" width="${barW}" height="${Math.max(0, T + plotH - y(v.completed))}" class="report-bar-completed"/>
    </g>`;
    const label = e2.name.length > 10 ? e2.name.slice(0, 9) + '…' : e2.name;
    xLabels += `<text x="${cx}" y="${H - 8}" text-anchor="middle" class="report-axis-label">${esc(label)}</text>`;
  });

  const title = useEffort
    ? (units[0] === 'HOURS' ? t('report.velocityTitleHours') : t('report.velocityTitlePoints'))
    : t('report.velocityTitle');
  const note = mixedUnits
    ? `<div class="report-note">${icon('warning', { size: 'sm' })} ${t('report.velocityMixedUnits')}</div>`
    : '';

  return `
    <div class="report-section">
      <div class="report-title">${title}</div>
      <svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${t('report.velocityAria')}">
        ${grid}${yLabels}${xLabels}${bars}
      </svg>
      <div class="report-legend">
        <span><span class="report-swatch report-swatch-committed"></span>${t('report.committed')}</span>
        <span><span class="report-swatch report-swatch-completed"></span>${t('report.completed')}</span>
      </div>
      ${note}
    </div>`;
}

// ── view registration (see registry.js for the contract) ──
Views.register('sprints', {
  render: renderSprints,
  sidebar: { icon: 'sprint', label: () => t('nav.sprints'), key: 'S', order: 50 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreateSprint">${icon('add',{size:'md'})} ${t('sprint.new')}</button>`,
});
Views.register('releases', {
  render: renderReleases,
  sidebar: { icon: 'release', label: () => t('nav.releases'), key: 'R', order: 60 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreateRelease">${icon('add',{size:'md'})} ${t('release.new')}</button>`,
});

// ═══════════════════════════════════════════════════════════
// PAGES — split-pane editor + TOC
// ═══════════════════════════════════════════════════════════

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([showCreateRelease, showCreateSprint], _A0);
registerActions([
  closeRelease, editRelease, editSprint, reopenRelease, startSprint, toggleSprintReport,
], _A1);
registerActions([completeSprint, deleteRelease, deleteSprint, setSprintReportUnit], _A2);

export { burndownUnitFor, renderBurndownChart, renderVelocityChart, reportEffortLabel, showCreateRelease, showCreateSprint };
