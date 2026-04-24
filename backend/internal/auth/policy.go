package auth

import (
	"strings"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

const minPasswordLen = 12

// CheckPasswordPolicy enforces: minimum length, blocks substring-of-email-
// local-part or display-name tokens (case-insensitive, tokens >=4 chars),
// and blocks bloom-filter matches against the embedded common-passwords list.
func CheckPasswordPolicy(password, email, displayName string) error {
	if len(password) < minPasswordLen {
		return httpx.NewValidationError("password must be at least 12 characters")
	}
	lower := strings.ToLower(password)
	if local, _, ok := splitEmailLocal(email); ok && strings.Contains(lower, strings.ToLower(local)) {
		return httpx.NewValidationError("password cannot contain your email address")
	}
	for _, tok := range strings.Fields(displayName) {
		if len(tok) >= 4 && strings.Contains(lower, strings.ToLower(tok)) {
			return httpx.NewValidationError("password cannot contain your name")
		}
	}
	if IsCommonPassword(password) {
		return httpx.NewValidationError("that password is too common, pick another")
	}
	return nil
}

func splitEmailLocal(email string) (local, domain string, ok bool) {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "", "", false
	}
	return email[:at], email[at+1:], true
}
