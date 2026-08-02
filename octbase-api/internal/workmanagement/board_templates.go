package workmanagement

import (
	"strings"

	"github.com/octbase/octbase-api/internal/shared"
)

// Board column templates. A new board can be seeded with a default set of
// lanes for Scrum or Kanban. Lane names are localized at creation time (the
// name is a stored, user-renamable string) from the creator's locale.
const (
	BoardTemplateNone   = "none"
	BoardTemplateKanban = "kanban"
	BoardTemplateScrum  = "scrum"
)

// templateColumn is one default lane: a task status plus its display name per
// supported locale.
type templateColumn struct {
	Status string
	Names  map[string]string
}

var boardTemplates = map[string][]templateColumn{
	BoardTemplateKanban: {
		{Status: StatusPlanned, Names: map[string]string{"en": "To Do", "de": "Zu erledigen"}},
		{Status: StatusInProgress, Names: map[string]string{"en": "In Progress", "de": "In Arbeit"}},
		{Status: StatusDone, Names: map[string]string{"en": "Done", "de": "Erledigt"}},
	},
	BoardTemplateScrum: {
		{Status: StatusPlanned, Names: map[string]string{"en": "To Do", "de": "Zu erledigen"}},
		{Status: StatusInProgress, Names: map[string]string{"en": "In Progress", "de": "In Arbeit"}},
		{Status: StatusInReview, Names: map[string]string{"en": "In Review", "de": "In Prüfung"}},
		{Status: StatusDone, Names: map[string]string{"en": "Done", "de": "Erledigt"}},
	},
}

// IsValidBoardTemplate reports whether t is a recognised template ("" and
// "none" mean "no default columns").
func IsValidBoardTemplate(t string) bool {
	switch t {
	case "", BoardTemplateNone, BoardTemplateKanban, BoardTemplateScrum:
		return true
	}
	return false
}

// normalizeLocale reduces an Accept-Language-ish value to a supported locale,
// defaulting to English.
func normalizeLocale(loc string) string {
	loc = strings.ToLower(strings.TrimSpace(loc))
	if len(loc) >= 2 {
		loc = loc[:2]
	}
	if loc == "de" {
		return "de"
	}
	return "en"
}

// templateColumnsFor builds the default columns for a template in the given
// locale, ready to persist. It returns nil for an unknown/none template.
func templateColumnsFor(boardID, template, locale, now string) []*BoardColumn {
	cols, ok := boardTemplates[template]
	if !ok {
		return nil
	}
	loc := normalizeLocale(locale)
	out := make([]*BoardColumn, 0, len(cols))
	for i, tc := range cols {
		name := tc.Names[loc]
		if name == "" {
			name = tc.Names["en"]
		}
		out = append(out, &BoardColumn{
			ID: shared.NewUUID(), BoardID: boardID, Name: name,
			Status: tc.Status, Position: i, CreatedAt: now, UpdatedAt: now,
		})
	}
	return out
}
