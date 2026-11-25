package account

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Locked reports whether err is the server's "account locked" answer to
// a sign-in with the right password (HTTP 423 `{"error":"account_locked",
// "retry_after":<s>}` plus a Retry-After header), and for how long.
// wait is 0 when the server did not say.
func Locked(err error) (wait time.Duration, ok bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return 0, false
	}
	if apiErr.Code != "account_locked" && !(apiErr.Status == http.StatusLocked && apiErr.Code == "") {
		return 0, false
	}
	return apiErr.RetryAfter, true
}

// LockedMessage is the pt-BR line shown for a locked account:
// "conta bloqueada — 15 min" (minutes rounded up), "conta bloqueada —
// menos de 1 min", or just "conta bloqueada" when no wait is known.
func LockedMessage(wait time.Duration) string {
	switch {
	case wait <= 0:
		return "conta bloqueada"
	case wait < time.Minute:
		return "conta bloqueada — menos de 1 min"
	}
	mins := int((wait + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("conta bloqueada — %d min", mins)
}
