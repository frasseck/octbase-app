package shared

import "strings"

// validationMessageKeys maps the fixed English messages used with the
// VALIDATION_ERROR code to stable i18n translation keys. VALIDATION_ERROR is
// reused across many endpoints with different messages, so it can't be
// mapped to a key by code alone like other error codes.
var validationMessageKeys = map[string]string{
	"displayName is required":                     "errors.validation.displayNameRequired",
	"displayName is too long":                     "errors.validation.displayNameTooLong",
	"email is required":                           "errors.validation.emailRequired",
	"name is required":                            "errors.validation.nameRequired",
	"password is required":                        "errors.validation.passwordRequired",
	"password must be at least 12 characters":     "errors.validation.passwordTooShort",
	"project name must not be blank":              "errors.validation.projectNameRequired",
	"sprint name must not be blank":               "errors.validation.sprintNameRequired",
	"sprint end date must not precede start date": "errors.validation.sprintEndBeforeStart",
	"invalid globalRole":                          "errors.validation.invalidGlobalRole",
	"invalid project role":                        "errors.validation.invalidProjectRole",
	"invalid status":                              "errors.validation.invalidStatus",
	"isActive is required":                        "errors.validation.isActiveRequired",
	"projectId is required":                       "errors.validation.projectIdRequired",
	"taskIds must not be empty":                   "errors.validation.taskIdsRequired",
}

// MessageKeyFor derives a stable i18n translation key for an error response
// from its machine-readable code and (English) message. Codes other than
// VALIDATION_ERROR map 1:1 to "errors.<camelCase(code)>"; VALIDATION_ERROR is
// looked up by message text since the same code covers many distinct
// messages, falling back to "errors.validation.generic".
func MessageKeyFor(code, message string) string {
	if code == "VALIDATION_ERROR" {
		if key, ok := validationMessageKeys[message]; ok {
			return key
		}
		return "errors.validation.generic"
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
