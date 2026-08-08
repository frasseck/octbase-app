package shared

import "strings"

// validationMessageKeys maps the fixed English messages used with the
// VALIDATION_ERROR code to stable i18n translation keys. VALIDATION_ERROR is
// reused across many endpoints with different messages, so it can't be
// mapped to a key by code alone like other error codes.
//
// The keys live under "validation.", NOT under "errors." like the by-code
// ones — that is the namespace all four locale files use, and the one the
// classic-terminology overlay was written against
// (classic.validation.sprintNameRequired). They spelt it "errors.validation."
// here until 2026-08-08, which resolved to nothing, so every VALIDATION_ERROR
// showed its raw English message even in German (OCT-27).
// scripts/check-error-translations.mjs now reads this map and checks each key
// against the locale files, so the two sides cannot drift apart again.
var validationMessageKeys = map[string]string{
	"displayName is required":                     "validation.displayNameRequired",
	"displayName is too long":                     "validation.displayNameTooLong",
	"email is required":                           "validation.emailRequired",
	"name is required":                            "validation.nameRequired",
	"password is required":                        "validation.passwordRequired",
	"password must be at least 12 characters":     "validation.passwordTooShort",
	"project name must not be blank":              "validation.projectNameRequired",
	"sprint name must not be blank":               "validation.sprintNameRequired",
	"sprint end date must not precede start date": "validation.sprintEndBeforeStart",
	"invalid globalRole":                          "validation.invalidGlobalRole",
	"invalid project role":                        "validation.invalidProjectRole",
	"invalid status":                              "validation.invalidStatus",
	"isActive is required":                        "validation.isActiveRequired",
	"projectId is required":                       "validation.projectIdRequired",
	"taskIds must not be empty":                   "validation.taskIdsRequired",
}

// MessageKeyFor derives a stable i18n translation key for an error response
// from its machine-readable code and (English) message. Codes other than
// VALIDATION_ERROR map 1:1 to "errors.<camelCase(code)>"; VALIDATION_ERROR is
// looked up by message text since the same code covers many distinct
// messages, falling back to "validation.generic".
func MessageKeyFor(code, message string) string {
	if code == "VALIDATION_ERROR" {
		if key, ok := validationMessageKeys[message]; ok {
			return key
		}
		return "validation.generic"
	}
	return "errors." + screamingSnakeToCamel(code)
}

// screamingSnakeToCamel converts SCREAMING_SNAKE_CASE to camelCase, e.g.
// "PROJECT_NOT_FOUND" -> "projectNotFound".
func screamingSnakeToCamel(s string) string {
	parts := strings.Split(strings.ToLower(s), "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
