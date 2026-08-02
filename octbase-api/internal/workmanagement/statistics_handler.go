package workmanagement

import (
	"database/sql"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Project statistics: the numbers a project manager asks for before a standup
// or a steering meeting — how the work is distributed, what is late, how fast
// it is finishing, and who is carrying it.
//
// Like the burndown and velocity reports this is a projection computed on read;
// no snapshot table, no nightly job. It is a handful of grouped aggregates over
// tasks/releases, each narrowed by project_id (indexed), plus one bounded scan
// of the tasks finished inside the throughput window. Nothing here scans a
// project's whole task history row by row.

const (
	// statsThroughputWeeks is how many trailing whole weeks the throughput
	// series covers, including the current (partial) one.
	statsThroughputWeeks = 8
	// statsCycleTimeDays bounds the sample the cycle-time average/median is
	// taken from: recent enough to describe how the team works *now*.
	statsCycleTimeDays = 90
	// statsDueSoonDays is the look-ahead for the "due soon" tile.
	statsDueSoonDays = 7
	// statsWorkloadMax caps how many assignees the workload breakdown returns
	// (busiest first). The tail is folded into unassigned-style noise rather
	// than making the page unbounded.
	statsWorkloadMax = 12
)

// The two reads below vary by estimation unit only in which estimate column
// they project. A column name cannot be a bind parameter, so rather than
// splicing one into a shared query (which puts a Go value in the statement text
// — gosec G202, and a habit worth not forming) each unit gets a *complete*
// query literal, whitelisted by unit. Every map here is total over the three
// units, so an unknown unit is impossible rather than merely unlikely: NONE
// projects a literal NULL, which is what makes `effort` null all the way out to
// the API.
//
// The `estimateExprFor`-style helper these replaced is deliberately gone: one
// idiom for this concern, not two.
var finishedTaskQueries = map[string]string{
	EstimationUnitNone: `SELECT created_at, done_at, NULL::numeric
		   FROM tasks
		  WHERE project_id=$1 AND done_at IS NOT NULL AND done_at >= $2`,
	EstimationUnitPoints: `SELECT created_at, done_at, story_points
		   FROM tasks
		  WHERE project_id=$1 AND done_at IS NOT NULL AND done_at >= $2`,
	EstimationUnitHours: `SELECT created_at, done_at, estimate_hours
		   FROM tasks
		  WHERE project_id=$1 AND done_at IS NOT NULL AND done_at >= $2`,
}

const openWorkloadFrom = `
		   FROM tasks t LEFT JOIN users u ON u.id = t.assignee_id
		  WHERE t.project_id=$1 AND t.status NOT IN ('DONE','ARCHIVED') AND t.assignee_id IS NOT NULL
		  GROUP BY t.assignee_id, u.display_name
		  ORDER BY COUNT(*) DESC, u.display_name ASC
		  LIMIT $2`

var openWorkloadQueries = map[string]string{
	EstimationUnitNone:   `SELECT t.assignee_id, COALESCE(u.display_name,''), COUNT(*), NULL::numeric` + openWorkloadFrom,
	EstimationUnitPoints: `SELECT t.assignee_id, COALESCE(u.display_name,''), COUNT(*), COALESCE(SUM(t.story_points),0)` + openWorkloadFrom,
	EstimationUnitHours:  `SELECT t.assignee_id, COALESCE(u.display_name,''), COUNT(*), COALESCE(SUM(t.estimate_hours),0)` + openWorkloadFrom,
}

// countEntry is one bucket of a distribution (status, type, priority). A slice
// of these rather than a map because order is presentation: the API delivers
// the buckets in the domain's own enum order, so every client draws the same
// chart without re-deriving it.
type countEntry struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// taskStatistics is the headline block: totals and the three distributions.
type taskStatistics struct {
	Total      int `json:"total"`
	Open       int `json:"open"`
	InProgress int `json:"inProgress"`
	Done       int `json:"done"`
	Archived   int `json:"archived"`
	// Unassigned, Overdue and DueSoon count *open* tasks only — a finished
	// task that was late is history, not a call to action.
	Unassigned int `json:"unassigned"`
	Overdue    int `json:"overdue"`
	DueSoon    int `json:"dueSoon"`
	// CreatedLast30/CompletedLast30 are the crude in/out flow: a project
	// creating more than it finishes is growing its backlog.
	CreatedLast30   int          `json:"createdLast30"`
	CompletedLast30 int          `json:"completedLast30"`
	ByStatus        []countEntry `json:"byStatus"`
	ByType          []countEntry `json:"byType"`
	// ByPriority covers open tasks only: the point of the chart is what is
	// still queued, not how many CRITICALs the project has ever closed.
	ByPriority []countEntry `json:"byPriority"`
}

// effortStatistics is the estimation block — null for a project that does not
// estimate, so a client cannot accidentally render zeros as if they meant
// "nothing to do".
type effortStatistics struct {
	Unit      string  `json:"unit"`
	Total     float64 `json:"total"`
	Done      float64 `json:"done"`
	Remaining float64 `json:"remaining"`
	// Unestimated is the number of open, estimable tasks carrying no estimate
	// — the size of the blind spot in every number above it.
	Unestimated int `json:"unestimated"`
}

// throughputWeek is one bar of the delivery series: how much finished in that
// ISO week. Effort is null unless the project estimates.
type throughputWeek struct {
	WeekStart string   `json:"weekStart"`
	Completed int      `json:"completed"`
	Effort    *float64 `json:"effort"`
}

// cycleTimeStatistics describes how long a finished task took from creation to
// DONE. Median as well as average because a single six-month straggler drags
// the average somewhere no real task lives.
type cycleTimeStatistics struct {
	SampleSize  int      `json:"sampleSize"`
	AverageDays *float64 `json:"averageDays"`
	MedianDays  *float64 `json:"medianDays"`
}

// assigneeWorkload is one person's share of the open work.
type assigneeWorkload struct {
	UserID string   `json:"userId"`
	Name   string   `json:"name"`
	Open   int      `json:"open"`
	Effort *float64 `json:"effort"`
}

// sprintStatistics is the active sprint at a glance (null when none is running).
type sprintStatistics struct {
	SprintID  string  `json:"sprintId"`
	Name      string  `json:"name"`
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`
	Committed int     `json:"committed"`
	Completed int     `json:"completed"`
	// DaysRemaining counts the end date inclusively: a sprint ending today has
	// 1 day left, because today is still a working day. It is null when the
	// sprint has no end date, and never goes below zero (an overrunning sprint
	// reads "0 days left", not "-3").
	DaysRemaining     *int     `json:"daysRemaining"`
	CommittedEstimate *float64 `json:"committedEstimate"`
	CompletedEstimate *float64 `json:"completedEstimate"`
}

// releaseStatistics summarises the release plan.
type releaseStatistics struct {
	Open   int `json:"open"`
	Closed int `json:"closed"`
	// NextDue is the soonest due date among open releases, or null.
	NextDue     *string `json:"nextDue"`
	NextDueName *string `json:"nextDueName"`
	// OverdueOpen counts open releases whose due date has passed.
	OverdueOpen int `json:"overdueOpen"`
}

type projectStatistics struct {
	ProjectID      string `json:"projectId"`
	GeneratedAt    string `json:"generatedAt"`
	EstimationUnit string `json:"estimationUnit"`

	Tasks      taskStatistics      `json:"tasks"`
	Effort     *effortStatistics   `json:"effort"`
	Throughput []throughputWeek    `json:"throughput"`
	CycleTime  cycleTimeStatistics `json:"cycleTime"`
	Workload   []assigneeWorkload  `json:"workload"`
	Sprint     *sprintStatistics   `json:"sprint"`
	Releases   releaseStatistics   `json:"releases"`
}

// ProjectStatistics handles GET /api/v1/projects/{projectId}/reports/statistics.
// Read access is plain project membership, like the other two reports: the
// numbers are an aggregate of what a member can already see task by task.
func (h *Handler) ProjectStatistics(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	project, err := h.projects.FindByID(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if project == nil {
		shared.WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")
		return
	}

	now := time.Now().UTC()
	stats := projectStatistics{
		ProjectID:      projectID,
		GeneratedAt:    now.Format(time.RFC3339),
		EstimationUnit: project.EstimationUnit,
		Throughput:     []throughputWeek{},
		Workload:       []assigneeWorkload{},
	}

	if err := h.statsTaskTotals(&stats, projectID, project.EstimationUnit, now); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.statsDistributions(&stats, projectID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.statsDelivery(&stats, projectID, project.EstimationUnit, now); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.statsWorkload(&stats, projectID, project.EstimationUnit); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.statsSprint(&stats, projectID, project.EstimationUnit, now); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if err := h.statsReleases(&stats, projectID, now); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, stats)
}

// statsTaskTotals fills the headline counters and, when the project estimates,
// the effort block — one aggregate row over the project's tasks.
func (h *Handler) statsTaskTotals(stats *projectStatistics, projectID, unit string, now time.Time) error {
	today := now.Format(sprintDateLayout)
	dueSoonLimit := now.AddDate(0, 0, statsDueSoonDays).Format(sprintDateLayout)
	last30 := now.AddDate(0, 0, -30).Format(time.RFC3339)

	// Effort columns are selected as a pair and picked apart by unit below;
	// the estimable-type filter mirrors the write rule (only leaf types carry
	// an estimate), so an EPIC never shows up as "unestimated".
	row := h.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED')),
		       COUNT(*) FILTER (WHERE status = 'IN_PROGRESS'),
		       COUNT(*) FILTER (WHERE status = 'DONE'),
		       COUNT(*) FILTER (WHERE status = 'ARCHIVED'),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED') AND assignee_id IS NULL),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED') AND due_date IS NOT NULL AND due_date < $2),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED') AND due_date IS NOT NULL AND due_date >= $2 AND due_date <= $3),
		       COUNT(*) FILTER (WHERE created_at >= $4),
		       COUNT(*) FILTER (WHERE done_at IS NOT NULL AND done_at >= $4),
		       COALESCE(SUM(story_points),0),
		       COALESCE(SUM(story_points) FILTER (WHERE status IN ('DONE','ARCHIVED')),0),
		       COALESCE(SUM(estimate_hours),0),
		       COALESCE(SUM(estimate_hours) FILTER (WHERE status IN ('DONE','ARCHIVED')),0),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED') AND task_type = ANY($5) AND story_points IS NULL),
		       COUNT(*) FILTER (WHERE status NOT IN ('DONE','ARCHIVED') AND task_type = ANY($5) AND estimate_hours IS NULL)
		  FROM tasks WHERE project_id = $1`,
		projectID, today, dueSoonLimit, last30, EstimableTaskTypes())

	var t taskStatistics
	var pointsTotal, pointsDone, hoursTotal, hoursDone float64
	var pointsUnestimated, hoursUnestimated int
	if err := row.Scan(&t.Total, &t.Open, &t.InProgress, &t.Done, &t.Archived,
		&t.Unassigned, &t.Overdue, &t.DueSoon, &t.CreatedLast30, &t.CompletedLast30,
		&pointsTotal, &pointsDone, &hoursTotal, &hoursDone,
		&pointsUnestimated, &hoursUnestimated); err != nil {
		return err
	}
	stats.Tasks = t

	switch unit {
	case EstimationUnitPoints:
		stats.Effort = newEffortStatistics(unit, pointsTotal, pointsDone, pointsUnestimated)
	case EstimationUnitHours:
		stats.Effort = newEffortStatistics(unit, hoursTotal, hoursDone, hoursUnestimated)
	}
	return nil
}

func newEffortStatistics(unit string, total, done float64, unestimated int) *effortStatistics {
	remaining := total - done
	if remaining < 0 {
		remaining = 0
	}
	return &effortStatistics{
		Unit:        unit,
		Total:       round2(total),
		Done:        round2(done),
		Remaining:   round2(remaining),
		Unestimated: unestimated,
	}
}

// statsDistributions fills the status/type/priority breakdowns, each returned
// in the domain's own enum order with the empty buckets included — a chart
// that drops "IN_REVIEW: 0" changes shape between reloads.
func (h *Handler) statsDistributions(stats *projectStatistics, projectID string) error {
	byStatus, err := h.statsGroupCount(`SELECT status, COUNT(*) FROM tasks WHERE project_id=$1 GROUP BY status`, projectID)
	if err != nil {
		return err
	}
	byType, err := h.statsGroupCount(`SELECT task_type, COUNT(*) FROM tasks WHERE project_id=$1 GROUP BY task_type`, projectID)
	if err != nil {
		return err
	}
	byPriority, err := h.statsGroupCount(
		`SELECT priority, COUNT(*) FROM tasks WHERE project_id=$1 AND status NOT IN ('DONE','ARCHIVED') GROUP BY priority`, projectID)
	if err != nil {
		return err
	}
	stats.Tasks.ByStatus = orderedCounts(ValidStatuses(), byStatus)
	stats.Tasks.ByType = orderedCounts(ValidTaskTypes(), byType)
	// Priorities are project-configurable (custom priorities on top of the
	// built-ins), so the known order is a prefix and anything else follows it
	// alphabetically rather than being dropped.
	stats.Tasks.ByPriority = orderedCounts(ValidPriorities(), byPriority)
	return nil
}

func (h *Handler) statsGroupCount(query, projectID string) (map[string]int, error) {
	rows, err := h.db.Query(query, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

// orderedCounts renders a bucket map in a stable presentation order: the known
// enum values first (including zeros), then any unknown key alphabetically so
// a custom priority or a value from a newer release still shows up.
func orderedCounts(known []string, counts map[string]int) []countEntry {
	out := make([]countEntry, 0, len(counts)+len(known))
	seen := make(map[string]bool, len(known))
	for _, k := range known {
		seen[k] = true
		out = append(out, countEntry{Key: k, Count: counts[k]})
	}
	extra := make([]string, 0)
	for k := range counts {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, countEntry{Key: k, Count: counts[k]})
	}
	return out
}

// statsDelivery fills the throughput series and the cycle-time sample from one
// bounded scan: the tasks finished inside the longer of the two windows.
func (h *Handler) statsDelivery(stats *projectStatistics, projectID, unit string, now time.Time) error {
	weekStart := startOfWeek(now)
	throughputFrom := weekStart.AddDate(0, 0, -7*(statsThroughputWeeks-1))
	cycleFrom := now.AddDate(0, 0, -statsCycleTimeDays)
	from := throughputFrom
	if cycleFrom.Before(from) {
		from = cycleFrom
	}

	rows, err := h.db.Query(finishedTaskQueries[unit], projectID, from.Format(time.RFC3339))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	// Buckets keyed by the Monday the week starts on, pre-created so a week
	// with no deliveries draws a gap in the bar chart instead of vanishing.
	buckets := make(map[string]*throughputWeek, statsThroughputWeeks)
	series := make([]throughputWeek, 0, statsThroughputWeeks)
	for i := statsThroughputWeeks - 1; i >= 0; i-- {
		key := weekStart.AddDate(0, 0, -7*i).Format(sprintDateLayout)
		series = append(series, throughputWeek{WeekStart: key})
	}
	for i := range series {
		buckets[series[i].WeekStart] = &series[i]
	}
	if unit != EstimationUnitNone {
		for i := range series {
			zero := 0.0
			series[i].Effort = &zero
		}
	}

	var cycleDays []float64
	for rows.Next() {
		var createdAt, doneAt string
		var estimate *float64
		if err := rows.Scan(&createdAt, &doneAt, &estimate); err != nil {
			return err
		}
		done, err := time.Parse(time.RFC3339, doneAt)
		if err != nil {
			continue
		}
		if !done.Before(throughputFrom) {
			if b := buckets[startOfWeek(done).Format(sprintDateLayout)]; b != nil {
				b.Completed++
				if b.Effort != nil && estimate != nil {
					*b.Effort = round2(*b.Effort + *estimate)
				}
			}
		}
		if !done.Before(cycleFrom) {
			if created, err := time.Parse(time.RFC3339, createdAt); err == nil && !done.Before(created) {
				cycleDays = append(cycleDays, done.Sub(created).Hours()/24)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	stats.Throughput = series
	stats.CycleTime = summariseCycleTime(cycleDays)
	return nil
}

// summariseCycleTime reduces the sample to average and median. Both are null
// for an empty sample — a project that has finished nothing has no cycle time,
// which is not the same as a cycle time of zero.
func summariseCycleTime(days []float64) cycleTimeStatistics {
	out := cycleTimeStatistics{SampleSize: len(days)}
	if len(days) == 0 {
		return out
	}
	sort.Float64s(days)
	sum := 0.0
	for _, d := range days {
		sum += d
	}
	avg := round2(sum / float64(len(days)))
	mid := len(days) / 2
	median := days[mid]
	if len(days)%2 == 0 {
		median = (days[mid-1] + days[mid]) / 2
	}
	median = round2(median)
	out.AverageDays = &avg
	out.MedianDays = &median
	return out
}

// statsWorkload fills the per-assignee open-work breakdown, busiest first.
func (h *Handler) statsWorkload(stats *projectStatistics, projectID, unit string) error {
	rows, err := h.db.Query(openWorkloadQueries[unit], projectID, statsWorkloadMax)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	out := make([]assigneeWorkload, 0, statsWorkloadMax)
	for rows.Next() {
		var wl assigneeWorkload
		var effort sql.NullFloat64
		if err := rows.Scan(&wl.UserID, &wl.Name, &wl.Open, &effort); err != nil {
			return err
		}
		if effort.Valid {
			v := round2(effort.Float64)
			wl.Effort = &v
		}
		out = append(out, wl)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	stats.Workload = out
	return nil
}

// statsSprint fills the active-sprint block, reusing the same board-scope
// definition the sprint card and the burndown use.
func (h *Handler) statsSprint(stats *projectStatistics, projectID, unit string, now time.Time) error {
	sp, err := h.sprints.FindActive(projectID)
	if err != nil || sp == nil {
		return err
	}
	total, done, err := h.sprints.CountTasks(sp.ID)
	if err != nil {
		return err
	}
	out := sprintStatistics{
		SprintID:  sp.ID,
		Name:      sp.Name,
		StartDate: sp.StartDate,
		EndDate:   sp.EndDate,
		Committed: total,
		Completed: done,
	}
	if sp.EndDate != nil {
		if end, err := time.ParseInLocation(sprintDateLayout, *sp.EndDate, time.UTC); err == nil {
			left := int(math.Ceil(end.AddDate(0, 0, 1).Sub(now).Hours() / 24))
			if left < 0 {
				left = 0
			}
			out.DaysRemaining = &left
		}
	}
	if unit != EstimationUnitNone {
		committed, completed, err := h.sprints.SumEstimates(sp.ID, unit)
		if err != nil {
			return err
		}
		committed, completed = round2(committed), round2(completed)
		out.CommittedEstimate = &committed
		out.CompletedEstimate = &completed
	}
	stats.Sprint = &out
	return nil
}

// statsReleases fills the release plan summary.
func (h *Handler) statsReleases(stats *projectStatistics, projectID string, now time.Time) error {
	today := now.Format(sprintDateLayout)
	row := h.db.QueryRow(`
		SELECT COUNT(*) FILTER (WHERE status <> 'CLOSED'),
		       COUNT(*) FILTER (WHERE status = 'CLOSED'),
		       COUNT(*) FILTER (WHERE status <> 'CLOSED' AND due_date IS NOT NULL AND due_date < $2),
		       MIN(due_date) FILTER (WHERE status <> 'CLOSED' AND due_date IS NOT NULL)
		  FROM releases WHERE project_id = $1`, projectID, today)
	var rel releaseStatistics
	var nextDue *string
	if err := row.Scan(&rel.Open, &rel.Closed, &rel.OverdueOpen, &nextDue); err != nil {
		return err
	}
	rel.NextDue = nextDue
	if nextDue != nil {
		var name string
		err := h.db.QueryRow(
			`SELECT name FROM releases WHERE project_id=$1 AND status <> 'CLOSED' AND due_date=$2 ORDER BY name LIMIT 1`,
			projectID, *nextDue).Scan(&name)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			rel.NextDueName = &name
		}
	}
	stats.Releases = rel
	return nil
}

// startOfWeek snaps t to the Monday of its ISO week, at midnight UTC.
func startOfWeek(t time.Time) time.Time {
	day := t.UTC().Truncate(24 * time.Hour)
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}

// round2 clamps a computed figure to two decimals so hour sums do not leak
// float noise (0.30000000000000004) into the API.
func round2(v float64) float64 { return math.Round(v*100) / 100 }
