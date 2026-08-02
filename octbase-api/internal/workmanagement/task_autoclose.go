package workmanagement

import "github.com/octbase/octbase-api/internal/shared"

// AutoCompleteTask completes a task on behalf of an automation (today: the SCM
// merge webhook when auto_close_on_merge is set) through the same rules as the
// interactive status door, so a non-interactive completion cannot do what a
// user's cannot:
//
//   - a DONE/ARCHIVED task is left alone (immutability; reopening is a
//     deliberate per-task ceremony, never a webhook side effect),
//   - a task with an open BLOCKER descendant is not completed (completionGuard's
//     rule — the webhook cannot answer 422 to the SCM provider, so the close is
//     skipped and the caller reports it),
//   - the task's card moves to the lane matching DONE, exactly like a status
//     change from the task panel (OCT-90/OCT-303),
//   - a TASK_STATUS_CHANGED activity entry is written with an empty actor (the
//     system-action convention, see TASK_AUTO_ARCHIVED), so the Activity view
//     and the sprint burndown replay see webhook completions too.
//
// Returns (false, nil) when the close was skipped by a rule rather than failed;
// the returned reason distinguishes the skip cases for the caller's log line.
// done_at stamping and the version bump ride on the same TaskRepo.Update as
// every interactive edit. Deliberately not carried over from the interactive
// door: user notifications — the old repo-level path sent none either, and
// whether a merge should notify like a person's edit is a product question,
// not part of this parity fix.
func (h *Handler) AutoCompleteTask(projectID, taskID string) (completed bool, reason string, err error) {
	t, err := h.tasks.FindByID(taskID)
	if err != nil {
		return false, "", err
	}
	if t == nil || t.ProjectID != projectID {
		return false, "task not found in project", nil
	}
	if IsImmutable(t.Status) {
		return false, "task is " + t.Status, nil
	}
	blocked, err := h.tasks.AnyOpenDescendantPriorityExists([]string{t.ID}, PriorityBlocker)
	if err != nil {
		return false, "", err
	}
	if blocked {
		return false, "an open descendant has BLOCKER priority", nil
	}
	oldStatus := t.Status
	t.Status = StatusDone
	t.UpdatedAt = shared.Now()
	if err := h.alignBoardColumnToStatus(t); err != nil {
		return false, "", err
	}
	if err := h.tasks.Update(t); err != nil {
		return false, "", err
	}
	_ = h.writeActivity(t.ProjectID, t.ID, "", "TASK_STATUS_CHANGED",
		map[string]any{"status": StatusDone, "from": oldStatus})
	return true, "", nil
}
