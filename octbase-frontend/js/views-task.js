import { t } from '@octbase/shared/i18n.js';
import { ESTIMATION_UNITS, FIBONACCI_POINTS, STATUSES, STATUS_META, TYPE_META, estimateLabel, estimateLimits, estimationField, estimationUnit, openDescendantsOf, parseEstimateInput, priorityMeta, priorityNames, projectTaskTypes, taskEstimatable, taskEstimate, typeChain, typeChildOf, typeParentAllowed, typeParentRule } from '@octbase/shared/meta.js';
import { rtSafeHref, sanitizeRichText } from '@octbase/shared/richtext.js';
import { api, dropProjectTasksPrefetch } from './api.js';
import { _A0, _A1, _A2, _A3, _VAL, registerActions, registerChanges, registerInputs, registerKeydowns } from './delegation.js';
import { API_BASE } from './env.js';
import { avatarHtml, confirmDelete, confirmModal, debounced, el, esc, fmtDateTime, memberName, promptModal, renderDescriptionHTML, renderedDescriptionOriginal, slugify, taskLabel, toast, typeBadge, userAvatarHtml } from './framework.js';
import { apiErrorMessage, http } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { S, getTaskDraft, taskFilterParams, taskSeqLabel } from './state.js';
import { applyBoardTaskRemoval, applyBoardTaskUpdate } from './views-board.js';
import { renderContent, updateBulkBar } from './views-shell.js';
import { applyListTaskRemoval, applyListTaskUpdate } from './views-tasklist.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
let _taskPanelReturnFocus = null;

async function openTaskPanel(taskId) {
  if (S.taskPanelId !== taskId) S.taskPanelTab = 'details';
  S.taskPanelId = taskId;
  // Update URL to reflect open task.
  if (S.project) {
    const base = `/projects/${S.project.id}/${S.view}`;
    const params = taskFilterParams();
    params.set('task', taskId);
    history.replaceState(null, '', '#' + base + '?' + params.toString());
  }
  const panel = el('#task-panel');
  const wasOpen = panel.classList.contains('open');
  panel.classList.add('open');
  panel.setAttribute('aria-modal', 'true');
  updateBulkBar();   // hide the bottom action bar while the panel is open
  if (!wasOpen) _taskPanelReturnFocus = document.activeElement;
  await renderTaskPanel(taskId);
  // Move focus into the panel (WCAG 2.4.3 Focus Order).
  const closeBtn = panel.querySelector('.panel-close');
  if (closeBtn) closeBtn.focus();
}

function closeTaskPanel() {
  // taskDescriptionOriginals[id] is only ever read while that task's Details
  // tab is open, and is unconditionally re-set from the server value the next
  // time it renders (see below) — so it's safe to drop on close rather than
  // letting one entry accumulate per task ever opened this session.
  // taskDescriptionDrafts[id] is deliberately NOT cleared here: it's the
  // unsaved-edit cache, kept across close/reopen so closing the panel by
  // accident doesn't lose in-progress text.
  if (S.taskPanelId) delete S.taskDescriptionOriginals[S.taskPanelId];
  S.taskPanelId = null;
  S.taskPanelData = null;
  const panel = el('#task-panel');
  if (panel) {
    panel.classList.remove('open');
    panel.setAttribute('aria-modal', 'false');
  }
  updateBulkBar();   // restore the bottom action bar if a selection is still active
  // Remove task= from URL.
  if (S.project) {
    const base = `/projects/${S.project.id}/${S.view}`;
    const params = taskFilterParams();
    const search = params.toString();
    history.replaceState(null, '', '#' + base + (search ? '?' + search : ''));
  }
  if (_taskPanelReturnFocus && document.body.contains(_taskPanelReturnFocus)) {
    _taskPanelReturnFocus.focus();
  }
  _taskPanelReturnFocus = null;
}

// The project's task list feeds the panel's hierarchy UI (parent candidates and
// the child-task list) and is by far the heaviest of the panel's parallel
// fetches — up to 200 full tasks. It was refetched on every panel open *and*
// after every small panel edit, since those re-run renderTaskPanel wholesale.
// Reuse it briefly instead. The TTL is short and any task write drops the cache
// (invalidateProjectTasks), so the worst case is that a task created in another
// tab takes a few seconds to appear among the parent candidates.
const PROJECT_TASKS_TTL_MS = 15000;
let _projectTasks = { projectId: null, at: 0, tasks: null };

function invalidateProjectTasks() {
  // Drop any pending Prefetch entry for the same request too — a prefetch
  // started before the mutation would otherwise hand the next view a list
  // that predates the write this invalidation is announcing.
  if (_projectTasks.projectId) dropProjectTasksPrefetch(_projectTasks.projectId);
  if (S.project?.id) dropProjectTasksPrefetch(S.project.id);
  _projectTasks = { projectId: null, at: 0, tasks: null };
}

function loadProjectTasks(projectId) {
  if (!projectId) return Promise.resolve([]);
  const c = _projectTasks;
  if (c.projectId === projectId && c.tasks && Date.now() - c.at < PROJECT_TASKS_TTL_MS) {
    return Promise.resolve(c.tasks);
  }
  // listAll, not one 200-row page: this cache backs the parent and relation
  // pickers, and a truncated set silently hides the project's oldest tasks from
  // them — the ones most likely to be the epic or story you are looking for.
  return api.tasks.listAll(projectId)
    .then(tasks => { _projectTasks = { projectId, at: Date.now(), tasks }; return tasks; })
    .catch(() => []);
}

async function renderTaskPanel(taskId) {
  const pane = el('#task-panel-content');
  if (!pane) return;
  pane.innerHTML = `<div class="loading"><div class="spinner"></div></div>`;
  try {
    const [task, comments, activity, branches, links, relations, attachments, projectTasks] = await Promise.all([
      api.tasks.get(taskId),
      api.comments.list(taskId),
      api.tasks.activity(taskId).catch(()=>[]),
      api.branches.list(taskId).catch(()=>[]),
      api.links.list(taskId).catch(()=>[]),
      api.relations.list(taskId).catch(()=>[]),
      api.attachments.list(taskId).catch(()=>[]),
      loadProjectTasks(S.project?.id),
    ]);
    // Cache the full payload so switching between tabs (comments, branches,
    // …) re-renders from memory instead of refetching all seven endpoints.
    S.taskPanelData = {
      taskId, task, comments, activity, branches, links, relations, attachments,
      // A full first page means there may be older entries; loadMoreTaskActivity
      // appends them here so tab switches keep what was loaded.
      activityDone: activity.length < TASK_ACTIVITY_PAGE_SIZE,
      projectTasks: (projectTasks || []).filter(tk => tk.status !== 'ARCHIVED'),
      branchSuggestion: suggestBranchName(task), relationsResolved: false,
    };
    await paintTaskPanel();
  } catch(e) {
    pane.innerHTML = `<div class="empty">${esc(apiErrorMessage(e))}</div>`;
  }
}

// paintTaskPanel rebuilds the panel chrome (header, badges, tabs) and the active
// tab straight from the cached S.taskPanelData snapshot — no network, no spinner.
// renderTaskPanel calls it after fetching; in-place refreshers (status/column
// changes made from the sidebar) call it directly so the panel updates the badges
// and detail controls without blanking and reloading the whole panel.
async function paintTaskPanel() {
  const pane = el('#task-panel-content');
  const d = S.taskPanelData;
  if (!pane || !d) return;
  const task = d.task;
  const seq = task.seqNumber != null
    ? `<span class="task-seq-big">${esc(S.project?.abbreviation||S.project?.slug?.toUpperCase()||'')}-${task.seqNumber}</span>`
    : '';
  // The repaint below replaces the panel's whole DOM, the details tab's
  // attachment list included. Take the rendered list out first and hand it to
  // renderActiveTab, which puts it back when it still shows the same files —
  // see the comment there for why rebuilding it is user-visible.
  const keptAtts = pane.querySelector('#att-sidebar-list');
  const tabs = [
    ['details',     t('task.detailsTab')],
    ['comments',    t('task.commentsTab')],
    ['links',       t('task.linksTab')],
    ['relations',   t('task.relationsTab')],
    ['branches',    t('task.branchesTab')],
    ['activity',    t('nav.activity')],
  ];
  pane.innerHTML = `
    <div class="panel-header">
      <div class="panel-title">
        <div class="panel-title-row">
          <input class="panel-title-input" id="panel-title-input" value="${esc(task.title)}"
            aria-label="${t('form.title')}"
            data-change="savePanelTitle" data-a0="${esc(task.id)}"
            data-keydown="panelTitleKeydown">
          <button class="icon-btn panel-title-edit" data-act="focusPanelTitle"
            aria-label="${t('task.editTitle')}" title="${t('task.editTitle')}">${icon('edit',{size:'sm'})}</button>
        </div>
        <div class="panel-title-meta">
          ${typeBadge(task.taskType)} ${seq}
          <span class="badge ${STATUS_META[task.status]?.cls||''}">${STATUS_META[task.status]?.label||task.status}</span>
          <span class="badge prio-badge ${priorityMeta(task.priority).cls}">${esc(priorityMeta(task.priority).label)}</span>
        </div>
      </div>
      <div class="panel-header-actions">
        <button class="icon-btn panel-close" data-act="closeTaskPanel" aria-label="${t('task.closeTaskPanel')}" title="${t('task.close')}">${icon('close')}</button>
      </div>
    </div>
    <div class="panel-tabs">
      ${tabs.map(([key,label])=>`<button class="panel-tab ${S.taskPanelTab===key?'active':''}" data-act="switchPanelTab" data-a0="${esc(key)}">${esc(label)}</button>`).join('')}
    </div>
    <div id="panel-tab-content"></div>`;
  await renderActiveTab(keptAtts);
}

// refreshTaskPanelInPlace repaints the open panel from its already-updated cache
// snapshot (the caller sets S.taskPanelData.task first) with no spinner and no
// refetch, so an edit made in the sidebar moves the badges and detail controls in
// place — the SPA-in-place counterpart of applyBoardTaskUpdate for the panel.
// No-ops when the panel isn't showing that task.
async function refreshTaskPanelInPlace(taskId) {
  if (S.taskPanelData?.task?.id !== taskId) return;
  // The repaint replaces the panel's DOM, so the control the user just operated
  // (usually the select they changed) would lose focus. Remember it and hand
  // focus back to its freshly rendered twin: an in-place update must not move
  // focus (WCAG 3.2.1 On Focus / keyboard operability).
  const focused = panelFocusSelector(document.activeElement);
  await paintTaskPanel();
  const pane = el('#task-panel-content');
  const again = focused && pane ? pane.querySelector(focused) : null;
  if (again && again !== document.activeElement) again.focus({ preventScroll: true });
}

// panelFocusSelector identifies a focused control inside the task panel well
// enough to find it again after a repaint: by id when it has one, otherwise by
// the handler attributes that make it unique (data-change/data-act/data-input
// plus their arguments). Returns '' for anything outside the panel or without a
// stable handle, in which case focus is simply left alone.
function panelFocusSelector(node) {
  const pane = el('#task-panel-content');
  if (!node || !pane || !pane.contains(node)) return '';
  const attr = a => `[${a}="${String(node.getAttribute(a)).replace(/["\\]/g, '\\$&')}"]`;
  if (node.id) return attr('id');
  const handler = ['data-change', 'data-act', 'data-input'].find(a => node.hasAttribute(a));
  if (!handler) return '';
  return node.tagName.toLowerCase()
    + [handler, 'data-a0', 'data-a1'].filter(a => node.hasAttribute(a)).map(attr).join('');
}

// applyTaskUpdate pushes a fresh server snapshot of a task into everything that
// shows it, without a reload: the cached panel payload, the open panel, and the
// board — which keeps its whole card set in memory, so the one card is patched
// and the lanes re-render in place. Every field a card face shows (title, type,
// priority, assignee, due date, release, sprint) therefore appears the moment the
// edit is saved, the way a status change already did; nothing waits for a page
// reload. The list views (Backlog, Task view) patch their own cached row the same
// way via applyListTaskUpdate; only a view with neither — or one whose list is not
// on screen — falls back to a full renderContent().
//
// `panel` picks how the sidebar itself is refreshed:
//   'inplace' — repaint from the updated cache (no spinner, no refetch)
//   'reload'  — refetch the panel, for edits that change data the response does
//               not carry (the task hierarchy: parent candidates, child list)
//   'none'    — leave it alone, when the control the user typed into already
//               shows the saved value and rebuilding it would disturb them
async function applyTaskUpdate(taskId, updated, { panel = 'inplace' } = {}) {
  if (updated && S.taskPanelData?.task?.id === taskId) S.taskPanelData.task = updated;
  invalidateProjectTasks();
  if (panel === 'reload') await renderTaskPanel(taskId);
  else if (panel === 'inplace') await refreshTaskPanelInPlace(taskId);
  const patched = !!updated
    && (applyBoardTaskUpdate(taskId, updated) || applyListTaskUpdate(taskId, updated));
  if (!patched) await renderContent();
}

// renderActiveTab fills #panel-tab-content from the cached task payload and
// marks the active tab button. The only network call is the lazy lookup of
// related-task titles, done once and cached on the payload.
// `carriedAtts` is the attachment list node paintTaskPanel rescued from the DOM
// it just replaced; when the details tab renders the same files again, that node
// goes back in instead of a freshly built one.
async function renderActiveTab(carriedAtts) {
  const host = el('#panel-tab-content');
  const d = S.taskPanelData;
  if (!host || !d) return;
  document.querySelectorAll('.panel-tab').forEach(b => {
    const isActive = b.dataset.a0 === S.taskPanelTab;
    b.classList.toggle('active', isActive);
    // On Compact the tab strip scrolls horizontally; keep the active tab in view.
    if (isActive) b.scrollIntoView({ inline: 'nearest', block: 'nearest' });
  });
  if (S.taskPanelTab === 'relations' && d.relations.length && !d.relationsResolved) {
    const others = await Promise.all(d.relations.map(r =>
      api.tasks.get(r.sourceTaskId === d.task.id ? r.targetTaskId : r.sourceTaskId).catch(()=>null)));
    d.relations.forEach((r,i) => { r._otherTitle = others[i]?.title || (r.sourceTaskId === d.task.id ? r.targetTaskId : r.sourceTaskId); });
    d.relationsResolved = true;
  }
  if (S.taskPanelTab === 'details') {
    // Layout: the description editor spans full width on top with the Preview
    // button in the top-right corner; below it the select fields and the
    // attachment column sit side-by-side in two columns; actions close the tab.
    const task = d.task;
    S.taskDescriptionOriginals[task.id] = task.description || '';
    const description = getTaskDraft(task);
    // Only an existing draft can be dirty, and it is measured with the same
    // comparison the keystroke handler uses (taskDescriptionDirty) so a repaint
    // mid-edit cannot disagree with the flag the editor was showing.
    const descriptionDirty = Object.prototype.hasOwnProperty.call(S.taskDescriptionDrafts, task.id)
      && taskDescriptionDirty(task.id, S.taskDescriptionDrafts[task.id]);
    const immutable = task.status === 'DONE' || task.status === 'ARCHIVED';
    // Every in-place panel repaint (an estimate saved, a status or priority
    // changed) rebuilds this tab's DOM. Rebuilding the attachment thumbnails
    // with it destroys <img> elements that already hold decoded pictures and
    // recreates them empty, so the previews visibly blink out and back — they
    // look like they are reloading, on an edit that has nothing to do with
    // them. Keep the rendered list node and re-insert it below when it still
    // shows exactly the same attachments.
    const keptAtts = carriedAtts || host.querySelector('#att-sidebar-list');
    const attsKey = attachmentListKey(d.attachments);
    host.innerHTML = `
      <div class="detail-layout">
        <div class="detail-toolbar-top">
          <span class="detail-version">${t('task.version',{version:task.version})}</span>
          <button class="btn btn-secondary btn-sm" data-act="openTaskPreview" data-a0="${esc(task.id)}">${icon('view',{size:'md'})} ${t('task.preview')}</button>
        </div>
        ${descriptionEditorHtml(task, description, descriptionDirty, immutable)}
        <div class="detail-two-col">
          <div class="detail-fields">${renderDetailFields(task, immutable)}</div>
          ${attachmentSidebarHtml(task, d.attachments)}
        </div>
        ${immutable ? `<p class="text-muted text-sm">${t('task.immutableHint')}</p>` : ''}
        ${renderDetailActions(task, immutable)}
      </div>`;
    if (keptAtts && keptAtts.dataset.attKey === attsKey) {
      host.querySelector('#att-sidebar-list')?.replaceWith(keptAtts);
    }
    hydrateAuthImages(host);
    hydrateRichEditors(host);
    trackRichEditorHeights(host);
    return;
  }
  host.innerHTML =
    S.taskPanelTab === 'comments'    ? renderTaskComments(d.task, d.comments) :
    S.taskPanelTab === 'links'       ? renderTaskLinks(d.task, d.links) :
    S.taskPanelTab === 'relations'   ? renderTaskRelations(d.task, d.relations) :
    S.taskPanelTab === 'branches'    ? renderTaskBranches(d.task, d.branches, d.branchSuggestion) :
    renderTaskActivity(d.activity, d.activityDone);
  // Comments (rich text), attachments (thumbnails), etc. may contain auth-gated
  // images; swap their sources for authenticated object URLs.
  hydrateAuthImages(host);
  hydrateRichEditors(host);
  trackRichEditorHeights(host);
}

function suggestBranchName(task) {
  if (!task || !S.project) return '';
  const type = (task.taskType||'TASK').toLowerCase();
  const branchType = type === 'bug' ? 'bugfix' : 'feature';
  const prefix = (S.project.abbreviation || S.project.slug).toLowerCase();
  const seq = task.seqNumber || '0';
  const title = slugify(task.title).slice(0, 40);
  return `${branchType}/${prefix}-${seq}-${title}`;
}

// rtToolbarButtons defines the rich-text toolbar. cmd is the execCommand-style
// action handled in rtExec; each button is i18n'd and keyboard-operable.
const RT_TOOLBAR = [
  { cmd:'bold',          icon:'B',  key:'editor.bold' },
  { cmd:'italic',        icon:'I',  key:'editor.italic' },
  { cmd:'insertUnorderedList', icon:'•',  key:'editor.bulletList' },
  { cmd:'insertOrderedList',   icon:'1.', key:'editor.numberedList' },
  { cmd:'heading',       icon:'H',  key:'editor.heading' },
  { cmd:'link',          icon:'link', svg:true, key:'editor.link' },
  { cmd:'code',          icon:'</>',key:'editor.code' },
];

// descriptionEditorHtml renders the contenteditable rich-text editor with its
// toolbar plus the inline attach button, mirroring the server HTML allowlist.
// The initial content is sanitized HTML (or escaped legacy plain text).
function descriptionEditorHtml(task, description, dirty, immutable) {
  if (immutable) {
    return `
      <div class="detail-group detail-desc">
        <label id="pt-desc-label">${t('task.description')}</label>
        <div class="rt-render" aria-labelledby="pt-desc-label">${renderDescriptionHTML(description)}</div>
      </div>`;
  }
  const toolbar = RT_TOOLBAR.map(b => `
    <button type="button" class="rt-tool" data-act="rtCmd" data-a0="${esc(task.id)}" data-a1="${esc(b.cmd)}"
      aria-label="${t(b.key)}" title="${t(b.key)}">${b.svg ? icon(b.icon,{size:'sm'}) : esc(b.icon)}</button>`).join('');
  return `
    <div class="detail-group detail-desc">
      <label id="pt-desc-label">${t('task.description')}</label>
      <div class="rt-toolbar" role="toolbar" aria-label="${t('editor.toolbar')}">
        ${toolbar}
        <span class="rt-tool-sep" aria-hidden="true"></span>
        <button type="button" class="rt-tool" data-act="rtAttach"
          aria-label="${t('editor.attachFile')}" title="${t('editor.attachFile')}">${icon('attach',{size:'sm'})}</button>
        <input type="file" id="rt-file-input" class="rt-file-input" data-change="rtFilePicked" data-a0="${esc(task.id)}" data-a1="#pt-desc" aria-hidden="true" tabindex="-1">
      </div>
      <div class="rt-editor form-input" id="pt-desc" contenteditable="true" role="textbox"
        aria-multiline="true" aria-labelledby="pt-desc-label"
        data-input="updateTaskDescriptionDraft" data-a0="${esc(task.id)}"
        data-keydown="rtEditorKeydown" data-a1="${esc(task.id)}">${renderDescriptionHTML(description)}</div>
      <div class="detail-inline-actions">
        <span class="text-muted text-sm" id="pt-desc-status">${dirty ? t('form.unsavedChanges') : t('form.saved')}</span>
        <button class="btn btn-primary btn-sm" id="pt-desc-save" ${dirty ? '' : 'disabled'} data-act="saveTaskDescription" data-a0="${esc(task.id)}">${t('form.save')}</button>
      </div>
    </div>`;
}

// personOptions renders the <option> list shared by the assignee and reviewer
// selects. Candidates come from S.assignables — the project's members plus the
// global admins, who reach the project without a membership row and were
// therefore missing from these pickers while they read S.members. A global
// admin who is not a member is marked as such, since project access still
// follows membership for everyone below Super Admin. A currently-set person who
// is no longer a candidate (member removed, account disabled) keeps an option of
// their own, so the select shows the truth rather than silently reading as
// unassigned.
function personOptions(selectedId) {
  const list = (S.assignables && S.assignables.length) ? S.assignables : S.members;
  const opt = (id, label, selected) =>
    `<option value="${esc(id)}" ${selected?'selected':''}>${esc(label)}</option>`;
  const html = list.map(m => opt(
    m.userId,
    m.member === false ? t('task.globalAdmin', {name: m.name}) : m.name,
    selectedId === m.userId,
  )).join('');
  if (selectedId && !list.some(m => m.userId === selectedId)) {
    return opt(selectedId, t('task.formerMember', {name: memberName(selectedId)}), true) + html;
  }
  return html;
}

// panelStatusOptions lists the statuses the panel's Status control offers: the
// canonical built-ins plus any custom board-column status, so every lane on the
// project's board is reachable now that Status — and not a second board-column
// select — decides where the card sits. The task's own status is always in the
// list, so a card sitting in a since-renamed lane still reads truthfully rather
// than showing whatever option happened to come first.
function panelStatusOptions(task) {
  const laneStatuses = (S.board?.columns || []).map(c => c.status).filter(Boolean);
  return [...new Set([...STATUSES, ...laneStatuses, task.status].filter(Boolean))]
    .map(s => `<option value="${esc(s)}" ${task.status===s?'selected':''}>${esc(STATUS_META[s]?.label ?? s)}</option>`)
    .join('');
}

// renderDetailFields renders the select/value rows (status, priority, assignee,
// dates, …) as a single-column stack — it sits in the left of the two-column
// row beneath the full-width description editor.
//
// Status is the single control over both the task's status and its board lane:
// there used to be a second "board column" select beside it, which meant the
// panel showed two controls for one idea and let them disagree. changeStatus
// now reconciles the board, so the column select is gone and Status is shown
// for every task rather than only for one off the board.
//
// On an immutable (DONE/ARCHIVED) task the editors mirror what the API will
// actually accept: status, type, estimate and due date are disabled because
// UpdateTask/ChangeStatus reject them with TASK_IMMUTABLE, while the placement
// controls (parent, release, sprint) and priority/assignee/reviewer stay live
// because their endpoints allow them on finished tasks.
function renderDetailFields(task, immutable) {
  const row = (label, valueHtml) => `
      <div class="detail-row">
        <span class="detail-label">${esc(label)}</span>
        <span class="detail-val">${valueHtml}</span>
      </div>`;
  return `
    <div class="panel-details panel-details-stacked">
      ${row(t('task.statusLabel'), `
        <select class="detail-select" aria-label="${t('task.statusLabel')}" data-change="changeStatus" data-a0="${esc(task.id)}"${immutable?' disabled':''}>
          ${panelStatusOptions(task)}
        </select>`)}
      ${row(t('task.priorityLabel'), `
        <select class="detail-select" aria-label="${t('task.priorityLabel')}" data-change="changePriority" data-a0="${esc(task.id)}">
          ${priorityNames(S.priorities).map(p=>`<option value="${esc(p)}" ${task.priority===p?'selected':''}>${esc(priorityMeta(p).label)}</option>`).join('')}
        </select>`)}
      ${row(t('task.typeLabel'), `
        <select class="detail-select" aria-label="${t('task.typeLabel')}" data-change="changeType" data-a0="${esc(task.id)}"${immutable?' disabled':''}>
          ${projectTaskTypes(S.project).map(tt=>`<option value="${tt}" ${(S.taskPanelData?.pendingType||task.taskType)===tt?'selected':''}>${TYPE_META[tt].label}</option>`).join('')}
        </select>`)}
      ${renderParentRow(task)}
      ${row(t('task.assignee'), `
        <select class="detail-select" id="task-assignee" aria-label="${t('task.assignee')}" data-change="assignTask" data-a0="${esc(task.id)}" data-a1="assigneeId">
          <option value="">${t('task.unassigned')}</option>
          ${personOptions(task.assigneeId)}
        </select>`)}
      ${row(t('task.reviewer'), `
        <select class="detail-select" aria-label="${t('task.reviewer')}" data-change="assignTask" data-a0="${esc(task.id)}" data-a1="reviewerId">
          <option value="">${t('form.none')}</option>
          ${personOptions(task.reviewerId)}
        </select>`)}
      ${row(t('task.creator'), `
        <span class="detail-static">${userAvatarHtml(task.reporterId)}${esc(memberName(task.reporterId))}</span>`)}
      ${row(t('release.label'), `
        <select class="detail-select" aria-label="${t('release.label')}" data-change="updateTaskField" data-a0="${esc(task.id)}" data-a1="releaseId">
          <option value="">${t('form.none')}</option>
          ${S.releases.map(m=>`<option value="${m.id}" ${task.releaseId===m.id?'selected':''}>${esc(m.name)}</option>`).join('')}
        </select>`)}
      ${row(t('sprint.label'), `
        <select class="detail-select" aria-label="${t('sprint.label')}" data-change="updateTaskField" data-a0="${esc(task.id)}" data-a1="sprintId">
          <option value="">${t('sprint.none')}</option>
          ${S.sprints.filter(s=>s.status!=='COMPLETED').map(s=>`<option value="${s.id}" ${task.sprintId===s.id?'selected':''}>${esc(s.name)}${s.status==='ACTIVE'?' '+t('sprint.active'):''}</option>`).join('')}
        </select>`)}
      ${renderTaskDates(task, immutable)}
    </div>`;
}

// renderParentRow renders the hierarchy-parent selector for the details tab.
// The candidates are the project's live tasks of the type one level up
// (TYPE_PARENT); EPICs render nothing since they can never have a parent, and
// a SUBTASK gets no "no parent" option since its parent is mandatory.
// While a type change is pending (changeType deferred a save that needs a
// parent, e.g. TASK → SUBTASK), the row lists the *new* type's candidates
// behind a "Select parent…" placeholder; choosing one saves both together.
function renderParentRow(task) {
  const pending = S.taskPanelData?.pendingType;
  const type = pending || task.taskType;
  const rule = typeParentRule(S.project, type);
  if (!rule.parentType) return '';
  // Every level above the task's own is a candidate, not just rule.parentType
  // (typeParentAllowed) — grouped by type, matching the create modal.
  const groups = typeChain(S.project)
    .filter(ty => typeParentAllowed(S.project, type, ty))
    .map(ty => {
      const candidates = (S.taskPanelData?.projectTasks || [])
        .filter(tk => tk.taskType === ty && tk.id !== task.id);
      if (!candidates.length) return '';
      return `<optgroup label="${esc(TYPE_META[ty].label)}">${
        candidates.map(tk=>`<option value="${esc(tk.id)}" ${!pending && task.parentId===tk.id?'selected':''}>${esc((taskSeqLabel(tk)?taskSeqLabel(tk)+' — ':'')+tk.title)}</option>`).join('')
      }</optgroup>`;
    }).join('');
  return `
      <div class="detail-row">
        <span class="detail-label">${esc(t('task.parentLabel'))}</span>
        <span class="detail-val">
          <select class="detail-select" aria-label="${t('task.parentLabel')}" data-change="changeParent" data-a0="${esc(task.id)}">
            ${pending ? `<option value="" disabled selected>${t('task.selectParent')}</option>`
                      : rule.required ? '' : `<option value="">${t('task.noParent')}</option>`}
            ${groups}
          </select>
        </span>
      </div>`;
}

// renderTaskChildren lists the task's direct children on the relations tab;
// each entry opens its own panel. Children are read off parentId and may be of
// any lower type, not only typeChildOf's — that call is only the "can this
// type have children at all" question, which a SUBTASK answers with no.
// Renders nothing when the task has no children — no empty placeholder claims
// space.
function renderTaskChildren(task) {
  const childType = typeChildOf(S.project, task.taskType);
  if (!childType) return '';
  const kids = (S.taskPanelData?.projectTasks || []).filter(tk => tk.parentId === task.id);
  if (!kids.length) return '';
  return `
    <div class="detail-children">
      <span class="detail-label">${esc(t('task.children'))}</span>
      ${kids.map(k => `
          <button type="button" class="child-task-row" data-act="openTaskPanel" data-a0="${esc(k.id)}">
            ${typeBadge(k.taskType)}
            ${taskSeqLabel(k) ? `<span class="task-seq">${esc(taskSeqLabel(k))}</span>` : ''}
            <span class="child-task-title">${esc(k.title)}</span>
            <span class="badge ${STATUS_META[k.status]?.cls||''}">${STATUS_META[k.status]?.label||esc(k.status)}</span>
          </button>`).join('')}
    </div>`;
}

// renderTaskEstimate renders the effort-estimate editor — and renders nothing
// at all unless the project has switched estimation on and the task is an
// estimable leaf type. That absence is the feature: a team that does not
// estimate never sees an empty box teaching them to ignore the form.
//
// The points case adds Fibonacci chips as a shortcut; they are UI sugar over
// the same free 0–100 field, not a constrained scale. The number input is left
// empty for an unestimated task, which is why the handler distinguishes ''
// (clear) from '0' (a deliberate zero-effort estimate).
function renderTaskEstimate(task, row, immutable) {
  if (!taskEstimatable(S.project, task)) return '';
  const unit   = estimationUnit(S.project);
  const field  = estimationField(S.project);
  const value  = taskEstimate(S.project, task);
  const label  = estimateLabel(S.project);
  const limits = estimateLimits(unit);
  const input = `
    <input class="form-input form-input-sm estimate-input" id="task-estimate" type="number"
      inputmode="decimal" aria-label="${label}" placeholder="${t('task.estimateNone')}"
      min="0" max="${limits.max}" step="${limits.step}"
      value="${value === null ? '' : esc(String(value))}"
      data-change="updateTaskEstimate" data-a0="${esc(task.id)}" data-a1="${esc(field)}"${immutable?' disabled':''}>`;
  const chips = unit === 'POINTS'
    ? `<span class="estimate-presets">${FIBONACCI_POINTS.map(n => `
        <button type="button" class="estimate-chip${value === n ? ' is-active' : ''}"
          data-act="setTaskEstimatePreset" data-a0="${esc(task.id)}" data-a1="${esc(field)}" data-a2="${n}"
          title="${label}: ${n}"${immutable?' disabled':''}>${n}</button>`).join('')}</span>`
    : '';
  return row(label, `<span class="estimate-editor">${input}${unit === 'HOURS' ? `<span class="estimate-unit">${t('task.estimateHoursSuffix')}</span>` : ''}${chips}</span>`);
}

// renderTaskDates renders the Due Date editor plus the created/updated meta,
// the latter packed side-by-side into two sub-columns to save a line. It is
// appended to the detail field list in the left column.
function renderTaskDates(task, immutable) {
  const row = (label, valueHtml) => `
      <div class="detail-row">
        <span class="detail-label">${esc(label)}</span>
        <span class="detail-val">${valueHtml}</span>
      </div>`;
  return `
    <div class="detail-dates">
      ${renderTaskEstimate(task, row, immutable)}
      ${row(t('task.dueDateLabel'), `
        <input class="form-input form-input-sm" id="task-due-date" type="date" aria-label="${t('task.dueDateLabel')}"
          value="${task.dueDate ? task.dueDate.slice(0,10) : ''}"
          data-change="updateTaskField" data-a0="${esc(task.id)}" data-a1="dueDate"${immutable?' disabled':''}>`)}
      <div class="detail-date-cols">
        <span class="detail-date-col">${t('task.createdAt',{date:fmtDateTime(task.createdAt)})}</span>
        <span class="detail-date-col">${t('task.updatedAt',{date:fmtDateTime(task.updatedAt)})}</span>
      </div>
    </div>`;
}

// renderDetailActions renders the task action buttons shown at the bottom of the
// details tab. (The task version moved to the panel header's top-left.)
function renderDetailActions(task, immutable) {
  return `
    <div class="detail-actions">
      ${task.boardColumnId ? `<button class="btn btn-secondary btn-sm" data-act="moveTaskToColumn" data-a0="${esc(task.id)}" data-a1="">${t('task.moveToBacklog')}</button>` : ''}
      ${immutable
        ? `<button class="btn btn-secondary btn-sm" data-act="reopenTask" data-a0="${esc(task.id)}">${t('task.reopen')}</button>`
        : `<button class="btn btn-warning btn-sm" data-act="archiveTask" data-a0="${esc(task.id)}">${t('task.archive')}</button>`}
      ${AppPerms.can('task.delete') ? `<button class="btn btn-danger btn-sm" data-act="deleteTask" data-a0="${esc(task.id)}" data-a1="${esc((task.title))}">${t('form.delete')}</button>` : ''}
    </div>`;
}

// commentAuthorLabel resolves a comment's display name. The server now ships the
// author's name with each comment (authorName); fall back to the project-member
// lookup for older payloads so the UI never shows a raw author ID.
function commentAuthorLabel(c) {
  return c.authorName || memberName(c.authorId);
}

// renderCommentNode renders one comment and, recursively, its replies. Replies
// are nested in a .comment-replies container so CSS can indent the thread.
// When this comment is the one being edited (S.editingCommentId), its body is
// swapped for an inline rich-text editor instead of the static text + actions.
// Comments are deliberately NOT gated on the task's immutability. Immutability
// protects what a finished task *says* — its title, description, type, dates and
// workflow outcome are the historical record (see UpdateTask in the API). A
// comment is discussion about that record, not part of it, which is why the API
// has no immutability check on the comment routes at all. The panel used to hide
// the composer and every Reply button on a DONE/ARCHIVED task, a frontend-only
// freeze that contradicted both the API and the user guide, and that bit exactly
// when a retrospective note is most likely to be written.
function renderCommentNode(task, c, childrenByParent) {
  const name = commentAuthorLabel(c);
  const kids = childrenByParent[c.id] || [];
  const isOwn = S.user && c.authorId === S.user.id;
  const canEdit = isOwn;
  const editing = canEdit && S.editingCommentId === c.id;
  const edited = c.updatedAt && c.updatedAt !== c.createdAt;
  const bodyHtml = editing
    ? commentEditEditorHtml(task, c)
    : `
          <div class="comment-text rt-render">${renderDescriptionHTML(c.text)}</div>
          <div class="comment-actions">
            <button class="btn-text" data-act="replyComment" data-a0="${esc(task.id)}" data-a1="${esc(c.id)}">${t('task.reply')}</button>
            ${canEdit ? `<button class="btn-text" data-act="editComment" data-a0="${esc(task.id)}" data-a1="${esc(c.id)}">${t('form.edit')}</button>` : ''}
            ${isOwn ? `<button class="btn-text" data-act="deleteComment" data-a0="${esc(task.id)}" data-a1="${esc(c.id)}">${t('form.delete')}</button>` : ''}
          </div>`;
  return `
    <div class="comment-node" data-comment-id="${esc(c.id)}">
      <div class="comment comment-item">
        <div class="comment-body">
          <div class="comment-header">
            <div class="comment-author">${avatarHtml(name, c.authorId, S.usersMap[c.authorId]?.avatarUpdatedAt)}<strong>${esc(name)}</strong></div>
            <span class="comment-time">${fmtDateTime(c.createdAt)}${edited ? ` · <span class="comment-edited">${t('task.commentEdited')}</span>` : ''}</span>
          </div>
          ${bodyHtml}
          <div class="comment-reply-host" id="reply-host-${esc(c.id)}"></div>
        </div>
      </div>
      ${kids.length ? `<div class="comment-replies">${kids.map(k => renderCommentNode(task, k, childrenByParent)).join('')}</div>` : ''}
    </div>`;
}

// commentEditEditorHtml renders the inline rich-text editor shown in place of a
// comment's text when it is being edited. It mirrors the compose toolbar — file
// attach included, so a comment can gain an image while being corrected and not
// only when first written — and pre-fills the editor with the current rich text.
function commentEditEditorHtml(task, c) {
  const toolbar = RT_TOOLBAR.map(b => `
    <button type="button" class="rt-tool" data-act="rtCmdCommentEdit" data-a0="${esc(task.id)}" data-a1="${esc(b.cmd)}"
      aria-label="${t(b.key)}" title="${t(b.key)}">${b.svg ? icon(b.icon,{size:'sm'}) : esc(b.icon)}</button>`).join('');
  return `
    <div class="comment-compose comment-edit-compose">
      <div class="rt-toolbar" role="toolbar" aria-label="${t('editor.toolbar')}">
        ${toolbar}
        <span class="rt-tool-sep" aria-hidden="true"></span>
        <button type="button" class="rt-tool" data-act="rtAttach"
          aria-label="${t('editor.attachFile')}" title="${t('editor.attachFile')}">${icon('attach',{size:'sm'})}</button>
        <input type="file" id="rt-file-input-comment-edit" class="rt-file-input" data-change="rtFilePicked"
          data-a0="${esc(task.id)}" data-a1="#comment-edit-editor" aria-hidden="true" tabindex="-1">
      </div>
      <div class="rt-editor form-input" id="comment-edit-editor" contenteditable="true" role="textbox"
        aria-multiline="true" aria-label="${t('task.commentPlaceholder')}">${renderDescriptionHTML(c.text)}</div>
      <div class="detail-inline-actions">
        <button class="btn btn-primary btn-sm" data-act="saveEditComment" data-a0="${esc(task.id)}" data-a1="${esc(c.id)}">${t('form.save')}</button>
        <button class="btn btn-secondary btn-sm" data-act="cancelEditComment" data-a0="${esc(c.id)}">${t('form.cancel')}</button>
      </div>
    </div>`;
}

function renderTaskComments(task, comments) {
  // Group comments by parent, then render the forest from its roots. A comment
  // whose parent is missing from the payload is treated as a root so nothing is
  // silently dropped.
  const ids = new Set(comments.map(c => c.id));
  const childrenByParent = {};
  for (const c of comments) {
    const key = (c.parentId && ids.has(c.parentId)) ? c.parentId : '';
    (childrenByParent[key] = childrenByParent[key] || []).push(c);
  }
  const roots = childrenByParent[''] || [];
  return `
    <div class="comments-section">
      ${roots.map(c => renderCommentNode(task, c, childrenByParent)).join('')}
      ${commentEditorHtml(task)}
    </div>`;
}

// commentReplyEditorHtml renders the lightweight inline composer shown under a
// comment when the user clicks Reply. It submits via addComment with the parent
// comment id carried in data-a1.
function commentReplyEditorHtml(taskId, commentId) {
  return `
    <div class="comment-compose comment-reply-compose">
      <div class="rt-editor form-input" contenteditable="true" role="textbox" aria-multiline="true"
        data-reply-editor="${esc(commentId)}" data-placeholder="${t('task.replyPlaceholder')}"></div>
      <div class="detail-inline-actions">
        <button class="btn btn-primary btn-sm" data-act="addComment" data-a0="${esc(taskId)}" data-a1="${esc(commentId)}">${t('task.replySubmit')}</button>
        <button class="btn btn-secondary btn-sm" data-act="cancelReply" data-a0="${esc(commentId)}">${t('form.cancel')}</button>
      </div>
    </div>`;
}

// replyComment toggles an inline reply composer under the target comment. Only
// one reply box is open at a time.
function replyComment(taskId, commentId) {
  const host = document.getElementById('reply-host-' + commentId);
  if (!host) return;
  if (host.dataset.open === '1') { cancelReply(commentId); return; }
  document.querySelectorAll('.comment-reply-host[data-open="1"]').forEach(h => {
    h.innerHTML = ''; h.dataset.open = '';
  });
  host.dataset.open = '1';
  host.innerHTML = commentReplyEditorHtml(taskId, commentId);
  const ed = host.querySelector('.rt-editor');
  if (ed) ed.focus();
}

// cancelReply closes the inline reply composer for a comment.
function cancelReply(commentId) {
  const host = document.getElementById('reply-host-' + commentId);
  if (!host) return;
  host.innerHTML = '';
  host.dataset.open = '';
}

// commentEditorHtml renders the rich-text comment composer: the same formatting
// toolbar and inline attach button as the description editor, a contenteditable
// body, and a submit button.
function commentEditorHtml(task) {
  const toolbar = RT_TOOLBAR.map(b => `
    <button type="button" class="rt-tool" data-act="rtCmdComment" data-a0="${esc(task.id)}" data-a1="${esc(b.cmd)}"
      aria-label="${t(b.key)}" title="${t(b.key)}">${b.svg ? icon(b.icon,{size:'sm'}) : esc(b.icon)}</button>`).join('');
  return `
    <div class="comment-compose">
      <label id="new-comment-label" class="sr-only">${t('task.commentPlaceholder')}</label>
      <div class="rt-toolbar" role="toolbar" aria-label="${t('editor.toolbar')}">
        ${toolbar}
        <span class="rt-tool-sep" aria-hidden="true"></span>
        <button type="button" class="rt-tool" data-act="rtAttach"
          aria-label="${t('editor.attachFile')}" title="${t('editor.attachFile')}">${icon('attach',{size:'sm'})}</button>
        <input type="file" id="rt-file-input-comment" class="rt-file-input" data-change="rtFilePicked"
          data-a0="${esc(task.id)}" data-a1="#comment-editor" aria-hidden="true" tabindex="-1">
      </div>
      <div class="rt-editor form-input" id="comment-editor" contenteditable="true" role="textbox"
        aria-multiline="true" aria-labelledby="new-comment-label"
        data-placeholder="${t('task.commentPlaceholder')}"></div>
      <div class="detail-inline-actions">
        <button class="btn btn-primary btn-sm" data-act="addComment" data-a0="${esc(task.id)}">${t('task.commentSubmit')}</button>
      </div>
    </div>`;
}

function renderTaskLinks(task, links) {
  return `
    <div class="links-section">
      ${links.length ? links.map(l=>`
        <div class="link-row">
          <a class="link-url" href="${rtSafeHref(l.url) ? esc(l.url) : '#'}" target="_blank" rel="noopener">${esc(l.title || l.url)}</a>
          <button class="icon-btn" data-act="deleteLink" data-a0="${esc(task.id)}" data-a1="${esc(l.id)}" aria-label="${t('task.deleteLink')}" title="${t('task.deleteLink')}">${icon('delete')}</button>
        </div>`).join('') : `<div class="empty"><div class="empty-title">${t('task.noLinks')}</div></div>`}
      <div class="link-form">
        <input class="form-input" id="link-title" placeholder="${t('task.linkTitlePlaceholder')}" aria-label="${t('task.linkTitlePlaceholder')}"
          data-keydown="linkFormKeydown" data-a0="${esc(task.id)}">
        <input class="form-input" id="link-url" placeholder="${t('task.linkUrlPlaceholder')}" aria-label="${t('task.linkUrlPlaceholder')}"
          data-keydown="linkFormKeydown" data-a0="${esc(task.id)}">
        <button class="btn btn-secondary btn-sm" data-act="addLink" data-a0="${esc(task.id)}">${t('form.add')}</button>
      </div>
    </div>`;
}

// inverseRelationType mirrors the server's mapping (service.go): BLOCKS and
// BLOCKED_BY flip, RELATES_TO and DUPLICATES are self-inverse.
function inverseRelationType(rt) {
  return rt === 'BLOCKS' ? 'BLOCKED_BY' : rt === 'BLOCKED_BY' ? 'BLOCKS' : rt;
}

// relationTypeLabel resolves the localized label for a relation as seen from
// `task`'s side, falling back to the raw type for values without a label.
function relationTypeLabel(task, r) {
  const type = r.sourceTaskId === task.id ? r.relationType : inverseRelationType(r.relationType);
  const key = 'task.relationType.' + type;
  const label = t(key);
  return label !== key ? label : r.relationType;
}

// visibleRelations collapses the two stored directions of a relation into one
// row. The API writes a relation plus its symmetric inverse, and the list
// endpoint returns both, so without this a single "A blocks B" showed twice.
// A target-side row is kept only when its mirror is absent — relations created
// before the two-row model (e.g. seeded directly) exist as one row and must
// still appear, direction-flipped via relationTypeLabel.
function visibleRelations(task, relations) {
  return relations.filter(r => {
    if (r.sourceTaskId === task.id) return true;
    const inv = inverseRelationType(r.relationType);
    return !relations.some(r2 => r2.sourceTaskId === task.id
      && r2.targetTaskId === r.sourceTaskId && r2.relationType === inv);
  });
}

function renderTaskRelations(task, relations) {
  const candidates = (S.taskPanelData?.projectTasks || []).filter(tk => tk.id !== task.id);
  const rows = visibleRelations(task, relations);
  return `
    <div class="relations-section">
      ${rows.length ? rows.map(r=>{
        const otherId = r.sourceTaskId === task.id ? r.targetTaskId : r.sourceTaskId;
        const otherTitle = r._otherTitle || otherId;
        return `
        <div class="relation-row">
          <span class="relation-type">${esc(relationTypeLabel(task, r))}</span>
          <button type="button" class="link-url relation-target" data-act="openTaskPanel" data-a0="${esc(otherId)}">${esc(otherTitle)}</button>
          <button class="icon-btn" data-act="deleteRelation" data-a0="${esc(task.id)}" data-a1="${esc(r.id)}" aria-label="${t('task.deleteRelation')}" title="${t('task.deleteRelation')}">${icon('delete')}</button>
        </div>`;
      }).join('') : `<div class="empty"><div class="empty-title">${t('task.noRelations')}</div></div>`}
      <div class="link-form relation-form">
        <select class="form-select-sm" id="rel-type" aria-label="${t('task.relationTypeLabel')}">
          ${['RELATES_TO','BLOCKS','BLOCKED_BY','DUPLICATES'].map(rt =>
            `<option value="${rt}">${t('task.relationType.'+rt)}</option>`).join('')}
        </select>
        <select class="form-select-sm" id="rel-target" aria-label="${t('task.relationTargetLabel')}">
          <option value="">${t('task.selectRelatedTask')}</option>
          ${candidates.map(tk=>`<option value="${esc(tk.id)}">${esc((taskSeqLabel(tk)?taskSeqLabel(tk)+' — ':'')+tk.title)}</option>`).join('')}
        </select>
        <button class="btn btn-secondary btn-sm" data-act="addRelation" data-a0="${esc(task.id)}">${t('form.add')}</button>
      </div>
      ${renderTaskChildren(task)}
      ${renderRelationMap(task, rows)}
    </div>`;
}

// ── The Relations tab's dependency map ──────────────────────────────────────
// The lists above answer "what is linked here"; the map answers the question
// they cannot, which is which way the work flows. It reads flow left-to-right
// like the project mindmap: everything this task waits on sits to its left,
// everything waiting on it to its right, so "what unblocks me" is a glance
// rather than a reading of four relation-type labels.
//
// It draws no new data — same deduped relation rows the list renders, same
// project task list the hierarchy controls use — so the picture can never
// disagree with the text above it.
const REL_COL_W = 210, REL_ROW_H = 46, REL_NODE_W = 168, REL_NODE_H = 36, REL_PAD = 12;

// relationMapSides splits a task's links into the two columns flanking it.
// Direction is read from the task's own side (relationTypeLabel does the same),
// so a relation stored on the other task still lands on the correct side.
// Hierarchy joins the picture because the tab shows it too: a parent is
// something this task sits under (left), children hang off it (right).
function relationMapSides(task, rows) {
  const byId = new Map((S.taskPanelData?.projectTasks || []).map(tk => [tk.id, tk]));
  const left = [], right = [];
  for (const r of rows) {
    const otherId = r.sourceTaskId === task.id ? r.targetTaskId : r.sourceTaskId;
    const type = r.sourceTaskId === task.id ? r.relationType : inverseRelationType(r.relationType);
    const node = {
      id: otherId,
      title: r._otherTitle || byId.get(otherId)?.title || otherId,
      seq: taskSeqLabel(byId.get(otherId)) || '',
      status: byId.get(otherId)?.status || '',
      edge: relationTypeLabel(task, r),
    };
    (type === 'BLOCKED_BY' ? left : right).push(node);
  }
  const parent = task.parentId ? byId.get(task.parentId) : null;
  if (parent) {
    left.push({ id: parent.id, title: parent.title, seq: taskSeqLabel(parent) || '',
      status: parent.status, edge: t('task.parentLabel') });
  }
  const childType = typeChildOf(S.project, task.taskType);
  if (childType) {
    for (const kid of (S.taskPanelData?.projectTasks || []).filter(tk => tk.parentId === task.id)) {
      right.push({ id: kid.id, title: kid.title, seq: taskSeqLabel(kid) || '',
        status: kid.status, edge: t('task.children') });
    }
  }
  return { left, right };
}

// relationMapNodeHtml draws one flanking task as a button that opens its panel —
// the same affordance the rows above it carry, so the map is navigable and not
// merely decorative.
function relationMapNodeHtml(node, col, row) {
  const status = STATUS_META[node.status];
  const label = `${node.seq ? node.seq + ' ' : ''}${node.title} — ${node.edge}`;
  return `
    <button type="button" class="mm-node rel-node" style="left:${col * REL_COL_W + REL_PAD}px;top:${row * REL_ROW_H + REL_PAD}px"
      data-act="openTaskPanel" data-a0="${esc(node.id)}" aria-label="${esc(label)}" title="${esc(label)}">
      ${node.seq ? `<span class="task-seq">${esc(node.seq)}</span>` : ''}
      <span class="mm-label">${esc(node.title)}</span>
      ${status ? `<span class="mm-status ${esc(status.cls)}" aria-hidden="true"></span>` : ''}
    </button>`;
}

// renderRelationMap draws the map, or nothing at all when the task has no links
// in either direction — an empty canvas would only repeat what the "no
// relations" placeholder above already says.
function renderRelationMap(task, rows) {
  const { left, right } = relationMapSides(task, rows);
  if (!left.length && !right.length) return '';
  const lanes = Math.max(left.length, right.length, 1);
  const width = 3 * REL_COL_W + REL_PAD;
  const height = lanes * REL_ROW_H + REL_PAD;
  // The subject centres on the taller side so both fans stay symmetric about it.
  const centreRow = (lanes - 1) / 2;
  const rowOf = (list, i) => i + (lanes - list.length) / 2;
  const edge = (x1, y1, x2, y2) => {
    const mx = (x1 + x2) / 2;
    return `<path class="mm-link" d="M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"/>`;
  };
  const midY = row => row * REL_ROW_H + REL_PAD + REL_NODE_H / 2;
  const paths = [
    ...left.map((_, i) => edge(REL_PAD + REL_NODE_W, midY(rowOf(left, i)), REL_COL_W + REL_PAD, midY(centreRow))),
    ...right.map((_, i) => edge(REL_COL_W + REL_PAD + REL_NODE_W, midY(centreRow), 2 * REL_COL_W + REL_PAD, midY(rowOf(right, i)))),
  ].join('');
  const seq = taskSeqLabel(task);
  const subject = `
    <div class="mm-node rel-node rel-node-self" style="left:${REL_COL_W + REL_PAD}px;top:${centreRow * REL_ROW_H + REL_PAD}px">
      ${seq ? `<span class="task-seq">${esc(seq)}</span>` : ''}
      <span class="mm-label">${esc(task.title)}</span>
    </div>`;
  return `
    <div class="rel-map">
      <h3 class="rel-map-title">${t('task.relationMapTitle')}</h3>
      <div class="rel-map-legend">
        <span class="rel-map-legend-side">${t('task.relationMapWaitsOn')}</span>
        <span class="rel-map-legend-side">${t('task.relationMapBlocking')}</span>
      </div>
      <div class="mm-scroll rel-map-scroll" role="group" aria-label="${t('task.relationMapTitle')}">
        <div class="mm-canvas rel-map-canvas" style="width:${width}px;height:${height}px">
          <svg class="mm-links" width="${width}" height="${height}" aria-hidden="true">${paths}</svg>
          ${left.map((n, i) => relationMapNodeHtml(n, 0, rowOf(left, i))).join('')}
          ${subject}
          ${right.map((n, i) => relationMapNodeHtml(n, 2, rowOf(right, i))).join('')}
        </div>
      </div>
    </div>`;
}

// humanFileSize formats a byte count as a human-readable string.
function humanFileSize(bytes) {
  if (!bytes || bytes < 0) return '';
  const units = ['B','KB','MB','GB'];
  let n = bytes, i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(1)) + ' ' + units[i];
}
// fileTypeIcon returns an icon-registry name for an attachment based on its type.
function fileTypeIcon(att) {
  const ct = att.contentType || '';
  if (ct.startsWith('image/')) return 'image';
  if (ct === 'application/pdf') return 'doc';
  if (ct.startsWith('text/')) return 'page';
  if (ct.includes('zip')) return 'archive';
  if (att.externalUrl) return 'link';
  return 'attach';
}

// isPreviewableImage reports whether an uploaded attachment is an image that can
// be shown as an inline thumbnail.
function isPreviewableImage(a) {
  return !a.externalUrl && (a.contentType || '').startsWith('image/');
}

// attachmentListKey identifies what an attachment list currently renders as:
// the attachments themselves plus the language their labels were built in. The
// details tab stamps it on the list container so a repaint can tell whether the
// already-rendered list is still correct — see renderActiveTab.
function attachmentListKey(attachments) {
  const lang = (typeof document !== 'undefined' && document.documentElement?.lang) || '';
  return lang + '|' + (attachments || []).map(a => a.id).join(',');
}

// attachmentRowHtml renders one attachment row (used by the details sidebar
// and the task preview). Uploaded files open via an authenticated fetch (a plain link would
// 401 because the bearer token can't ride on a browser navigation); image
// uploads also show a small thumbnail. External links keep their behavior.
function attachmentRowHtml(task, a) {
  const name = esc(a.filename || '');
  const meta = a.sizeBytes ? `<span class="att-size text-muted text-sm">${esc(humanFileSize(a.sizeBytes))}</span>` : '';
  // Leading visual: a real thumbnail for previewable images, else a type icon.
  // Thumbnail <img> src is hydrated after render via the authenticated loader.
  const thumb = isPreviewableImage(a)
    ? `<button type="button" class="att-thumb" data-act="viewAttachment" data-a0="${esc(task.id)}" data-a1="${esc(a.id)}" aria-label="${name}" title="${name}"><img data-att-src="${esc(api.attachments.contentPath(task.id, a.id))}" alt="${name}" loading="lazy"></button>`
    : `<span class="att-icon" aria-hidden="true">${icon(fileTypeIcon(a),{size:'md'})}</span>`;
  const link = a.externalUrl
    ? `<a class="link-url att-name" href="${rtSafeHref(a.externalUrl) ? esc(a.externalUrl) : '#'}" target="_blank" rel="noopener">${name}</a>`
    : `<button type="button" class="link-url att-name att-view-btn" data-act="viewAttachment" data-a0="${esc(task.id)}" data-a1="${esc(a.id)}" title="${t('task.viewAttachment')}">${name}</button>`;
  return `
    <div class="link-row att-row">
      ${thumb}
      ${link}
      ${meta}
      <button class="icon-btn" data-act="deleteAttachment" data-a0="${esc(task.id)}" data-a1="${esc(a.id)}" aria-label="${t('task.deleteAttachment')}" title="${t('task.deleteAttachment')}">${icon('delete')}</button>
    </div>`;
}

// ── Authenticated image loading ──
// The content endpoint requires a bearer token, so auth-gated images cannot be
// rendered with a direct <img src>. We fetch the bytes once, cache an object URL
// per content path, and assign it to the matching <img> elements.
const _authBlobUrls = new Map();
function authBlobUrl(path) {
  if (_authBlobUrls.has(path)) return Promise.resolve(_authBlobUrls.get(path));
  return http.getBlob(path).then(blob => {
    const url = URL.createObjectURL(blob);
    _authBlobUrls.set(path, url);
    return url;
  });
}

// forgetAuthBlob drops (and revokes) the cached object URL for one content path
// — used after a delete, where the bytes can never be fetched again, so keeping
// the blob alive would only leak memory.
function forgetAuthBlob(path) {
  const url = _authBlobUrls.get(path);
  if (!url) return;
  URL.revokeObjectURL(url);
  _authBlobUrls.delete(path);
}

// contentPathOf normalizes an <img src> that points at the attachment content
// endpoint down to the relative API path getBlob expects (stripping any origin).
function contentPathOf(src) {
  if (!src) return '';
  let p = src;
  if (API_BASE && p.startsWith(API_BASE)) p = p.slice(API_BASE.length);
  else { try { p = new URL(src, window.location.href).pathname; } catch { /* keep as-is */ } }
  return /\/attachments\/[^/]+\/content$/.test(p) ? p : '';
}

// hydrateAuthImages swaps auth-gated image sources for object URLs within root:
//  - thumbnails we render with data-att-src (no src yet)
//  - inline rich-text images whose src points at the content endpoint
// Editors are skipped here and handled by hydrateRichEditors instead: their
// content is persisted verbatim, so the blob URL must never become the src
// without the real path being kept somewhere to put back (see rtEditorHtml).
function hydrateAuthImages(root) {
  if (!root) return;
  root.querySelectorAll('img[data-att-src], img[src*="/attachments/"]').forEach(img => {
    if (img.dataset.attHydrated === '1') return;
    if (img.closest('[contenteditable="true"]')) return;
    const path = img.dataset.attSrc || contentPathOf(img.getAttribute('src'));
    if (!path) return;
    img.dataset.attHydrated = '1';
    // Bytes we already hold are assigned synchronously, in the same task as the
    // render that created this <img>: going through a promise tick would leave
    // the element src-less across a repaint, which is part of what makes a
    // rebuilt thumbnail flash. Only an uncached image waits for the network.
    const cached = _authBlobUrls.get(path);
    if (cached) { img.src = cached; return; }
    authBlobUrl(path).then(url => { img.src = url; }).catch(() => {
      img.removeAttribute('src');
      img.alt = t('task.attachmentLoadFailed');
    });
  });
}

// ── Inline images inside a rich-text editor ──
// An attachment's bytes need the bearer token, so an <img> pointing straight at
// the content endpoint renders as a broken icon — which is what an image
// inserted into a description or a comment looked like until it was saved and
// re-rendered read-only. The editor therefore displays the blob URL while the
// path the server accepts is parked in data-att-path, and rtEditorHtml puts the
// path back before the content is read for saving. Persisting a blob: URL would
// not merely be ugly: rtSafeImageSrc rejects it, so the sanitizer would drop the
// whole <img> and the picture would vanish on save.
function hydrateRichEditors(root) {
  if (!root) return;
  root.querySelectorAll('.rt-editor[contenteditable="true"] img').forEach(img => {
    if (img.dataset.attHydrated === '1') return;
    const path = img.dataset.attPath || contentPathOf(img.getAttribute('src'));
    if (!path) return;
    img.dataset.attPath = path;
    img.dataset.attHydrated = '1';
    authBlobUrl(path).then(url => { img.src = url; }).catch(() => {
      // Leave data-att-path in place: the path is still what must be saved,
      // whether or not this browser could fetch the bytes to show it.
      img.removeAttribute('src');
      img.alt = t('task.attachmentLoadFailed');
    });
  });
}

// rtEditorHtml reads a rich-text editor's content as it must be PERSISTED,
// undoing the display-only src swap hydrateRichEditors applied. Every read of
// an editor's content goes through this — the draft, the dirty check, the save,
// the comment submit — because reading .innerHTML directly would capture the
// transient blob URLs and lose every inline image.
function rtEditorHtml(editor) {
  if (!editor) return '';
  if (!editor.querySelector('img[data-att-path]')) return editor.innerHTML;
  const clone = editor.cloneNode(true);
  clone.querySelectorAll('img[data-att-path]').forEach(img => {
    img.setAttribute('src', img.dataset.attPath);
    delete img.dataset.attPath;
    delete img.dataset.attHydrated;
  });
  return clone.innerHTML;
}

// ── Manual editor heights ──
// The editors carry a CSS resize handle. The panel repaints on every small field
// edit (renderActiveTab rebuilds the tab from scratch), so a height the user
// dragged has to be re-applied to the new element or the handle would appear not
// to work at all. Keyed by role rather than by task: it is a per-session
// preference for how tall this user likes the box, not a property of the task.
const _rtHeights = new Map();
let _rtHeightObserver = null;

function rtEditorRole(editor) {
  return editor.id || (editor.dataset.replyEditor ? 'reply' : '');
}

// trackRichEditorHeights re-applies the remembered heights inside root and
// watches the editors for new drags. The observer is rebuilt per render so it
// never holds on to editors that have been replaced.
function trackRichEditorHeights(root) {
  if (!root || typeof ResizeObserver === 'undefined') return;
  if (_rtHeightObserver) _rtHeightObserver.disconnect();
  _rtHeightObserver = _rtHeightObserver || new ResizeObserver(entries => {
    // Only an inline height means the user dragged the handle; content-driven
    // growth leaves style.height empty and must not be recorded as a choice.
    entries.forEach(e => {
      const role = rtEditorRole(e.target);
      if (role && e.target.style.height) _rtHeights.set(role, e.target.style.height);
    });
  });
  root.querySelectorAll('.rt-editor[contenteditable="true"]').forEach(editor => {
    const saved = _rtHeights.get(rtEditorRole(editor));
    if (saved) editor.style.height = saved;
    _rtHeightObserver.observe(editor);
  });
}

// viewAttachment fetches an uploaded file with auth and opens it in a new tab
// (images/PDF render inline; other types download). The blank tab is opened
// synchronously so the user gesture isn't lost to the async fetch.
async function viewAttachment(taskId, attId) {
  const win = window.open('', '_blank');
  try {
    const blob = await api.attachments.content(taskId, attId);
    const url = URL.createObjectURL(blob);
    if (win) win.location.href = url;
    else {
      // Popup blocked: fall back to a same-tab download via a transient anchor.
      const a = document.createElement('a');
      a.href = url; a.target = '_blank'; a.rel = 'noopener';
      document.body.appendChild(a); a.click(); a.remove();
    }
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  } catch (e) {
    if (win) win.close();
    toast(apiErrorMessage(e), 'error');
  }
}

// renderAttachmentSidebar refills the attachment side-column from the cached
// payload (used after an inline upload so the new file appears immediately).
function renderAttachmentSidebar() {
  const host = el('#att-sidebar-list');
  const d = S.taskPanelData;
  if (!host || !d) return;
  const atts = d.attachments || [];
  host.innerHTML = atts.length
    ? atts.map(a => attachmentRowHtml(d.task, a)).join('')
    : `<div class="empty att-empty"><div class="empty-title">${t('task.noAttachments')}</div></div>`;
  host.dataset.attKey = attachmentListKey(atts);
  hydrateAuthImages(host);
}

// attachmentSidebarHtml renders the attachment side-column shown next to the
// task details, including a drop zone for inline uploads.
function attachmentSidebarHtml(task, attachments) {
  const immutable = task.status === 'DONE' || task.status === 'ARCHIVED';
  return `
    <aside class="att-sidebar" aria-label="${t('task.attachmentsTab')}"
      ${immutable ? '' : `data-drop-attach="${esc(task.id)}"`}>
      <h3 class="att-sidebar-title">${t('task.attachmentsTab')}</h3>
      <div id="att-sidebar-list" data-att-key="${esc(attachmentListKey(attachments))}">
        ${(attachments && attachments.length)
          ? attachments.map(a => attachmentRowHtml(task, a)).join('')
          : `<div class="empty att-empty"><div class="empty-title">${t('task.noAttachments')}</div></div>`}
      </div>
      ${immutable ? '' : `
      <div class="att-sidebar-actions">
        <button class="btn btn-secondary btn-sm" data-act="rtAttach">${icon('attach',{size:'md'})} ${t('editor.attachFile')}</button>
        <p class="text-muted text-sm att-drop-hint">${t('editor.dropHint')}</p>
      </div>`}
    </aside>`;
}

function renderTaskBranches(task, branches, suggestion) {
  const prBadge = (s) => {
    if (!s) return '';
    const cls = {open:'badge-in-review', merged:'badge-done', declined:'badge-archived'}[s] || 'badge-planned';
    return `<span class="badge ${cls}">${t('task.branchStatus.'+s)}</span>`;
  };
  return `
    <div class="branches-section">
      ${branches.map(b=>`
        <div class="branch-item link-row">
          <code>${esc(b.branchName)}</code>
          ${b.prStatus ? prBadge(b.prStatus) : ''}
          ${b.prUrl ? `<a href="${esc(b.prUrl)}" target="_blank" rel="noopener" class="btn-text">${t('task.prNumber',{number:b.prNumber})}</a>`
                    : `<button class="btn-text" data-act="createPullRequest" data-a0="${esc(task.id)}" data-a1="${esc(b.id)}">${t('task.openPr')}</button>`}
          <button class="icon-btn" data-act="deleteBranch" data-a0="${esc(task.id)}" data-a1="${esc(b.id)}" aria-label="${t('task.deleteBranch')}" title="${t('task.deleteBranch')}">${icon('delete')}</button>
        </div>`).join('')}
      ${S.repos.length ? `
      <div class="branch-form">
        <input class="form-input" id="br-name" value="${esc(suggestion)}" placeholder="${t('task.branchNamePlaceholder')}">
        <select class="form-select-sm" id="branch-type">
          ${['feature','bugfix','hotfix','release'].map(bt=>`<option value="${bt}">${bt}</option>`).join('')}
        </select>
        <select class="form-select-sm" id="branch-repo">
          <option value="">${t('task.selectRepo')}</option>
          ${S.repos.map(r=>`<option value="${r.id}">${esc(r.displayName)}</option>`).join('')}
        </select>
        <button class="btn btn-secondary btn-sm" data-act="createBranch" data-a0="${esc(task.id)}">${t('task.linkBranch')}</button>
        <button class="btn-text" data-act="copyBranchName">${icon('copy',{size:'md'})} ${t('form.copy')}</button>
      </div>` : `<div class="empty"><div class="empty-title">${t('task.noRepoConnected')}</div></div>`}
    </div>`;
}

// The task panel's Activity tab. Newest first — the feed is paged (50 per page,
// see activity.PageSize), so appending older pages below is only coherent in
// that order; the unpaged version reversed its one page instead.
//
// A task's own activity never carries targetDeleted: an entry only gets it when
// the thing it described was deleted, and a deleted task has no panel to open.
function renderTaskActivity(entries, done) {
  if (!entries.length) return `<div class="empty"><div class="empty-title">${t('activity.empty')}</div></div>`;
  return `<div class="activity-list">
    ${entries.map(e=>`
      <div class="activity-item">
        <div class="activity-msg">${esc(activityMessage(e))}</div>
        <div class="activity-time">${fmtDateTime(e.createdAt)}</div>
      </div>`).join('')}
    ${done ? '' : `<button class="btn btn-secondary btn-sm" data-act="loadMoreTaskActivity">${t('activity.loadMore')}</button>`}
  </div>`;
}

// Mirrors activity.PageSize on the server; used only to tell a full page (there
// may be more) from a short one (there is not) without an X-Total-Count.
const TASK_ACTIVITY_PAGE_SIZE = 50;

// loadMoreTaskActivity appends the next older page into the cached panel payload
// and repaints the tab. Writing to S.taskPanelData is what makes the growth
// survive a switch to another tab and back, which renders from that cache.
async function loadMoreTaskActivity() {
  const d = S.taskPanelData;
  if (!d || d.activityDone) return;
  const page = Math.floor(d.activity.length / TASK_ACTIVITY_PAGE_SIZE);
  try {
    const more = await api.tasks.activity(d.taskId, { page });
    d.activity = d.activity.concat(more);
    d.activityDone = more.length < TASK_ACTIVITY_PAGE_SIZE;
  } catch (e) { toast(apiErrorMessage(e),'error'); return; }
  paintTaskPanel();
}

// TASK_ASSIGNED carries raw user ids (assigneeId and/or reviewerId, either of
// which is null when the field was cleared), so it has no single message: emit
// one localized part per field the write actually touched.
const ASSIGN_FIELDS = [['assigneeId', 'ASSIGNEE'], ['reviewerId', 'REVIEWER']];
function assignmentMessage(params) {
  return ASSIGN_FIELDS
    .filter(([field]) => field in params)
    .map(([field, key]) => params[field]
      ? t(`notifications.activity.TASK_ASSIGNED_${key}`, { name: memberName(params[field]) })
      : t(`notifications.activity.TASK_ASSIGNED_${key}_CLEARED`))
    .join(' · ');
}

// Builds a localized activity message from an entry's type + params, falling
// back to the raw type if no notifications.activity.<type> key is defined.
function activityMessage(e) {
  const key = 'notifications.activity.' + e.type;
  const params = { ...(e.params || {}) };
  // Status and priority arrive as enum names; render them the way badges do,
  // which also covers admin-defined custom priorities.
  if (e.type === 'TASK_STATUS_CHANGED' && STATUS_META[params.status]) {
    params.status = STATUS_META[params.status].label;
  }
  if (e.type === 'TASK_PRIORITY_CHANGED' && params.priority) {
    params.priority = priorityMeta(params.priority).label;
  }
  if (e.type === 'TASK_ASSIGNED') {
    const msg = assignmentMessage(params);
    if (msg) return msg;
  }
  // The estimation unit arrives as an enum name too; show the same wording the
  // project settings offer rather than a raw POINTS/HOURS.
  if (e.type === 'PROJECT_ESTIMATION_UNIT_CHANGED') {
    for (const k of ['from', 'to']) {
      if (ESTIMATION_UNITS.includes(params[k])) params[k] = t('taskSettings.estimationUnitOption.' + params[k]);
    }
  }
  const translated = t(key, params);
  return translated !== key ? translated : e.type;
}

async function switchPanelTab(tab) {
  if (S.taskPanelTab === tab) return;
  S.taskPanelTab = tab;
  // Re-render just the tab body from cache; the panel itself is not reloaded.
  if (S.taskPanelData && S.taskPanelData.taskId === S.taskPanelId) {
    await renderActiveTab();
  } else {
    await renderTaskPanel(S.taskPanelId);
  }
}

// saveTaskPatch patches a task carrying the version of the loaded panel
// snapshot, so an edit based on a stale snapshot is rejected by the server
// (409 VERSION_CONFLICT) instead of silently overwriting a concurrent editor's
// changes. The snapshot is refreshed from the response so follow-up edits
// carry the new version.
async function saveTaskPatch(taskId, d) {
  // A caller that already set d.version (the description save, whose draft may
  // predate an SSE panel refresh) wins over the current snapshot's version.
  const snap = S.taskPanelData?.task;
  if (d.version == null && snap && snap.id === taskId && snap.version) d.version = snap.version;
  const updated = await api.tasks.update(taskId, d);
  if (S.taskPanelData?.task?.id === taskId) S.taskPanelData.task = updated;
  invalidateProjectTasks();
  return updated;
}

// onTaskSaveError toasts a failed task edit; on a version conflict it also
// reloads the panel so the user immediately sees the state that won.
async function onTaskSaveError(e, taskId) {
  toast(apiErrorMessage(e), 'error');
  if (e && e.code === 'VERSION_CONFLICT') { await renderTaskPanel(taskId); await renderContent(); }
}

async function savePanelTitle(taskId, newTitle) {
  const title = newTitle.trim();
  if (!title) return;
  try {
    const updated = await saveTaskPatch(taskId, {title});
    // Keep the input's defaultValue (= last-saved title) current so a later
    // Escape reverts to this save, not to the originally loaded title.
    const input = el('#panel-title-input');
    if (input) input.defaultValue = title;
    toast(t('task.titleSaved'),'success');
    // The card's title updates in place. The panel is not repainted: it already
    // shows the new title (the user typed it), and rebuilding the input the
    // moment it loses focus would disturb wherever they are clicking next.
    await applyTaskUpdate(taskId, updated, { panel: 'none' });
  } catch(e) { await onTaskSaveError(e, taskId); }
}
// The header pencil exists to make the borderless title input discoverable;
// it just moves focus into the input.
function focusPanelTitle() {
  const input = el('#panel-title-input');
  if (input) { input.focus(); input.select(); }
}
// Enter commits the title (blur fires the change handler). Escape is handled
// by the app-wide keydown handler (framework.js), which must revert the
// pending edit *before* closing the panel — its focus restoration blurs the
// input, and a blur with an unreverted value would commit it.
function panelTitleKeydown(input, ev) {
  if (ev.key === 'Enter') { ev.preventDefault(); input.blur(); }
}
// resolveStatusBoard picks the board a status change reconciles against, and is
// the one place that decides what "status owns placement" may and may not do.
//
// A card already sitting on the visible board moves between that board's lanes.
// A task on no board at all joins the visible board — this is the case that used
// to be skipped, which is why setting a backlog task to IN_PROGRESS left it in
// the backlog and invisible to anyone reading the board. A task that belongs to
// some *other* board is left alone: its lanes are not the ones on screen.
//
// The sprint-board carve-out is deliberate. A sprint board's lanes are a
// sprint's committed scope, and the API refuses to enroll a new task in a
// running sprint (422 SPRINT_SCOPE_LOCKED). Joining a sprint is a planning
// decision, so it must not fall out of setting a status — an already-enrolled
// card still moves between the sprint board's lanes.
function resolveStatusBoard(task) {
  const board = S.board;
  if (!board?.columns) return null;
  if (board.columns.some(c => c.id === task.boardColumnId)) return board;
  return (task.boardColumnId || board.isSprintBoard) ? null : board;
}

// ── Completing a container over live work ────────────────────────────────────
//
// Marking a parent DONE while open children sit under it is PERMITTED by design
// — BLOCKER priority is this product's mechanism for holding a parent open, and
// widening the API guard to every open child reversed that once and locked a
// task out of DONE permanently (see TestCompleteTask_OpenNonBlockerDescendant-
// DoesNotBlock, and the note on task_repo.go's openDescendantsCTE). So the
// backend keeps saying yes; what was missing is that nothing told the user. An
// epic can be dropped in the Done lane over live work, answer 200, and the only
// roll-up this product has — sprint velocity, burndown, the release-close check
// — quietly counts it as finished.
//
// Hence a confirmation rather than a refusal, and it belongs at EVERY door that
// completes a task (panel status control, Done-lane drop, bulk set-status), or
// users learn to route around it through whichever door stayed silent.
const OPEN_DESCENDANT_SAMPLE = 3;

// The subtree walk itself moved to @octbase/shared/meta.js when the mobile
// companion grew the same warning (OCT-301) — mobile runs the identical walk
// over its own task list, and a second copy is the drift that package exists to
// prevent. What stays here is the desktop dialog around it.

// confirmCompletionOverOpenDescendants resolves true when the caller may go on
// completing taskIds. It asks only when there is something to say: no open
// descendants means no dialog, so the ordinary case of finishing a leaf task is
// untouched.
//
// It deliberately fails OPEN. If the task list can't be read, loadProjectTasks
// answers [] and this returns true rather than blocking a legitimate write on a
// failed side fetch — the confirmation is an affordance, not a guard, and the
// real guard (an open BLOCKER anywhere below) still runs server-side.
async function confirmCompletionOverOpenDescendants(taskIds) {
  const ids = (taskIds || []).filter(Boolean);
  if (!ids.length) return true;
  const open = openDescendantsOf(ids, await loadProjectTasks(S.project?.id));
  if (!open.length) return true;
  // taskLabel already prefixes the project key ("OCT-300 Warn before …"), which
  // is what a reader needs to go and look at the task that is still running.
  const names = open.slice(0, OPEN_DESCENDANT_SAMPLE).map(task => esc(taskLabel(task))).join(', ');
  const rest = open.length - Math.min(open.length, OPEN_DESCENDANT_SAMPLE);
  const listed = rest ? t('task.openDescendantsMore', { names, count: rest }) : names;
  const bodyKey = ids.length > 1 ? 'task.openDescendantsBodyBulk' : 'task.openDescendantsBody';
  const body = `${t(bodyKey, { count: open.length })}`
    + `<br><span class="form-hint">${t('task.openDescendantsList', { names: listed })}</span>`;
  return confirmModal(t('task.openDescendantsTitle'), body, t('task.completeAnyway'));
}

async function changeStatus(taskId, status) {
  if (status === 'DONE' && !await confirmCompletionOverOpenDescendants([taskId])) {
    // The select already shows DONE — repaint the tab so the control goes back
    // to the status the task actually has.
    await renderActiveTab();
    return;
  }
  try {
    // Status is the panel's only placement control, so a status change has to put
    // the card in the lane carrying that status — otherwise the board and the
    // task status diverge, which is exactly what the old board-column select was
    // there to paper over.
    const task = S.taskPanelData?.task;
    const board = task ? resolveStatusBoard(task) : null;
    const col = board ? board.columns.find(c => c.status === status) : null;
    if (col && col.id !== task.boardColumnId) {
      const movedTask = await api.boards.move(board.id, { taskId, boardColumnId: col.id, boardRank: 1000, version: task.version });
      if (S.taskPanelData?.task?.id === taskId) S.taskPanelData.task = movedTask;
    }
    const updated = await api.tasks.status(taskId, status, panelTaskVersion(taskId));
    toast(t('task.statusUpdated'),'success');
    await applyTaskUpdate(taskId, updated);
  } catch(e) { await onTaskSaveError(e, taskId); }
}
// panelTaskVersion returns the open panel snapshot's version for taskId, so
// quick actions carry it and a stale snapshot 409s instead of overwriting.
function panelTaskVersion(taskId) {
  const task = S.taskPanelData?.task;
  return (task && task.id === taskId) ? task.version : undefined;
}
async function changePriority(taskId, priority) {
  try {
    const updated = await api.tasks.priority(taskId,priority,panelTaskVersion(taskId));
    toast(t('task.priorityUpdated'),'success');
    await applyTaskUpdate(taskId, updated);
  } catch(e) { await onTaskSaveError(e, taskId); }
}
async function changeType(taskId, taskType) {
  const d = S.taskPanelData;
  if (d) delete d.pendingType;
  const rule = typeParentRule(S.project, taskType);
  const parent = d?.task?.parentId ? (d.projectTasks || []).find(tk => tk.id === d.task.parentId) : null;
  // Any level above the new type keeps the existing parent valid, so a retype
  // strands the parent far less often than when only rule.parentType counted.
  const parentOk = !!parent && typeParentAllowed(S.project, taskType, parent.taskType);
  if (rule.required && !parentOk && d) {
    // The new type needs a parent the task doesn't have: the server only
    // accepts taskType+parentId in one PATCH, so defer the save and let the
    // parent select (re-rendered with the new type's candidates) complete it.
    d.pendingType = taskType;
    await renderActiveTab();
    toast(t('task.pickParentHint'), 'info');
    return;
  }
  const patch = { taskType };
  if (parent && !parentOk) patch.parentId = null; // old parent is the wrong level for the new type
  // The type badge on the card updates in place; the panel is refetched because
  // a type change re-shapes the hierarchy controls (parent candidates, children).
  try { const updated = await saveTaskPatch(taskId,patch); toast(t('form.saved'),'success'); await applyTaskUpdate(taskId, updated, { panel: 'reload' }); } catch(e) { await onTaskSaveError(e, taskId); await renderTaskPanel(taskId); }
}
async function changeParent(taskId, parentId) {
  const d = S.taskPanelData;
  const patch = { parentId: parentId || null };
  if (d?.pendingType) { patch.taskType = d.pendingType; delete d.pendingType; }
  try { const updated = await saveTaskPatch(taskId,patch); toast(t('form.saved'),'success'); await applyTaskUpdate(taskId, updated, { panel: 'reload' }); } catch(e) { await onTaskSaveError(e, taskId); await renderTaskPanel(taskId); }
}
async function assignTask(taskId, userId, field) {
  const d = { version: panelTaskVersion(taskId) }; d[field] = userId||null;
  try {
    const updated = await api.tasks.assign(taskId,d);
    toast(t('form.updated'),'success');
    // The assignee avatar sits on the card face, so the board has to show the
    // new person straight away rather than on the next reload.
    await applyTaskUpdate(taskId, updated);
    await offerProjectMembership(taskId, userId);
  } catch(e) { await onTaskSaveError(e, taskId); }
}

// offerProjectMembership speaks up when the person just given work is a global
// admin who is not a member of this project. They administer the instance, but
// project access follows membership for everyone below Super Admin, so without
// one they get a 403 on the very task they were handed — a dead end the picker
// would otherwise create silently. A Super Admin needs no membership and is
// skipped.
//
// The membership is created through the ordinary endpoint, so the grant stays
// permission-checked and audited: nothing is added behind the actor's back, and
// an actor without project.invite_users (the same permission the API's
// CanAssignRole requires) is told who to ask instead.
async function offerProjectMembership(taskId, userId) {
  if (!userId || !S.project) return;
  const person = (S.assignables || []).find(m => m.userId === userId);
  if (!person || person.member !== false || person.globalRole === 'SUPER_ADMIN') return;

  if (!AppPerms.can('project.invite_users', S.project)) {
    toast(t('task.notMemberAsk', {name: person.name}), 'info');
    return;
  }
  const add = await confirmModal(
    t('task.notMemberTitle'),
    t('task.notMemberBody', {name: esc(person.name)}),
    t('task.notMemberAdd'));
  if (!add) return;
  try {
    await api.members.add(S.project.id, {userId, role: 'PROJECT_VIEWER'});
    S.assignables = await api.members.assignable(S.project.id).catch(() => S.assignables);
    toast(t('task.notMemberAdded', {name: person.name}), 'success');
    await renderTaskPanel(taskId);
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}
// updateTaskField saves one detail-row field (release, sprint, due date). All
// three are drawn on the board card, so the save propagates like every other
// panel edit instead of leaving the card stale until the next reload.
async function updateTaskField(taskId, field, value) {
  const d = {}; d[field] = value;
  try {
    const updated = await saveTaskPatch(taskId,d);
    toast(t('form.saved'),'success');
    await applyTaskUpdate(taskId, updated);
  } catch(e) { await onTaskSaveError(e, taskId); }
}

// updateTaskEstimate saves the estimate field. It goes through its own handler
// rather than updateTaskField because the value must reach the API as a
// *number* or null, never a string — parseEstimateInput (shared meta.js)
// holds the null-vs-0 contract.
async function updateTaskEstimate(taskId, field, raw) {
  const value = parseEstimateInput(raw);
  if (value === undefined) { toast(t('task.estimateInvalid'), 'error'); return; }
  await updateTaskField(taskId, field, value);
}

// setTaskEstimatePreset applies a Fibonacci chip. Clicking the active chip
// clears the estimate, so the shortcut can undo itself without reaching for
// the input.
async function setTaskEstimatePreset(taskId, field, n) {
  // The open panel's snapshot is the authority for "what is set right now";
  // the board's column lists are the fallback when the chip is somehow
  // clicked without a panel payload loaded.
  const task = (S.taskPanelData?.task?.id === taskId ? S.taskPanelData.task : null)
    || Object.values(S.tasksByCol || {}).flat().find(x => x.id === taskId);
  const value = task && task[field] === Number(n) ? null : Number(n);
  await updateTaskField(taskId, field, value);
}
// ── The description editor's dirty flag ─────────────────────────────────────
// taskDescriptionDirty answers "does the editor content differ from what is
// saved". Both sides are normalized the same way — the editor through
// sanitizeRichText, the saved text through renderDescriptionHTML — because that
// is what the editor was seeded with, so a keystroke that has since been undone
// reads as clean.
//
// This is the only comparison; the initial paint of the tab and the per-keystroke
// update both go through it, so the button state can't disagree with itself. The
// rendered original comes from the memo in framework.js instead of being rebuilt
// per call: it is by definition unchanged text, and re-sanitizing it was half the
// per-keystroke cost.
function taskDescriptionDirty(taskId, editorHtml) {
  return sanitizeRichText(editorHtml) !== renderedDescriptionOriginal(taskId);
}

// _descDirtyTail repaints the dirty flag after typing pauses. It reads the editor
// live rather than trusting the value from the keystroke that scheduled it — the
// same reason saveTaskDescription re-reads it — so the flag always describes the
// editor's real current content, whichever keystroke in the burst won.
//
// Coalescing this is safe because it drives nothing but two pieces of chrome (the
// Save button's disabled state and the status line). The two things that MUST NOT
// lag a keystroke — the draft itself and the pinned version — are written
// synchronously below.
const _descDirtyTail = debounced(200, taskId => {
  const editor = el('#pt-desc');
  if (!editor) return;
  const dirty = taskDescriptionDirty(taskId, rtEditorHtml(editor));
  const save = el('#pt-desc-save');
  const status = el('#pt-desc-status');
  if (save) save.disabled = !dirty;
  if (status) status.textContent = dirty ? t('form.unsavedChanges') : t('form.saved');
});

// updateTaskDescriptionDraft is called on every editor input. `value` is the raw
// contenteditable innerHTML.
function updateTaskDescriptionDraft(taskId, value) {
  // Pin the version this draft is based on at the first keystroke: an SSE
  // refresh of the open panel advances the snapshot's version, and saving with
  // that fresh version would silently overwrite the concurrent editor's change.
  if (!(taskId in S.taskDescriptionDraftVersions) && S.taskPanelData?.task?.id === taskId) {
    S.taskDescriptionDraftVersions[taskId] = S.taskPanelData.task.version;
  }
  // Store the editor's own HTML, unsanitized, and store it synchronously.
  //
  // Synchronously because a panel repaint (an SSE refresh, an in-place field
  // edit) reseeds the editor from this draft: a draft that lagged the editor by a
  // debounce interval would silently discard the last keystrokes.
  //
  // Unsanitized because sanitizing here bought nothing. The draft has exactly two
  // consumers and both already sanitize: it goes back into the DOM through
  // renderDescriptionHTML (descriptionEditorHtml) and to the server through
  // sanitizeRichText (saveTaskDescription, which re-reads the editor anyway, and
  // whose sanitize is untouched — as is the server's own). Running a full
  // DOMPurify parse of the whole description per keystroke to produce a value
  // whose every reader re-sanitizes it was pure overhead.
  S.taskDescriptionDrafts[taskId] = value;
  _descDirtyTail(taskId);
}
async function saveTaskDescription(taskId) {
  // Read straight from the editor so we never miss the last keystroke, then
  // sanitize client-side (defense-in-depth; the server re-sanitizes).
  const editor = el('#pt-desc');
  const raw = editor ? rtEditorHtml(editor) : (S.taskDescriptionDrafts[taskId] ?? S.taskDescriptionOriginals[taskId] ?? '');
  const value = sanitizeRichText(raw);
  const patch = { description: value };
  if (taskId in S.taskDescriptionDraftVersions) patch.version = S.taskDescriptionDraftVersions[taskId];
  try {
    const updated = await saveTaskPatch(taskId, patch);
    S.taskDescriptionOriginals[taskId] = value;
    delete S.taskDescriptionDrafts[taskId];
    delete S.taskDescriptionDraftVersions[taskId];
    toast(t('form.saved'),'success');
    await applyTaskUpdate(taskId, updated);
  } catch(e) {
    if (e && e.code === 'VERSION_CONFLICT') {
      // Keep the draft but drop the pinned version: the conflict has been
      // surfaced, so a repeated save is a deliberate overwrite (it will carry
      // the reloaded snapshot's fresh version).
      delete S.taskDescriptionDraftVersions[taskId];
      toast(t('task.descriptionConflict'), 'error');
      await renderTaskPanel(taskId);
      await renderContent();
      return;
    }
    await onTaskSaveError(e, taskId);
  }
}

// ── Rich-text editor commands ──────────────────────────────
// rtExec applies a formatting command to the current selection inside the
// editor using document.execCommand (still the simplest cross-browser path for
// a contenteditable; no external dependency). After mutating, it re-fires the
// draft update so dirty state and sanitization stay in sync.
// rtToggleBlock applies a block format (e.g. 'pre', 'h3') to the current line,
// or reverts it back to a normal paragraph if that format is already active.
// execCommand('formatBlock', 'pre') only ever sets the block — calling it again
// keeps it a <pre>, so without this toggle there is no way to switch the format
// off again. queryCommandValue reports the block tag at the caret so we can flip.
function rtToggleBlock(tag) {
  const current = (document.queryCommandValue('formatBlock') || '').toLowerCase();
  const target = current === tag ? 'p' : tag;
  document.execCommand('formatBlock', false, target);
}
// rtApplyCommand runs a formatting command against the given contenteditable.
async function rtApplyCommand(editor, cmd) {
  editor.focus();
  switch (cmd) {
    case 'bold':   document.execCommand('bold'); break;
    case 'italic': document.execCommand('italic'); break;
    case 'insertUnorderedList': document.execCommand('insertUnorderedList'); break;
    case 'insertOrderedList':   document.execCommand('insertOrderedList'); break;
    case 'heading': rtToggleBlock('h3'); break;
    case 'code':    rtToggleBlock('pre'); break;
    case 'link':    await rtInsertLink(editor); break;
  }
}
// rtInsertLink prompts for a URL via the app's own modal (promptModal, not
// window.prompt() — this app styles every other dialog itself) and applies it
// to the editor's current selection. Opening a modal moves focus into the
// dialog, which collapses the browser's Selection, so the Range is snapshotted
// before the modal opens and restored into the editor right before execCommand.
async function rtInsertLink(editor) {
  const sel = window.getSelection();
  const range = sel && sel.rangeCount ? sel.getRangeAt(0) : null;
  const url = await promptModal(t('editor.link'), t('editor.linkPrompt'), t('editor.insert'));
  if (!url) return;
  if (!rtSafeHref(url)) { toast(t('editor.linkInvalid'), 'error'); return; }
  editor.focus();
  if (range) {
    const restored = window.getSelection();
    restored.removeAllRanges();
    restored.addRange(range);
  }
  document.execCommand('createLink', false, url);
}
async function rtCmd(taskId, cmd) {
  const editor = el('#pt-desc');
  if (!editor) return;
  await rtApplyCommand(editor, cmd);
  updateTaskDescriptionDraft(taskId, rtEditorHtml(editor));
}
// rtCmdComment applies a formatting command to the comment composer editor.
async function rtCmdComment(taskId, cmd) {
  const editor = el('#comment-editor');
  if (!editor) return;
  await rtApplyCommand(editor, cmd);
}
// rtCmdCommentEdit applies a formatting command to the inline comment-edit
// editor (only one is open at a time, so its id is fixed).
async function rtCmdCommentEdit(taskId, cmd) {
  const editor = el('#comment-edit-editor');
  if (!editor) return;
  await rtApplyCommand(editor, cmd);
}

// rtEditorKeydown adds Ctrl/Cmd+B / +I shortcuts scoped to the editor so they
// never clash with the global command palette (Ctrl+K).
function rtEditorKeydown(node, ev) {
  if (!(ev.ctrlKey || ev.metaKey)) return;
  const k = ev.key.toLowerCase();
  if (k === 'b' || k === 'i') {
    ev.preventDefault();
    rtCmd(node.dataset.a1, k === 'b' ? 'bold' : 'italic');
  }
}

// Every attach button sits in the same block as the editor it fills — its own
// .rt-toolbar for the three composers, and the details tab's .detail-layout for
// the attachment sidebar's button, which has no toolbar of its own and belongs
// to the description. Finding the file input by walking up from the button (not
// by a fixed id) is what lets the comments tab hold two composers — the one at
// the bottom and an inline edit — without their attach buttons colliding.
const RT_ATTACH_HOSTS = '.rt-toolbar, .comment-compose, .detail-desc, .detail-layout';

// rtAttach triggers the hidden file input belonging to the clicked button.
function rtAttach(node) {
  const host = node.closest(RT_ATTACH_HOSTS);
  const input = host && host.querySelector('.rt-file-input');
  if (input) input.click();
}
function rtFilePicked(node) {
  const files = node.files;
  if (files && files.length) rtUploadFiles(node.dataset.a0, Array.from(files), node.dataset.a1);
  node.value = '';
}

// rtUploadFiles uploads one or more files via the multipart endpoint, shows
// progress toasts, refreshes the attachment sidebar, and offers to insert
// successfully-uploaded images inline. `target` is a selector for the editor
// that should receive the image — the composer the user attached from, so an
// image added while writing a comment lands in that comment and not in the
// description that happens to share the panel.
async function rtUploadFiles(taskId, files, target) {
  for (const file of files) {
    try {
      toast(t('editor.uploading', { name: file.name }), 'info');
      const att = await api.attachments.upload(taskId, file);
      // Refresh the cached attachment list + details sidebar so the new file
      // (and its thumbnail) appears immediately.
      if (S.taskPanelData && S.taskPanelData.taskId === taskId) {
        S.taskPanelData.attachments = await api.attachments.list(taskId).catch(() => S.taskPanelData.attachments);
        renderAttachmentSidebar();
      }
      toast(t('editor.uploadSuccess', { name: att.filename }), 'success');
      if ((att.contentType || '').startsWith('image/')) {
        // Same modal overlay as confirmDelete, not a native confirm dialog —
        // awaited so multiple images uploaded at once prompt one at a time.
        // esc() the filename: confirmModal renders its body via innerHTML, and
        // an uploaded filename is attacker-controlled (only path/control-char
        // sanitized server-side, never HTML-escaped there).
        if (await confirmModal(t('editor.insert'), t('editor.insertImagePrompt', { name: esc(att.filename) }), t('editor.insert'))) {
          insertImageIntoEditor(taskId, att, target);
        }
      }
    } catch (e) {
      toast(apiErrorMessage(e), 'error');
    }
  }
}

// insertImageIntoEditor inserts an <img> referencing the authenticated content
// endpoint (relative path — the only image src the sanitizer permits) at the
// caret, and updates the draft. The inserted image is hydrated straight away so
// the user sees the actual picture rather than a broken-image icon; the path
// stays in data-att-path and rtEditorHtml restores it on save.
function insertImageIntoEditor(taskId, att, target) {
  const editor = (target && el(target)) || el('#pt-desc') || el('#comment-editor');
  if (!editor) return;
  editor.focus();
  const src = api.attachments.contentPath(taskId, att.id);
  const img = document.createElement('img');
  img.setAttribute('src', src);
  img.setAttribute('alt', att.filename || '');
  img.setAttribute('loading', 'lazy');
  const sel = window.getSelection();
  if (sel && sel.rangeCount && editor.contains(sel.anchorNode)) {
    const range = sel.getRangeAt(0);
    range.collapse(false);
    range.insertNode(img);
    range.setStartAfter(img);
    sel.removeAllRanges();
    sel.addRange(range);
  } else {
    editor.appendChild(img);
  }
  hydrateRichEditors(editor.parentNode || editor);
  // Only the description editor tracks a save-draft; the comment editors don't.
  if (editor.id === 'pt-desc') updateTaskDescriptionDraft(taskId, rtEditorHtml(editor));
}

// ═══════════════════════════════════════════════════════════
// TASK PREVIEW OVERLAY + LIGHTBOX
// ═══════════════════════════════════════════════════════════
// The preview is a read-mostly overlay (role=dialog, aria-modal) that shows the
// formatted description together with the task's attachments, rendering image
// attachments inline as a thumbnail grid. It never changes the route/hash and
// reuses modal-style focus management. Image data is lazy-loaded (loading="lazy"
// on thumbnails). Clicking a thumbnail opens a minimal vanilla lightbox.
let _previewReturnFocus = null;
let _previewKeydownHandler = null;
let _lightboxImages = [];
let _lightboxIndex = 0;

// previewAttachmentsHtml renders the preview's two attachment sections — image
// thumbnails and a file list — from an attachment list. Kept separate from the
// overlay shell so the sections can be repainted on their own after a delete,
// which also drops a heading whose section has become empty.
function previewAttachmentsHtml(task, attachments) {
  const atts = attachments || [];
  const images = atts.filter(isPreviewableImage);
  const files = atts.filter(a => !isPreviewableImage(a));
  return `
    ${images.length ? `
    <h3 class="preview-section-title">${t('task.imagesTab')}</h3>
    <div class="preview-thumbs">
      ${images.map((a,i)=>`
        <button type="button" class="preview-thumb" data-act="openLightbox" data-a0="${esc(String(i))}"
          aria-label="${esc(a.filename || t('task.imagesTab'))}">
          <img data-att-src="${esc(api.attachments.contentPath(task.id, a.id))}" alt="${esc(a.filename || '')}" loading="lazy">
        </button>`).join('')}
    </div>` : ''}
    ${files.length ? `
    <h3 class="preview-section-title">${t('task.attachmentsTab')}</h3>
    <div class="preview-files">${files.map(a => attachmentRowHtml(task, a)).join('')}</div>` : ''}`;
}

// lightboxImagesOf builds the lightbox dataset from the same image subset the
// thumbnails render, so the index a thumbnail carries always addresses the same
// picture — they must be derived together whenever the sections are repainted.
function lightboxImagesOf(task, attachments) {
  return (attachments || []).filter(isPreviewableImage)
    .map(a => ({ path: api.attachments.contentPath(task.id, a.id), alt: a.filename || '' }));
}

// renderPreviewAttachments refills the open preview overlay's attachment
// sections from the cached payload. No-ops when the preview is closed.
function renderPreviewAttachments() {
  const overlay = el('#preview-overlay');
  const d = S.taskPanelData;
  if (!overlay || overlay.classList.contains('hidden') || !d) return;
  const host = overlay.querySelector('#preview-attachments');
  if (!host) return;
  host.innerHTML = previewAttachmentsHtml(d.task, d.attachments);
  hydrateAuthImages(host);
  _lightboxImages = lightboxImagesOf(d.task, d.attachments);
}

async function openTaskPreview(taskId) {
  const d = S.taskPanelData;
  // Lazy-load attachments when the preview opens (use cache if it's this task).
  let task, attachments;
  if (d && d.taskId === taskId) {
    task = d.task;
    attachments = d.attachments || [];
  } else {
    [task, attachments] = await Promise.all([
      api.tasks.get(taskId),
      api.attachments.list(taskId).catch(() => []),
    ]);
  }
  const overlay = el('#preview-overlay');
  if (!overlay) return;
  overlay.innerHTML = `
    <div class="preview-dialog" role="dialog" aria-modal="true" aria-labelledby="preview-title" tabindex="-1">
      <div class="preview-header">
        <h2 id="preview-title" class="preview-title">${esc(taskLabel(task) || task.title)}</h2>
        <button class="icon-btn" data-act="closeTaskPreview" aria-label="${t('task.close')}" title="${t('task.close')}">${icon('close')}</button>
      </div>
      <div class="preview-body">
        <div class="preview-description rt-render">${renderDescriptionHTML(task.description) || `<p class="text-muted">${t('task.noDescription')}</p>`}</div>
        <div id="preview-attachments">${previewAttachmentsHtml(task, attachments)}</div>
      </div>
    </div>`;
  overlay.classList.remove('hidden');
  hydrateAuthImages(overlay);

  // Lightbox dataset (only inline images). Store the relative content path so the
  // lightbox can load full-size bytes through the authenticated image loader.
  _lightboxImages = lightboxImagesOf(task, attachments);

  // Focus management mirroring the modal: trap Tab, Esc closes, restore focus.
  _previewReturnFocus = document.activeElement;
  const dialog = overlay.querySelector('.preview-dialog');
  (dialog || overlay).focus();
  _previewKeydownHandler = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); closeTaskPreview(); return; }
    if (e.key !== 'Tab') return;
    const items = Array.from(overlay.querySelectorAll('button, a[href], [tabindex]:not([tabindex="-1"])'))
      .filter(elm => !elm.disabled && elm.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  };
  overlay.addEventListener('keydown', _previewKeydownHandler);
}

function closeTaskPreview() {
  const overlay = el('#preview-overlay');
  if (!overlay) return;
  if (_previewKeydownHandler) overlay.removeEventListener('keydown', _previewKeydownHandler);
  _previewKeydownHandler = null;
  overlay.classList.add('hidden');
  overlay.innerHTML = '';
  if (_previewReturnFocus && document.body.contains(_previewReturnFocus)) _previewReturnFocus.focus();
  _previewReturnFocus = null;
}

// openLightbox opens the minimal image viewer over the preview. Esc / click-out
// close it; ArrowLeft/Right and prev/next buttons cycle multiple images.
function openLightbox(index) {
  _lightboxIndex = Number(index) || 0;
  const lb = el('#lightbox');
  if (!lb || !_lightboxImages.length) return;
  renderLightbox();
  lb.classList.remove('hidden');
  _lightboxReturnFocus = document.activeElement;
  lb.focus();
}
let _lightboxReturnFocus = null;
function renderLightbox() {
  const lb = el('#lightbox');
  const img = _lightboxImages[_lightboxIndex];
  if (!lb || !img) return;
  const multi = _lightboxImages.length > 1;
  lb.innerHTML = `
    <div class="lightbox-inner" role="dialog" aria-modal="true" aria-label="${esc(img.alt || t('task.imagesTab'))}">
      <button class="lightbox-close icon-btn" data-act="closeLightbox" aria-label="${t('task.close')}" title="${t('task.close')}">${icon('close')}</button>
      ${multi ? `<button class="lightbox-nav lightbox-prev icon-btn" data-act="lightboxPrev" aria-label="${t('editor.prevImage')}" title="${t('editor.prevImage')}">${icon('chevron-left',{size:'md'})}</button>` : ''}
      <img class="lightbox-img" data-att-src="${esc(img.path)}" alt="${esc(img.alt)}">
      ${multi ? `<button class="lightbox-nav lightbox-next icon-btn" data-act="lightboxNext" aria-label="${t('editor.nextImage')}" title="${t('editor.nextImage')}">${icon('chevron-right',{size:'md'})}</button>` : ''}
    </div>`;
  hydrateAuthImages(lb);
}
function closeLightbox() {
  const lb = el('#lightbox');
  if (!lb) return;
  lb.classList.add('hidden');
  lb.innerHTML = '';
  if (_lightboxReturnFocus && document.body.contains(_lightboxReturnFocus)) _lightboxReturnFocus.focus();
  _lightboxReturnFocus = null;
}
function lightboxPrev() {
  if (!_lightboxImages.length) return;
  _lightboxIndex = (_lightboxIndex - 1 + _lightboxImages.length) % _lightboxImages.length;
  renderLightbox();
}
function lightboxNext() {
  if (!_lightboxImages.length) return;
  _lightboxIndex = (_lightboxIndex + 1) % _lightboxImages.length;
  renderLightbox();
}

async function moveTaskToColumn(taskId, colId) {
  if (!S.board) {
    toast(t('errors.noBoardAvailable'), 'error');
    return;
  }
  try {
    // Track the task's post-move snapshot so the board card can be moved in
    // place (see applyBoardTaskUpdate) instead of reloading the whole board.
    let snapshot = null;
    if (colId) {
      const movedTask = await api.boards.move(S.board.id, { taskId, boardColumnId: colId, boardRank: 1000, version: panelTaskVersion(taskId) });
      snapshot = movedTask;
      const col = S.board.columns?.find(column => column.id === colId);
      if (col) snapshot = await api.tasks.status(taskId, col.status, movedTask?.version).catch(() => movedTask);
    } else {
      // Removed from the board: remove-task answers with the updated task, so
      // its now-null boardColumnId drops the card from the lanes in place with
      // no second read. (It used to re-GET the task here, which described the
      // same row the response already carried.)
      snapshot = await api.boards.remove(S.board.id, taskId);
    }
    toast(t('task.moved'), 'success');
    // With a fresh snapshot everything updates in place; only refetch the panel
    // when we couldn't read the moved task back.
    await applyTaskUpdate(taskId, snapshot, { panel: snapshot ? 'inplace' : 'reload' });
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}
async function archiveTask(taskId) {
  try { const updated = await api.tasks.archive(taskId); await applyTaskUpdate(taskId, updated); } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function reopenTask(taskId) {
  try { const updated = await api.tasks.reopen(taskId); await applyTaskUpdate(taskId, updated); } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function deleteTask(taskId, title) {
  confirmDelete(t('task.deleteTitle'), t('task.deleteConfirm',{title}), async () => {
    await api.tasks.del(taskId);
    invalidateProjectTasks();
    closeTaskPanel();
    // The row/card is dropped from whichever view is showing it in place; a view
    // with no in-place path re-renders.
    if (!(applyBoardTaskRemoval(taskId) || applyListTaskRemoval(taskId))) await renderContent();
  });
}

// addComment posts a top-level comment (parentId omitted) or, when a parentId is
// supplied by an inline reply composer, a threaded reply to that comment.
async function addComment(taskId, parentId) {
  const inp = parentId
    ? document.querySelector(`[data-reply-editor="${parentId}"]`)
    : el('#comment-editor');
  if (!inp) return;
  // Sanitize client-side (defense-in-depth; the server re-sanitizes). Reject
  // only when there's neither text nor an inserted image — the composer
  // supports image-only comments (rtAttach/insertImageIntoEditor), so gating
  // on bare text content alone would wrongly reject those.
  const text = sanitizeRichText(rtEditorHtml(inp));
  if(!inp.textContent.trim() && !/<img\b/i.test(text)) { toast(t('validation.commentRequired'),'error'); return; }
  try {
    await api.comments.add(taskId, text, parentId);
    S.taskPanelTab = 'comments';
    await renderTaskPanel(taskId);
    toast(parentId ? t('task.replyAdded') : t('task.commentAdded'),'success');
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function deleteComment(taskId, commentId) {
  try { await api.comments.del(taskId,commentId); await renderTaskPanel(taskId); } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// editComment swaps the target comment for an inline editor. Only one comment is
// editable at a time; opening one closes any other and any open reply box.
async function editComment(taskId, commentId) {
  S.editingCommentId = commentId;
  await renderActiveTab();
  const ed = el('#comment-edit-editor');
  if (ed) {
    ed.focus();
    // Place the caret at the end of the pre-filled text.
    const range = document.createRange();
    range.selectNodeContents(ed);
    range.collapse(false);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }
}

// cancelEditComment discards the inline editor and restores the comment text.
async function cancelEditComment(commentId) {
  S.editingCommentId = null;
  await renderActiveTab();
}

// saveEditComment persists the inline edit. The text is sanitized client-side
// (the server re-sanitizes) and rejected if empty.
async function saveEditComment(taskId, commentId) {
  const ed = el('#comment-edit-editor');
  if (!ed) return;
  const text = sanitizeRichText(rtEditorHtml(ed));
  if (!ed.textContent.trim() && !/<img\b/i.test(text)) { toast(t('validation.commentRequired'),'error'); return; }
  // Carry the loaded comment's version so an edit based on a stale comment
  // 409s instead of overwriting a concurrent editor's text.
  const loaded = (S.taskPanelData?.comments || []).find(c => c.id === commentId);
  try {
    await api.comments.update(taskId, commentId, text, loaded?.version);
    S.editingCommentId = null;
    S.taskPanelTab = 'comments';
    await renderTaskPanel(taskId);
    toast(t('task.commentUpdated'),'success');
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// linkFormKeydown submits the Links-tab form on Enter, so adding a URL doesn't
// require reaching for the Add button.
function linkFormKeydown(node, ev) {
  if (ev.key === 'Enter') { ev.preventDefault(); addLink(node.dataset.a0); }
}
async function addLink(taskId) {
  const url = el('#link-url')?.value?.trim();
  const title = el('#link-title')?.value?.trim();
  if(!url) { toast(t('validation.urlRequired'),'error'); return; }
  if(!rtSafeHref(url)) { toast(t('validation.urlUnsafe'),'error'); return; }
  try {
    await api.links.add(taskId, {url, title});
    toast(t('task.linkAdded'),'success');
    S.taskPanelTab = 'links';
    await renderTaskPanel(taskId);
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function deleteLink(taskId, linkId) {
  try { await api.links.del(taskId,linkId); await renderTaskPanel(taskId); toast(t('task.linkRemoved'),'success'); } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// addRelation creates a task-to-task relation from the Relations tab form. The
// panel is refetched afterwards, which re-reads the relation list (and thus
// shows what the server actually stored) rather than trusting the write.
async function addRelation(taskId) {
  const relationType = el('#rel-type')?.value;
  const targetTaskId = el('#rel-target')?.value;
  if(!targetTaskId) { toast(t('task.relationTargetRequired'),'error'); return; }
  try {
    await api.relations.add(taskId, {targetTaskId, relationType});
    toast(t('task.relationAdded'),'success');
    S.taskPanelTab = 'relations';
    await renderTaskPanel(taskId);
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function deleteRelation(taskId, relationId) {
  try { await api.relations.del(taskId,relationId); await renderTaskPanel(taskId); } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// deleteAttachment removes one attachment and updates what is on screen from the
// cached payload: the details sidebar, plus the preview overlay when it is open.
// It deliberately does not re-run renderTaskPanel — refetching the whole panel
// for a one-row change blanked it behind a spinner, reset the scroll position and
// threw away an unsaved description draft, which reads as a page reload. The
// inline upload path already updates in place the same way.
async function deleteAttachment(taskId, attachmentId) {
  try {
    await api.attachments.del(taskId, attachmentId);
    const d = S.taskPanelData;
    if (d && d.taskId === taskId) {
      d.attachments = (d.attachments || []).filter(a => a.id !== attachmentId);
      renderAttachmentSidebar();
      renderPreviewAttachments();
    }
    // The bytes are gone, so the object URL cached for them can never be valid
    // again — revoke it instead of holding the blob for the rest of the session.
    forgetAuthBlob(api.attachments.contentPath(taskId, attachmentId));
    toast(t('task.attachmentRemoved'), 'success');
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}

async function createBranch(taskId) {
  const name = el('#br-name')?.value?.trim();
  const type = el('#branch-type')?.value || 'feature';
  const repo = el('#branch-repo')?.value;
  if(!name || !repo) { toast(t('validation.branchRequired'),'error'); return; }
  try {
    await api.branches.create(taskId, {branchName:name, branchType:type, repositoryId:repo});
    toast(t('task.branchLinked'),'success');
    S.taskPanelTab = 'branches';
    await renderTaskPanel(taskId);
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function deleteBranch(taskId, branchId) {
  try { await api.branches.del(taskId,branchId); await renderTaskPanel(taskId); toast(t('task.branchRemoved'),'success'); } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function createPullRequest(taskId, branchId) {
  try {
    await api.branches.createPullRequest(taskId, branchId, {});
    toast(t('task.prCreated'),'success');
    S.taskPanelTab = 'branches';
    await renderTaskPanel(taskId);
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// ═══════════════════════════════════════════════════════════
// RELEASES
// ═══════════════════════════════════════════════════════════

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([closeTaskPanel, focusPanelTitle, loadMoreTaskActivity], _A0);
registerActions([
  addLink, addRelation, archiveTask, createBranch, openTaskPanel, reopenTask,
  saveTaskDescription, switchPanelTab, openTaskPreview, openLightbox, cancelReply,
  cancelEditComment,
], _A1);
registerActions([
  addComment, deleteAttachment, deleteBranch, deleteComment, deleteLink, deleteRelation,
  deleteTask, moveTaskToColumn, createPullRequest, rtCmdComment, replyComment, viewAttachment,
  editComment, saveEditComment, rtCmdCommentEdit,
], _A2);
registerActions([setTaskEstimatePreset], _A3);
registerActions({
  rtCmd:         el => rtCmd(el.dataset.a0, el.dataset.a1),
  // Not _A1: the attach button carries no task id — it finds its own file
  // input by walking up from itself, so the two composers the comments tab
  // can show at once each fill their own editor (rtAttach below).
  rtAttach:      node => rtAttach(node),
  closeTaskPreview:  () => closeTaskPreview(),
  closeTaskPreviewBackdrop: (el, ev) => { if (ev.target === el) closeTaskPreview(); },
  closeLightbox:     () => closeLightbox(),
  closeLightboxBackdrop: (el, ev) => { if (ev.target === el) closeLightbox(); },
  lightboxPrev:  () => lightboxPrev(),
  lightboxNext:  () => lightboxNext(),
  copyBranchName: () => navigator.clipboard.writeText(el('#br-name').value)
                          .then(() => toast(t('form.copied'), 'success'))
                          .catch(() => {}),
});
registerChanges([changePriority, changeStatus, changeType, changeParent, savePanelTitle], _VAL);
registerChanges({
  assignTask:        el => assignTask(el.dataset.a0, el.value, el.dataset.a1),
  updateTaskField:   el => updateTaskField(el.dataset.a0, el.dataset.a1, el.value || null),
  // Not `el.value || null`: an estimate of 0 is a deliberate value, and
  // only an empty box means unestimated. updateTaskEstimate makes that call.
  updateTaskEstimate: el => updateTaskEstimate(el.dataset.a0, el.dataset.a1, el.value),
  rtFilePicked:      node => rtFilePicked(node),
});
registerInputs({
  // The description editor is a contenteditable element (no .value); read its
  // HTML instead — through rtEditorHtml, which swaps the blob URLs inline
  // images are DISPLAYED with back for the paths that get saved.
  updateTaskDescriptionDraft: node => updateTaskDescriptionDraft(node.dataset.a0, rtEditorHtml(node)),
});
registerKeydowns({
  rtEditorKeydown:   (el, ev) => rtEditorKeydown(el, ev),
  panelTitleKeydown: (el, ev) => panelTitleKeydown(el, ev),
  linkFormKeydown:   (el, ev) => linkFormKeydown(el, ev),
});

export { activityMessage, addComment, changeParent, changePriority, changeStatus, changeType, closeLightbox, closeTaskPanel, commentEditEditorHtml, confirmCompletionOverOpenDescendants, deleteAttachment, deleteComment, deleteTask, editComment, invalidateProjectTasks, lightboxNext, lightboxPrev, openTaskPanel, personOptions, relationMapSides, renderTaskComments, renderTaskDates, renderTaskPanel, replyComment, resolveStatusBoard, rtAttach, rtEditorHtml, rtUploadFiles, saveEditComment, updateTaskField, viewAttachment };
