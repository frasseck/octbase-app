package workmanagement

import (
	"errors"
	"math"
	"testing"
)

func TestIsImmutable(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{StatusDone, true},
		{StatusArchived, true},
		{StatusPlanned, false},
		{StatusInProgress, false},
		{StatusInReview, false},
		{"", false},
		{"UNKNOWN", false},
	}
	for _, tc := range tests {
		got := IsImmutable(tc.status)
		if got != tc.want {
			t.Errorf("IsImmutable(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestValidStatus(t *testing.T) {
	valid := []string{StatusPlanned, StatusInProgress, StatusInReview, StatusDone, StatusArchived}
	for _, s := range valid {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "DONE_AND_DUSTED", "planned", "in_progress"}
	for _, s := range invalid {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
}

func TestValidPriority(t *testing.T) {
	valid := []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical, PriorityBlocker}
	for _, s := range valid {
		if !ValidPriority(s) {
			t.Errorf("ValidPriority(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "EXTREME", "low", "high"}
	for _, s := range invalid {
		if ValidPriority(s) {
			t.Errorf("ValidPriority(%q) = true, want false", s)
		}
	}
}

func TestSlugFromName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Project", "my-project"},
		{"Hello World", "hello-world"},
		{"foo_bar", "foo-bar"},
		{"  leading", "leading"},
		{"trailing  ", "trailing"},
		{"UPPER CASE", "upper-case"},
		{"a--b", "a-b"},
		{"special!@#chars", "special-chars"},
		{"simple", "simple"},
		{"123 numbers", "123-numbers"},
	}
	for _, tc := range tests {
		got := SlugFromName(tc.input)
		if got != tc.want {
			t.Errorf("SlugFromName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidateTaskInput_BlankTitle(t *testing.T) {
	for _, title := range []string{"", "   ", "\t"} {
		err := ValidateTaskInput(title, "")
		if err == nil {
			t.Errorf("ValidateTaskInput(%q, ...) expected error, got nil", title)
			continue
		}
		var de *DomainError
		if !errors.As(err, &de) {
			t.Errorf("expected *DomainError, got %T", err)
			continue
		}
		if de.Code != "TASK_TITLE_REQUIRED" {
			t.Errorf("code = %q, want TASK_TITLE_REQUIRED", de.Code)
		}
	}
}

func TestValidateTaskInput_TitleTooLong(t *testing.T) {
	long := string(make([]byte, 256))
	for i := range long {
		long = long[:i] + "a" + long[i+1:]
	}
	err := ValidateTaskInput(long, "")
	if err == nil {
		t.Fatal("expected error for 256-char title, got nil")
	}
	var de *DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if de.Code != "TASK_TITLE_TOO_LONG" {
		t.Errorf("code = %q, want TASK_TITLE_TOO_LONG", de.Code)
	}
}

func TestValidateTaskInput_DescriptionTooLong(t *testing.T) {
	desc := make([]byte, 50001)
	for i := range desc {
		desc[i] = 'a'
	}
	err := ValidateTaskInput("valid title", string(desc))
	if err == nil {
		t.Fatal("expected error for 50001-char description, got nil")
	}
	var de *DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DomainError, got %T", err)
	}
	if de.Code != "DESCRIPTION_TOO_LONG" {
		t.Errorf("code = %q, want DESCRIPTION_TOO_LONG", de.Code)
	}
}

func TestValidateTaskInput_Valid(t *testing.T) {
	if err := ValidateTaskInput("A task", "some description"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCommentInput_Empty(t *testing.T) {
	err := ValidateCommentInput("")
	if err == nil {
		t.Fatal("expected error for empty comment, got nil")
	}
	var de *DomainError
	if !errors.As(err, &de) || de.Code != "COMMENT_INVALID" {
		t.Errorf("expected COMMENT_INVALID DomainError, got %v", err)
	}
}

func TestValidateCommentInput_TooLong(t *testing.T) {
	text := make([]byte, 10001)
	for i := range text {
		text[i] = 'x'
	}
	err := ValidateCommentInput(string(text))
	if err == nil {
		t.Fatal("expected error for 10001-char comment, got nil")
	}
	var de *DomainError
	if !errors.As(err, &de) || de.Code != "COMMENT_INVALID" {
		t.Errorf("expected COMMENT_INVALID DomainError, got %v", err)
	}
}

func TestValidateCommentInput_Valid(t *testing.T) {
	if err := ValidateCommentInput("This is a valid comment."); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidTaskType(t *testing.T) {
	valid := []string{TaskTypeTask, TaskTypeStory, TaskTypeEpic, TaskTypeSubtask}
	for _, s := range valid {
		if !ValidTaskType(s) {
			t.Errorf("ValidTaskType(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "FEATURE", "task", "bug", "BUG", "CHORE"}
	for _, s := range invalid {
		if ValidTaskType(s) {
			t.Errorf("ValidTaskType(%q) = true, want false", s)
		}
	}
}

func TestValidVisibility(t *testing.T) {
	if !ValidVisibility(VisibilityPublic) {
		t.Error("ValidVisibility(PUBLIC) = false, want true")
	}
	if !ValidVisibility(VisibilityPrivate) {
		t.Error("ValidVisibility(PRIVATE) = false, want true")
	}
	for _, s := range []string{"", "public", "INTERNAL", "open"} {
		if ValidVisibility(s) {
			t.Errorf("ValidVisibility(%q) = true, want false", s)
		}
	}
}

func TestDomainError_ErrorString(t *testing.T) {
	err := &DomainError{Code: "TEST_CODE", Message: "test message"}
	want := "TEST_CODE: test message"
	if err.Error() != want {
		t.Errorf("DomainError.Error() = %q, want %q", err.Error(), want)
	}
}

// TestEstimationUnitsMatchValidator guards the enum served at GET /meta/enums
// against drifting from the set the project PATCH actually accepts: a unit
// offered to clients but rejected on write (or vice versa) is a contract bug
// that only shows up in the UI.
func TestEstimationUnitsMatchValidator(t *testing.T) {
	units := ValidEstimationUnits()
	if len(units) != 3 || units[0] != EstimationUnitNone {
		t.Fatalf("ValidEstimationUnits() = %v, want 3 units with NONE first (the default)", units)
	}
	for _, u := range units {
		if !ValidEstimationUnit(u) {
			t.Errorf("unit %q is offered by ValidEstimationUnits but rejected by ValidEstimationUnit", u)
		}
	}
	for _, bad := range []string{"", "POINT", "hours", "STORY_POINTS"} {
		if ValidEstimationUnit(bad) {
			t.Errorf("ValidEstimationUnit(%q) = true, want false", bad)
		}
	}
}

// TestEstimableTaskType pins which types may carry an estimate: leaves yes,
// containers no.
func TestEstimableTaskType(t *testing.T) {
	for _, tt := range []string{TaskTypeStory, TaskTypeTask, TaskTypeSubtask} {
		if !EstimableTaskType(tt) {
			t.Errorf("EstimableTaskType(%q) = false, want true", tt)
		}
	}
	for _, tt := range []string{TaskTypeEpic, TaskTypeInitiative, TaskTypeTheme, "NONSENSE"} {
		if EstimableTaskType(tt) {
			t.Errorf("EstimableTaskType(%q) = true, want false", tt)
		}
	}
}

// TestValidateEstimateHoursPrecision covers the boundaries the HTTP tests only
// sample, including the float cases that a naive "two decimals" check gets
// wrong.
func TestValidateEstimateHoursPrecision(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, ok := range []*float64{nil, f(0), f(0.25), f(7.5), f(999.99), f(1000)} {
		if err := ValidateEstimateHours(ok); err != nil {
			t.Errorf("ValidateEstimateHours(%v) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []*float64{f(-0.01), f(1000.01), f(1.005), f(math.NaN()), f(math.Inf(1))} {
		if err := ValidateEstimateHours(bad); err == nil {
			t.Errorf("ValidateEstimateHours(%v) = nil, want an error", bad)
		}
	}
	i := func(v int) *int { return &v }
	for _, ok := range []*int{nil, i(0), i(1), i(100)} {
		if err := ValidateStoryPoints(ok); err != nil {
			t.Errorf("ValidateStoryPoints(%v) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []*int{i(-1), i(101)} {
		if err := ValidateStoryPoints(bad); err == nil {
			t.Errorf("ValidateStoryPoints(%v) = nil, want an error", bad)
		}
	}
}
