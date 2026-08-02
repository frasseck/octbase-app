package shared

import (
	"crypto/rand"
	"fmt"
	"time"
)

// NewUUID generates a UUID v4.
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Now returns the current time in RFC3339 format.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// PaginationParams holds pagination query parameters.
type PaginationParams struct {
	Page int
	Size int
}

// ErrorResponse is the standard error JSON body.
type ErrorResponse struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	MessageKey string `json:"messageKey,omitempty"`
	Details    any    `json:"details,omitempty"`
}

// IsValidUUID does a basic UUID format check.
func IsValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}
