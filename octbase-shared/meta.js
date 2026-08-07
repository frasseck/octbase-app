// Octbase shared — task enum metadata (status / priority / type).
//
// Part of the @octbase/shared package (37b stage 3): one module imported by
// both SPAs, so there is no copy to drift any more. `'use strict'` is gone
// because an ES module is always strict.
//
// The label getters call `t()` lazily so the maps pick up the active locale —
// which is why this module can import i18n at load time without reading
// anything from it before the app has chosen a language.
import { t } from './i18n.js';
const STATUS_META = {
  PLANNED:     { cls:'badge-planned',     get label(){ return t('task.status.PLANNED'); } },
  IN_PROGRESS: { cls:'badge-in-progress', get label(){ return t('task.status.IN_PROGRESS'); } },
  IN_REVIEW:   { cls:'badge-in-review',   get label(){ return t('task.status.IN_REVIEW'); } },
  DONE:        { cls:'badge-done',        get label(){ return t('task.status.DONE'); } },
  ARCHIVED:    { cls:'badge-archived',    get label(){ return t('task.status.ARCHIVED'); } },
};
const PRIORITY_META = {
  LOW:      { cls:'prio-low',      get label(){ return t('task.priority.LOW'); } },
  MEDIUM:   { cls:'prio-medium',   get label(){ return t('task.priority.MEDIUM'); } },
  HIGH:     { cls:'prio-high',     get label(){ return t('task.priority.HIGH'); } },
  CRITICAL: { cls:'prio-critical', get label(){ return t('task.priority.CRITICAL'); } },
  BLOCKER:  { cls:'prio-blocker',  get label(){ return t('task.priority.BLOCKER'); } },
};
// priorityMeta resolves badge metadata for built-in AND admin-defined custom
// priorities (which have no entry in PRIORITY_META): customs get a neutral
// badge with their name as the label.
function priorityMeta(p) {
  return PRIORITY_META[p] || { cls: 'prio-custom', label: p };
}
// priorityNames returns the selectable priority values for a project: the
// built-in set followed by the project's custom priorities (as returned by
// GET /projects/{id}/task-priorities — objects with a .name).
function priorityNames(custom) {
  return PRIORITIES.concat((custom || []).map(c => c.name));
}
// Task types form a strict hierarchy (THEME → INITIATIVE → EPIC → STORY →
// TASK → SUBTASK); THEME and INITIATIVE are opt-in per project (the project's
// themeEnabled/initiativeEnabled settings) — the per-project rules live in
// typeChain/typeParentRule below. TASK stays first so it remains the default
// option in create forms.
const TYPE_META = {
  TASK:       { sym:'T',  cls:'type-task',       get label(){ return t('task.type.TASK'); } },
  STORY:      { sym:'S',  cls:'type-story',      get label(){ return t('task.type.STORY'); } },
  EPIC:       { sym:'E',  cls:'type-epic',       get label(){ return t('task.type.EPIC'); } },
  SUBTASK:    { sym:'ST', cls:'type-subtask',    get label(){ return t('task.type.SUBTASK'); } },
  INITIATIVE: { sym:'I',  cls:'type-initiative', get label(){ return t('task.type.INITIATIVE'); } },
  THEME:      { sym:'TH', cls:'type-theme',      get label(){ return t('task.type.THEME'); } },
};
// typeChain returns the project's active hierarchy from the top down. project
// may be null/undefined (core chain only). Mirrors the backend's
// Project.TaskTypeChain.
function typeChain(project) {
  const chain = [];
  if (project && project.themeEnabled) chain.push('THEME');
  if (project && project.initiativeEnabled) chain.push('INITIATIVE');
  return chain.concat(['EPIC', 'STORY', 'TASK', 'SUBTASK']);
}
// typeParentRule mirrors the backend's ParentTaskTypeFor: parentType is the
// type directly above taskType in the project's chain (null at the top or for
// a type the project has not enabled); only a SUBTASK's parent is mandatory.
// parentType is the NEAREST allowed parent, not the only one — every level
// further up is allowed too (typeParentAllowed), so use it for defaults and
// for the allowed/required questions, not to validate a concrete pair.
function typeParentRule(project, taskType) {
  const chain = typeChain(project);
  const i = chain.indexOf(taskType);
  if (i <= 0) return { parentType: null, required: false };
  return { parentType: chain[i - 1], required: taskType === 'SUBTASK' };
}
// typeParentAllowed mirrors the backend's TaskParentTypeAllowed: a parent may
// be any level ABOVE the child in the project's chain, not just the one
// directly above, so a task may sit straight under an epic. SUBTASK is the
// exception — its parent stays exactly a TASK.
function typeParentAllowed(project, taskType, parentType) {
  const chain = typeChain(project);
  const ci = chain.indexOf(taskType);
  const pi = chain.indexOf(parentType);
  if (ci <= 0 || pi < 0) return false;
  if (taskType === 'SUBTASK') return pi === ci - 1;
  return pi < ci;
}
// typeChildOf returns the child type directly below taskType in the project's
// chain ('' when the type cannot have children there). Like typeParentRule
// this is the nearest level; any lower type may hang under it.
function typeChildOf(project, taskType) {
  const chain = typeChain(project);
  const i = chain.indexOf(taskType);
  return i >= 0 && i + 1 < chain.length ? chain[i + 1] : '';
}
// projectTaskTypes returns the type options for create/edit selects: the core
// set (TASK first, as the default), plus INITIATIVE/THEME when the project
// has enabled them.
function projectTaskTypes(project) {
  const types = ['TASK', 'STORY', 'EPIC', 'SUBTASK'];
  if (project && project.initiativeEnabled) types.push('INITIATIVE');
  if (project && project.themeEnabled) types.push('THEME');
  return types;
}
// ── Effort estimation (opt-in per project) ──────────────────────────────────
// A project estimates in exactly one unit, or not at all: estimationUnit is
// NONE (the default), POINTS or HOURS. Mirrors the backend's
// Project.EstimationUnit. When it is NONE nothing about estimates is shown —
// that invisibility is the feature, not an oversight.
const ESTIMATION_UNITS = ['NONE', 'POINTS', 'HOURS'];
// Fibonacci presets offered as chips in the points editor. UI sugar only: the
// server accepts any integer 0–100, so a team on a different scale is never
// blocked by these.
const FIBONACCI_POINTS = [1, 2, 3, 5, 8, 13, 21];

// estimationUnit reads a project's unit defensively — an older API response
// (or none) reads as NONE rather than throwing.
function estimationUnit(project) {
  const u = project && project.estimationUnit;
  return u === 'POINTS' || u === 'HOURS' ? u : 'NONE';
}
// DEFAULT_BOARD_LANE_LIMIT mirrors the API's DefaultBoardLaneLimit. It is the
// fallback, not the source of truth: the server sends the project's value on
// every project read, and this only covers a response that predates the field.
const DEFAULT_BOARD_LANE_LIMIT = 20;

// boardLaneLimit reads a project's board lane cap defensively, the same way
// estimationUnit does. 0 is a real value ("draw every card") and must survive,
// so the guard tests for an integer rather than for truthiness — `n || DEFAULT`
// would quietly turn "show all" back into 20.
function boardLaneLimit(project) {
  const n = project && project.boardLaneLimit;
  return Number.isInteger(n) && n >= 0 ? n : DEFAULT_BOARD_LANE_LIMIT;
}
// estimationEnabled answers "does this project estimate at all".
function estimationEnabled(project) {
  return estimationUnit(project) !== 'NONE';
}
// estimationField maps the project's unit to the task field it is stored in
// ('' when estimation is off). The two units have two fields on purpose:
// switching is non-destructive, so the inactive one keeps its value.
function estimationField(project) {
  const u = estimationUnit(project);
  return u === 'POINTS' ? 'storyPoints' : u === 'HOURS' ? 'estimateHours' : '';
}
// taskEstimate returns the task's estimate in the project's ACTIVE unit, or
// null when the task is unestimated or the project does not estimate. null is
// a real state meaning *unestimated* — never render it as 0.
function taskEstimate(project, task) {
  const field = estimationField(project);
  if (!field || !task) return null;
  const v = task[field];
  return v === null || v === undefined ? null : v;
}
// estimateText formats an estimate for a badge or chip: bare for points ("8"),
// suffixed for hours ("7.5 h" / "7.5 Std."). Returns '' when there is nothing
// to show, so callers can drop the whole element. The suffix goes through
// t() like every other label here, so badges and the editor agree per locale.
function estimateText(project, task) {
  const v = taskEstimate(project, task);
  if (v === null) return '';
  return estimationUnit(project) === 'HOURS' ? v + ' ' + t('task.estimateHoursSuffix') : String(v);
}
// estimateLabel is the project's unit label — the string every estimate
// control heads with ("Story points" / "Estimate (hours)").
function estimateLabel(project) {
  return estimationUnit(project) === 'HOURS' ? t('task.estimateHours') : t('task.storyPoints');
}
// estimateLimits mirrors the backend's validation bounds (0–100 whole points,
// 0–1000 hours with at most 2 decimals) as number-input attributes.
function estimateLimits(unit) {
  return unit === 'HOURS' ? { max: '1000', step: '0.25' } : { max: '100', step: '1' };
}
// parseEstimateInput turns a text-box value into the API's number-or-null:
// an empty box means *unestimated* (null), a typed "0" is a deliberate
// estimate of no effort, and the two must never collapse into each other the
// way `value || null` would fold them. A non-numeric value returns undefined
// so the caller can toast and keep the old value.
function parseEstimateInput(raw) {
  const text = String(raw == null ? '' : raw).trim();
  if (text === '') return null;
  const value = Number(text);
  return Number.isFinite(value) ? value : undefined;
}
// estimableType mirrors the backend's EstimableTaskType: only the leaf types
// carry an estimate; EPIC/INITIATIVE/THEME are containers.
function estimableType(taskType) {
  return taskType === 'STORY' || taskType === 'TASK' || taskType === 'SUBTASK';
}
// taskEstimatable answers the single question every estimate UI asks: should
// this task show an estimate control at all?
function taskEstimatable(project, task) {
  return estimationEnabled(project) && estimableType(task && task.taskType);
}

// openDescendantsOf walks the whole subtree under taskIds and returns the tasks
// that are still open, ids themselves excluded. It walks a project task list
// rather than asking the server: both SPAs already hold that list (the desktop
// panel's parent picker, mobile's api.tasks.listAll), so a confirmation costs no
// round trip on the path between a drag and the card landing.
//
// Two things the walk must get right, both learned from the backend guard this
// mirrors: it recurses THROUGH finished tasks (a DONE story can carry an open
// child, which is exactly the shape that hid the original bug), and it carries a
// visited set — nothing validates that parent_id is acyclic, and a cycle would
// otherwise spin here as readily as in SQL.
//
// It lives here rather than in either SPA because both completion warnings run
// the same walk: desktop's three doors (panel status control, Done-lane drop,
// bulk set-status) and mobile's two (the status sheet, the move-to-column
// sheet). A second copy is exactly the drift this package exists to prevent.
function openDescendantsOf(taskIds, tasks) {
  const ids = new Set(taskIds);
  const byParent = new Map();
  for (const task of tasks || []) {
    if (!task.parentId) continue;
    if (!byParent.has(task.parentId)) byParent.set(task.parentId, []);
    byParent.get(task.parentId).push(task);
  }
  const open = [];
  const seen = new Set(ids);
  const queue = [...ids];
  while (queue.length) {
    for (const child of byParent.get(queue.shift()) || []) {
      if (seen.has(child.id)) continue;
      seen.add(child.id);
      queue.push(child.id);
      // Selected siblings are being completed by this same action, so they are
      // not work that "keeps running" — only tasks outside the set count.
      if (!ids.has(child.id) && child.status !== 'DONE' && child.status !== 'ARCHIVED') open.push(child);
    }
  }
  return open;
}

const STATUSES   = Object.keys(STATUS_META);
const PRIORITIES = Object.keys(PRIORITY_META);
const TASK_TYPES = Object.keys(TYPE_META);

export { DEFAULT_BOARD_LANE_LIMIT, ESTIMATION_UNITS, FIBONACCI_POINTS, PRIORITIES, PRIORITY_META, STATUSES, STATUS_META, TASK_TYPES, TYPE_META, boardLaneLimit, estimableType, estimateLabel, estimateLimits, estimateText, estimationEnabled, estimationField, estimationUnit, openDescendantsOf, parseEstimateInput, priorityMeta, priorityNames, projectTaskTypes, taskEstimatable, taskEstimate, typeChain, typeChildOf, typeParentAllowed, typeParentRule };
