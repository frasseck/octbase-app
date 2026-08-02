import { t } from '@octbase/shared/i18n.js';
import { api } from './api.js';
import { _A0, _A1, _A2, registerActions, registerInputs } from './delegation.js';
import { confirmDelete, el, esc, fmtDateTime, hideModal, memberName, showModal, taskLabel, toast, typeBadge } from './framework.js';
import { apiErrorMessage } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { Views } from './registry.js';
import { S, taskMetaById } from './state.js';
import { BOARD_RANK_STEP, revealBoardCard } from './views-board.js';
import { activityMessage } from './views-task.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
async function renderPages() {
  S.pages = await api.pages.list(S.project.id);
  const c = el('#content');
  c.classList.add('content-pages');

  if(!S.pages.length && !S.selectedPage) {
    // Page creation goes through writerGuard, so a viewer gets a 403 from it.
    // Same gate as viewCreateButton (the empty state replaces that button).
    c.innerHTML=`<div class="empty"><div class="empty-icon">${icon('page',{size:'hero'})}</div><div class="empty-title">${t('page.emptyTitle')}</div>${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary" data-act="showCreatePage">${icon('add',{size:'md'})} ${t('page.new')}</button>`}</div>`;
    return;
  }

  c.innerHTML=`
    <div class="content-toolbar pages-toolbar">
      <label class="sr-only" for="pages-search">${t('page.searchLabel')}</label>
      <input type="search" class="form-input form-input-sm task-search-input" id="pages-search" value="${esc(S.pageSearch || '')}"
        placeholder="${t('page.searchPlaceholder')}" aria-label="${t('page.searchLabel')}"
        data-input="setPageSearch" autocomplete="off">
      <div class="content-toolbar-actions">
        ${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary btn-sm" data-act="showCreatePage">${icon('add',{size:'md'})} ${t('page.add')}</button>`}
      </div>
    </div>
    <div class="pages-layout">
      <div class="pages-sidebar">
        <div class="pages-sidebar-header">
          <span>${t('nav.pages')}</span>
        </div>
        <div class="pages-list" id="pages-list">
          ${pagesListInner(S.pages)}
        </div>
      </div>
      <div class="pages-content" id="pages-content">
        ${S.selectedPage ? '<div class="loading"><div class="spinner"></div></div>' : ''}
      </div>
    </div>`;

  if(S.selectedPage) await loadPage(S.selectedPage);
}

// filterPagesBySearch narrows the page list by the free-text query in
// S.pageSearch, matching full text (title + authored content) so pages can be
// found by body text, not just their sidebar title. The list payload already
// carries each page's content, so this stays a client-side filter. An empty
// query is a no-op so callers can apply it unconditionally.
function filterPagesBySearch(pages) {
  const needle = (S.pageSearch || '').trim().toLowerCase();
  if (!needle) return pages || [];
  return (pages || []).filter(p =>
    (p.title || '').toLowerCase().includes(needle) ||
    (p.content || '').toLowerCase().includes(needle));
}

// pagesListInner renders the sidebar buttons for the (search-filtered) pages, or
// an inline empty message when the query matches nothing.
function pagesListInner(pages) {
  const filtered = filterPagesBySearch(pages);
  if (!filtered.length) return `<div class="pages-empty-search">${t('page.noPagesMatch')}</div>`;
  return filtered.map(p=>`
    <button type="button" class="pages-item ${S.selectedPage===p.id?'active':''}" ${S.selectedPage===p.id?'aria-current="page"':''} data-act="openPage" data-a0="${esc(p.id)}">
      <span class="pages-status ${p.status==='PUBLISHED'?'status-published':'status-draft'}" aria-hidden="true"></span>
      ${esc(p.title)}
      <span class="sr-only">${p.status === 'PUBLISHED' ? t('page.statusPublished') : t('page.statusDraft')}</span>
    </button>`).join('');
}

// setPageSearch re-filters the cached page list in place (without refetching or
// losing input focus) as the user types in the pages search box.
function setPageSearch(node) {
  S.pageSearch = node.value;
  const list = el('#pages-list');
  if (list) list.innerHTML = pagesListInner(S.pages);
}

async function openPage(id) {
  S.selectedPage = id;
  S.pageEditMode = false;
  await renderPages();
}

async function loadPage(id) {
  const page = await api.pages.get(id);
  S.pageVersions[id] = page.version;
  const container = el('#pages-content');
  if(!container) return;

  if(S.pageEditMode) {
    container.innerHTML = `
      <div class="page-editor">
        <div class="page-editor-header">
          <input class="form-input page-editor-title-input" id="page-title-input" value="${esc(page.title)}" placeholder="${t('page.titlePlaceholder')}">
          <div class="page-editor-actions">
            <button class="btn btn-secondary btn-sm" id="cheatsheet-btn" data-act="toggleCheatsheet" aria-expanded="false" aria-controls="asciidoc-cheatsheet">${t('page.syntaxHelp')}</button>
            <button class="btn btn-secondary btn-sm" id="toggle-preview-btn" data-act="toggleEditorPreview">${t('page.hidePreview')}</button>
            <button class="btn btn-secondary btn-sm" data-act="savePageDraft" data-a0="${esc(id)}">${t('page.saveDraft')}</button>
            <button class="btn btn-secondary btn-sm" data-act="pageViewMode" data-a0="${esc(id)}">${t('page.view')}</button>
            <button class="btn btn-primary btn-sm" data-act="publishPage" data-a0="${esc(id)}">${t('page.publish')}</button>
          </div>
        </div>
        ${asciidocCheatsheetHTML()}
        <div class="page-split-pane" id="page-split">
          <textarea class="page-editor-area" id="page-content-input" data-input="debouncedPreview" data-a0="${esc(id)}">${esc(page.content)}</textarea>
          <div class="page-preview-pane" id="page-preview-pane">
            <div id="page-preview-content">${page.renderedHtml||''}</div>
          </div>
        </div>
      </div>`;
    // Start debounced preview.
    schedulePreview(id);
  } else {
    // Read view with TOC.
    const toc = buildTOC(page.renderedHtml || '');
    container.innerHTML = `
      <div class="page-view">
        <div class="page-view-header">
          <h1>${esc(page.title)}</h1>
          <div class="page-view-actions">
            <span class="badge ${page.status==='PUBLISHED'?'badge-done':'badge-planned'}">${page.status==='PUBLISHED'?t('page.statusBadgePublished'):t('page.statusBadgeDraft')}</span>
            <button class="btn btn-secondary btn-sm" data-act="pageEditMode" data-a0="${esc(id)}">${t('form.edit')}</button>
            <button class="btn btn-danger btn-sm" data-act="deletePage" data-a0="${esc(id)}" data-a1="${esc((page.title))}">${t('form.delete')}</button>
          </div>
        </div>
        <div class="page-body-wrap">
          ${toc.count >= 3 ? `<div class="page-toc"><div class="toc-title">${t('page.contents')}</div>${toc.html}</div>` : ''}
          <div class="page-body">${page.renderedHtml||`<em>${t('page.noContent')}</em>`}</div>
        </div>
      </div>`;
    // Smooth scroll on TOC click.
    container.querySelectorAll('.toc-link').forEach(a => {
      a.addEventListener('click', e => {
        e.preventDefault();
        const target = document.getElementById(a.dataset.id);
        if (target) target.scrollIntoView({behavior:'smooth'});
      });
    });
  }
}

function buildTOC(html) {
  const headings = [];
  // Match h1..h4 with an id anchor (the renderer emits id="h-..." on headings).
  // Strip any inline tags from the heading text so the TOC shows clean labels.
  const re = /<h([1-4])[^>]*\bid="([^"]*)"[^>]*>([\s\S]*?)<\/h\1>/gi;
  let m;
  while ((m = re.exec(html)) !== null) {
    headings.push({ level: parseInt(m[1]), id: m[2], text: m[3].replace(/<[^>]*>/g, '').trim() });
  }
  if (!headings.length) {
    const re2 = /<h([1-4])[^>]*>([\s\S]*?)<\/h\1>/gi;
    while ((m = re2.exec(html)) !== null) {
      headings.push({ level: parseInt(m[1]), id: '', text: m[2].replace(/<[^>]*>/g, '').trim() });
    }
  }
  const tocHtml = headings.map(h => `
    <div class="toc-item toc-level-${h.level}">
      <a href="#" class="toc-link" data-id="${esc(h.id)}">${esc(h.text)}</a>
    </div>`).join('');
  return { html: tocHtml, count: headings.length };
}

let _previewTimer = null;
function schedulePreview(pageId) {
  clearTimeout(_previewTimer);
  _previewTimer = setTimeout(() => runPreview(pageId), 300);
}
function debouncedPreview(pageId) { schedulePreview(pageId); }

async function runPreview(pageId) {
  const input = el('#page-content-input');
  if (!input) return;
  try {
    const result = await api.pages.preview(pageId, input.value);
    const pane = el('#page-preview-content');
    if (pane) pane.innerHTML = result.html || '';
  } catch {}
}

// asciidocCheatsheetHTML renders a lightweight, collapsible quick-reference of
// the supported AsciiDoc syntax. All labels are i18n; the syntax samples are
// literal AsciiDoc (escaped for safe injection).
function asciidocCheatsheetHTML() {
  const rows = [
    ['page.cheatHeadings', '= H1  == H2  === H3'],
    ['page.cheatBold', '*bold*'],
    ['page.cheatItalic', '_italic_'],
    ['page.cheatMono', '`code`'],
    ['page.cheatLink', 'https://site[label]'],
    ['page.cheatList', '* item   . step'],
    ['page.cheatCode', '[source,go]\n----\ncode\n----'],
    ['page.cheatQuote', '____\nquote\n____'],
    ['page.cheatAdmonition', 'NOTE: text'],
    ['page.cheatTable', '|===\n| A | B\n|==='],
    ['page.cheatTask', 'TASK-<uuid>'],
  ];
  return `
    <div id="asciidoc-cheatsheet" class="asciidoc-cheatsheet hidden" role="region" aria-label="${esc(t('page.syntaxHelp'))}">
      <div class="cheatsheet-grid">
        ${rows.map(([k, sample]) => `
          <div class="cheatsheet-row">
            <span class="cheatsheet-label">${esc(t(k))}</span>
            <code class="cheatsheet-sample">${esc(sample)}</code>
          </div>`).join('')}
      </div>
    </div>`;
}

function toggleCheatsheet() {
  const panel = el('#asciidoc-cheatsheet');
  const btn = el('#cheatsheet-btn');
  if (!panel) return;
  const hidden = panel.classList.toggle('hidden');
  if (btn) btn.setAttribute('aria-expanded', String(!hidden));
}

function toggleEditorPreview() {
  const pane = el('#page-preview-pane');
  const btn  = el('#toggle-preview-btn');
  const split = el('#page-split');
  if (!pane) return;
  if (pane.classList.contains('hidden')) {
    pane.classList.remove('hidden');
    if(split) split.classList.remove('page-split-pane--single');
    if(btn) btn.textContent = t('page.hidePreview');
  } else {
    pane.classList.add('hidden');
    if(split) split.classList.add('page-split-pane--single');
    if(btn) btn.textContent = t('page.showPreview');
  }
}

async function publishPage(id) {
  const input = el('#page-content-input');
  const title = el('#page-title-input')?.value?.trim();
  if(!input) return;
  if(!title) { toast(t('validation.titleRequired'), 'error'); return; }
  try {
    const updated = await api.pages.update(id, {title, content: input.value, version: S.pageVersions[id]});
    S.pageVersions[id] = updated.version;
    await api.pages.publish(id, 'Published');
    toast(t('page.published'),'success');
    S.pageEditMode = false;
    await loadPage(id);
  } catch(e) {
    if (e && e.code === 'VERSION_CONFLICT') { showPageConflictDialog(id, title, input.value, true); return; }
    toast(apiErrorMessage(e),'error');
  }
}

async function savePageDraft(id) {
  const input = el('#page-content-input');
  const title = el('#page-title-input')?.value?.trim();
  if (!input) return;
  if (!title) { toast(t('validation.titleRequired'), 'error'); return; }
  try {
    const updated = await api.pages.update(id, { title, content: input.value, version: S.pageVersions[id] });
    S.pageVersions[id] = updated.version;
    const item = S.pages.find(page => page.id === id);
    if (item) item.title = title;
    toast(t('page.draftSaved'), 'success');
    await renderPages();
  } catch(e) {
    if (e && e.code === 'VERSION_CONFLICT') { showPageConflictDialog(id, title, input.value, false); return; }
    toast(apiErrorMessage(e), 'error');
  }
}

// showPageConflictDialog surfaces a 409 from a page save: someone else changed
// the page while this editor was open. The user's text is kept and they choose
// between overwriting the other change (refetch the current version, resave)
// or discarding their edits and reloading the state that won. Cancel keeps the
// editor as-is so the text can still be copied out.
function showPageConflictDialog(id, title, content, alsoPublish) {
  showModal(t('page.conflictTitle'), `
    <p>${t('page.conflictBody')}</p>
    <div class="form-group">
      <button class="btn btn-secondary" data-act="pageConflictReload" data-a0="${esc(id)}">${t('page.conflictReload')}</button>
    </div>`,
    async () => {
      const current = await api.pages.get(id);
      const updated = await api.pages.update(id, { title, content, version: current.version });
      S.pageVersions[id] = updated.version;
      const item = S.pages.find(page => page.id === id);
      if (item) item.title = title;
      if (alsoPublish) {
        await api.pages.publish(id, 'Published');
        toast(t('page.published'), 'success');
        S.pageEditMode = false;
        await loadPage(id);
      } else {
        toast(t('page.draftSaved'), 'success');
        await renderPages();
      }
    }, t('page.conflictOverwrite'));
}

function showCreatePage() {
  showModal(t('page.new'), `
    <div class="form-group"><label class="form-label" for="page-title">${t('form.title')}</label><input class="form-input" id="page-title" placeholder="${t('page.titlePlaceholder')}"></div>`,
    async () => {
      const title = el('#page-title')?.value?.trim();
      if(!title) throw new Error(t('validation.titleRequired'));
      const page = await api.pages.create(S.project.id, {title});
      S.selectedPage = page.id;
      S.pageEditMode = true;
      toast(t('page.created'),'success');
      await renderPages();
    });
}

function deletePage(id,title) {
  confirmDelete(t('page.deleteTitle'), t('form.deleteNamedConfirm',{name:title}), async () => {
    await api.pages.del(id);
    S.selectedPage = null;
    toast(t('page.deleted'),'success');
    await renderPages();
  });
}

// ═══════════════════════════════════════════════════════════
// REPOS VIEW
// ═══════════════════════════════════════════════════════════
async function renderRepos() {
  S.repos = await api.repos.list(S.project.id).catch(()=>[]);
  const providers = ['GITHUB','GITLAB','BITBUCKET','FAKE_GITLAB'];
  const c = el('#content');
  c.innerHTML = `
    <div class="repos-wrap grid-2col">
      <div class="box">
        <div class="box-title">${t('repo.connectionsTitle')}</div>
        ${!S.repos.length ? `<div class="text-muted">${t('repo.empty')}</div>` :
          S.repos.map(r=>`
            <div class="repo-item">
              <div class="repo-info">
                <div class="repo-name">${esc(r.displayName)}
                  ${r.authKind === 'OAUTH' ? `<span class="badge badge-done">${t('repo.oauthConnected')}</span>` : ''}
                </div>
                <div class="text-muted text-sm">${esc(r.repositoryUrl)} · ${t('repo.defaultLabel',{branch:esc(r.defaultBranch)})}</div>
              </div>
              <div class="repo-actions">
                ${r.oauthAvailable ? `<button class="btn btn-secondary btn-sm" data-act="connectRepoOAuth" data-a0="${esc(r.id)}">${t('repo.connectOAuth')}</button>` : ''}
                ${r.authKind === 'OAUTH' ? `<button class="btn-text" data-act="refreshRepoToken" data-a0="${esc(r.id)}">${t('repo.refreshToken')}</button>` : ''}
                <button class="btn-icon" title="${t('form.delete')}" data-act="deleteRepo" data-a0="${esc(r.id)}" data-a1="${esc((r.displayName))}">${icon('delete')}</button>
              </div>
            </div>`).join('')}
      </div>
      <div class="box">
        <div class="box-title">${t('repo.addTitle')}</div>
        <div class="grid-gap">
          <input class="form-input" id="repo-name" placeholder="${t('repo.displayNamePlaceholder')}">
          <input class="form-input" id="repo-url" placeholder="${t('repo.urlPlaceholder')}">
          <div class="repo-provider-row">
            <select class="form-select flex-1" id="repo-provider">
              ${providers.map(p=>`<option value="${p}">${p}</option>`).join('')}
            </select>
            <input class="form-input flex-2" id="repo-branch" placeholder="${t('repo.branchPlaceholder')}">
          </div>
          <input class="form-input" id="repo-token" type="password" placeholder="${t('repo.tokenPlaceholder')}">
          <div class="text-muted text-sm">${t('repo.tokenHint')}</div>
          <button class="btn btn-primary btn-sm btn-add-repo" data-act="addRepo">${t('repo.addButton')}</button>
        </div>
      </div>
    </div>`;
}

async function addRepo() {
  const displayName = el('#repo-name')?.value?.trim();
  const repositoryUrl = el('#repo-url')?.value?.trim();
  const provider = el('#repo-provider')?.value || 'FAKE_GITLAB';
  const defaultBranch = el('#repo-branch')?.value?.trim() || 'main';
  const accessToken = el('#repo-token')?.value?.trim();
  if(!displayName||!repositoryUrl) { toast(t('validation.repoRequired'),'error'); return; }
  const body = {displayName,repositoryUrl,provider,defaultBranch};
  if (accessToken) body.accessToken = accessToken;
  try {
    await api.repos.create(S.project.id, body);
    toast(t('repo.added'),'success');
    await renderRepos();
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
function deleteRepo(id,name) {
  confirmDelete(t('repo.deleteTitle'), t('repo.removeConfirm',{name}), async()=>{
    await api.repos.del(id); toast(t('form.removed'),'success'); await renderRepos();
  });
}
// Begins the OAuth flow: fetch the provider consent URL (carrying our bearer
// token in the header), then navigate the browser to it.
async function connectRepoOAuth(id) {
  try {
    const res = await api.repos.oauthAuthorize(id);
    if (res && res.authorizeUrl) { window.location.href = res.authorizeUrl; }
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}
async function refreshRepoToken(id) {
  try {
    await api.repos.oauthRefresh(id);
    toast(t('repo.tokenRefreshed'),'success');
    await renderRepos();
  } catch(e) { toast(apiErrorMessage(e),'error'); }
}

// ═══════════════════════════════════════════════════════════
// ACTIVITY VIEW
// ═══════════════════════════════════════════════════════════
async function renderActivity() {
  const entries = await api.activity.project(S.project.id);
  // Every page: these resolve task ids to titles for the activity entries, and
  // an entry whose task fell outside the page renders without its title.
  const tasks = await api.tasks.listAll(S.project.id).catch(() => []);
  const taskMeta = taskMetaById(tasks);
  const c = el('#content');
  if(!entries.length) {
    c.innerHTML=`<div class="empty"><div class="empty-icon">${icon('time',{size:'hero'})}</div><div class="empty-title">${t('activity.emptyTitle')}</div></div>`;
    return;
  }
  const icons = {
    TASK_CREATED:'add',TASK_UPDATED:'edit',TASK_STATUS_CHANGED:'refresh',TASK_MOVED:'sort',
    TASK_COMMENT_ADDED:'comment',RELEASE_CLOSED:'milestone',MILESTONE_CLOSED:'milestone',PAGE_PUBLISHED:'page',BRANCH_CREATED:'branch',BRANCH_LINKED:'branch',
  };
  c.innerHTML=`
    <div class="activity-wrap">
      <div class="activity-box">
        ${entries.slice().reverse().map(e=>{
          const tag = e.taskId ? 'button' : 'div';
          const attrs = e.taskId ? `type="button" data-act="openProjectTask" data-a0="${esc(e.taskId)}" data-a1="${esc(S.project.id)}"` : '';
          return `
        <${tag} class="activity-item ${e.taskId ? 'activity-item-clickable' : ''}" ${attrs}>
            <div class="activity-icon" aria-hidden="true">${icon(icons[e.type]||'time',{size:'md'})}</div>
            <div class="activity-body">
            ${e.taskId && taskMeta.get(e.taskId) ? `<div class="activity-task-label">${taskMeta.get(e.taskId).seq ? `<span class="task-seq">${esc(taskMeta.get(e.taskId).seq)}</span>` : ''}${esc(taskMeta.get(e.taskId).title)}</div>` : ''}
            <div class="activity-msg">${esc(activityMessage(e))}</div>
            <div class="activity-time">${fmtDateTime(e.createdAt)}</div>
          </div>
          </${tag}>`;}).join('')}
      </div>
    </div>`;
}

// ═══════════════════════════════════════════════════════════
// ARCHIVE
// ═══════════════════════════════════════════════════════════
// renderArchive lists the project's ARCHIVED tasks — those manually archived and
// those the server auto-archives a month after they were marked DONE (so the
// board stays focused on live work). Rows open the task panel (full history,
// comments) and carry an inline Reopen that returns the task to PLANNED.
async function renderArchive() {
  // Every page: the archive is precisely where a project's oldest rows live,
  // so one 200-row page is the worst possible window onto it.
  const tasks = await api.tasks.listAll(S.project.id, { status: 'ARCHIVED' });
  const c = el('#content');
  if (!tasks.length) {
    c.innerHTML = `<div class="empty"><div class="empty-icon">${icon('archive',{size:'hero'})}</div><div class="empty-title">${t('archive.emptyTitle')}</div><p class="empty-body">${t('archive.emptyBody')}</p></div>`;
    return;
  }
  c.innerHTML = `
    <div class="backlog-wrap">
      <p class="archive-intro text-muted text-sm">${t('archive.intro')}</p>
      <div class="backlog-list" id="archive-list">
        ${tasks.map(task => archiveRow(task)).join('')}
      </div>
    </div>`;
}

function archiveRow(task) {
  const seq = task.seqNumber != null ? `${esc(S.project?.abbreviation||S.project?.slug?.toUpperCase()||'')}-${task.seqNumber}` : '';
  return `
    <div class="backlog-row archive-row">
      <span class="backlog-cell">${typeBadge(task.taskType)}</span>
      <span class="backlog-cell task-seq text-muted">${seq}</span>
      <button type="button" class="backlog-title archive-title" data-act="openTaskPanel" data-a0="${esc(task.id)}" aria-label="${t('task.openTask',{title:esc(taskLabel(task))})}">${esc(task.title)}</button>
      <span class="backlog-cell">${task.assigneeId ? esc(memberName(task.assigneeId)) : ''}</span>
      <button type="button" class="btn btn-secondary btn-sm" data-act="reopenFromArchive" data-a0="${esc(task.id)}">${t('task.reopen')}</button>
    </div>`;
}

// reopenFromArchive reopens an archived task (back to PLANNED) and refreshes the
// archive list in place, rather than opening the task panel the way the panel's
// own reopen does — the user is browsing the archive, not a single task.
async function reopenFromArchive(taskId) {
  try {
    await api.tasks.reopen(taskId);
    toast(t('archive.reopened'), 'success');
    await renderArchive();
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}

// ═══════════════════════════════════════════════════════════
// TASK CREATION MODAL
// ═══════════════════════════════════════════════════════════
// If currently viewing a board (the default board *or* a sprint board), place
// the newly created task on a lane of that board so it appears immediately
// rather than going to the backlog. S.board is the board in view, so the task
// lands on whichever board the user is looking at. columnId targets a specific
// lane (e.g. a lane's "+ Add task" button); without it — or if the id no longer
// matches a lane — the task lands in the first lane.
async function maybePlaceOnBoard(taskId, columnId) {
  if ((S.view !== 'board' && S.view !== 'sprintBoard') || !S.board) return;
  const cols = S.board.columns || [];
  const target = cols.find(c => c.id === columnId) || cols[0];
  if (!target) return;
  // Append to the end of the lane: rank just past the current highest so the
  // new card sorts below the existing ones (pinned cards still float to top).
  const maxRank = (S.boardTasks || [])
    .filter(t => t.boardColumnId === target.id)
    .reduce((m, t) => Math.max(m, t.boardRank || 0), 0);
  try {
    await api.boards.move(S.board.id, { taskId, boardColumnId: target.id, boardRank: maxRank + BOARD_RANK_STEP });
    // Appending puts the card at the end of the lane, which on a lane longer
    // than the project's card cap is a position the board does not draw. Say it
    // must be drawn anyway — a create that shows nothing reads as a failure.
    revealBoardCard(taskId);
  } catch { /* non-fatal — task still exists in backlog */ }
}

// ── view registration (see registry.js for the contract) ──
Views.register('pages', {
  render: renderPages,
  sidebar: { icon: 'page', label: () => t('nav.pages'), key: 'P', order: 70 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreatePage">${icon('add',{size:'md'})} ${t('page.new')}</button>`,
});
Views.register('repos', {
  render: renderRepos,
  sidebar: { icon: 'branch', label: () => t('nav.repositories'), order: 80 },
});
Views.register('activity', {
  render: renderActivity,
  sidebar: { icon: 'time', label: () => t('nav.activity'), order: 90 },
});
Views.register('archive', {
  render: renderArchive,
  sidebar: { icon: 'archive', label: () => t('nav.archive'), order: 100 },
});

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([addRepo, showCreatePage, toggleCheatsheet, toggleEditorPreview], _A0);
registerActions([
  openPage, publishPage, reopenFromArchive, savePageDraft, connectRepoOAuth, refreshRepoToken,
], _A1);
registerActions([deletePage, deleteRepo], _A2);
registerActions({
  pageViewMode:  el => { S.pageEditMode = false; loadPage(el.dataset.a0); },
  pageEditMode:  el => { S.pageEditMode = true;  loadPage(el.dataset.a0); },
  // Conflict dialog's "discard my edits": reload the winning server state
  // into the still-open editor (loadPage refreshes S.pageVersions too).
  pageConflictReload: el => { hideModal(); loadPage(el.dataset.a0); },
});
registerInputs({
  debouncedPreview: el => debouncedPreview(el.dataset.a0),
  setPageSearch:    el => setPageSearch(el),
});

export { maybePlaceOnBoard, renderPages, showCreatePage };
