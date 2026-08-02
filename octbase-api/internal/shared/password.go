package shared

import "strings"

// MinPasswordLength is the minimum accepted password length for account
// creation and invitation acceptance. Raised from 8 to 12 to align with the
// "Passwortqualität und -richtlinie" requirement of the Kanton Zürich
// "Sichere Website" guidance and the strong-password baseline of the
// RiLi-Webservices access-control section (8.2.3).
const MinPasswordLength = 12

// commonPasswords is a small blocklist of the most frequently breached / trivial
// passwords. It is deliberately short (a full breach-corpus check belongs in a
// dedicated service); the goal is to reject the obvious choices a length rule
// alone still admits (e.g. "password1234", "123456789012").
var commonPasswords = map[string]bool{
	"password1234": true,
	"passwort1234": true,
	"123456789012": true,
	"qwertyuiop12": true,
	"adminadmin12": true,
	"letmein12345": true,
	"welcome12345": true,
	"octbase12345": true,
	"changeme1234": true,
	"iloveyou1234": true,
}

// ValidatePassword returns an empty string if pw meets the password policy, or
// a human-readable reason why it does not. Callers surface the reason via
// WriteValidationError with field "password".
func ValidatePassword(pw string) string {
	if len(pw) < MinPasswordLength {
		return "password must be at least 12 characters"
	}
	if commonPasswords[strings.ToLower(pw)] {
		return "password is too common; choose a less predictable password"
	}
	return ""
}
