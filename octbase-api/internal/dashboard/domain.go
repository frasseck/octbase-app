// Package dashboard owns the personal settings dashboard's per-user cosmetic
// preferences (language, theme). It is deliberately separate from
// internal/security/mfa: preferences are low-risk and carry no crypto, while
// MFA is an auth/crypto surface — see docs/architecture.md.
package dashboard

// Preference values the frontend and mobile app currently support. These are
// validated server-side so the two enums can never drift silently between
// client and API.
var (
	// Languages mirrors octbase-frontend/js/i18n.js's AVAILABLE_LOCALES.
	Languages = []string{"en", "de"}
	// Themes mirrors the four options octbase-frontend/js/framework.js cycles
	// through (THEME_ORDER), including the "octopus" easter egg.
	Themes = []string{"system", "light", "dark", "octopus"}
	// Terminologies selects the vocabulary the UI labels things with: AGILE
	// (sprint, backlog, epic, story points) or CLASSIC (phase, task pool, work
	// package, effort points). It is a display preference only — no endpoint,
	// field name or stored value changes with it, so a team can mix both.
	Terminologies = []string{"AGILE", "CLASSIC"}
)

// DefaultLanguage, DefaultTheme and DefaultTerminology back-fill a preferences
// row the first time a user is read, so "no row yet" is never a special case
// for callers.
const (
	DefaultLanguage    = "en"
	DefaultTheme       = "system"
	DefaultTerminology = "AGILE"
)

// Preferences is a user's personal dashboard settings.
type Preferences struct {
	UserID      string `json:"userId"`
	Language    string `json:"language"`
	Theme       string `json:"theme"`
	Terminology string `json:"terminology"`
}

// IsValidLanguage reports whether lang is a supported locale.
func IsValidLanguage(lang string) bool {
	return contains(Languages, lang)
}

// IsValidTheme reports whether theme is a supported theme.
func IsValidTheme(theme string) bool {
	return contains(Themes, theme)
}

// IsValidTerminology reports whether terminology is a supported vocabulary.
func IsValidTerminology(terminology string) bool {
	return contains(Terminologies, terminology)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
