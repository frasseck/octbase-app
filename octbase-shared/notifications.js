// Notification rendering, shared by both SPAs' inboxes (OCT-323).
//
// A notification used to arrive as an English sentence composed by the server
// and stored in the database, which both SPAs printed verbatim — so a German
// reader read English in the desktop bell and in the mobile inbox, and fixing a
// wording meant rewriting stored rows. It now arrives the way an activity entry
// does: a `kind` naming the event plus a `params` object, rendered here through
// `notifications.messages.<kind>`.
//
// The server still composes and stores the English sentence, because the email
// channel has no browser and no locale to read; localizing that needs the
// recipient's stored language preference and a catalogue on the Go side, which
// is separate work. Here it is the fallback, and the two cases below are the
// whole reason this function is not a one-liner.

import { t } from './i18n.js';
import { STATUS_META } from './meta.js';

// The kinds that reach an inbox. `task_changed` is deliberately absent: it is
// email-only and writes no in-app row, so it has no message to translate. A
// kind not listed here falls back to the stored sentence rather than rendering
// a raw key — an unknown kind means this client is older than the server, and
// English text beats "notifications.messages.whatever".
const RENDERABLE_KINDS = new Set(['task_assigned', 'reviewer_set', 'mentioned', 'status_changed']);

// notificationParams prepares a kind's params for interpolation. status arrives
// as the raw enum, because the vocabulary is the client's to own — the same
// treatment the activity feed gives TASK_STATUS_CHANGED. A custom board-lane
// status has no STATUS_META entry and is a name a human typed, so it passes
// through as written.
//
// The lookup goes through hasOwnProperty because a board lane's status is a
// free string: a lane named `constructor` or `toString` hits STATUS_META's
// prototype, and `STATUS_META.constructor` is truthy with an undefined `label`,
// so a plain `if (meta)` would render the notification as "changed to
// undefined" instead of naming the lane.
function notificationParams(kind, params) {
  if (kind !== 'status_changed') return params;
  if (!Object.prototype.hasOwnProperty.call(STATUS_META, params.status)) return params;
  return { ...params, status: STATUS_META[params.status].label };
}

// notificationMessage returns the text to show for one notification.
//
// Falls back to the server's English `message` when the notification predates
// the params contract (`params` is null — those rows have no parameters to
// recover and the stored sentence is the only text they will ever have) or when
// the kind is one this client does not know. A kind that simply takes no
// parameters, such as `mentioned`, sends an empty object and renders normally.
//
// The result is plain text and is NOT escaped here; callers insert it through
// their own esc().
function notificationMessage(n) {
  if (!n) return '';
  if (!n.params || !RENDERABLE_KINDS.has(n.kind)) return n.message || '';
  return t(`notifications.messages.${n.kind}`, notificationParams(n.kind, n.params));
}

export { RENDERABLE_KINDS, notificationMessage };
