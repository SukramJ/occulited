package catalog

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Error codes for a release list that could not be read (B-21): what the install's progress and
// the page's check notice carry beside the English message, so the page can say it in German too.
const (
	CodeRateLimit   = "github-rate-limit"    // GitHub refused: the shared 60/h budget is spent
	CodeUnreachable = "releases-unreachable" // no answer, or an answer that is not the list
)

// ReleasesError is a release list of a repository that could not be read. There is no fallback to
// an older answer (B-21, maintainer 2026-09-26): an install that cannot read the list refuses, and
// a check keeps what it showed and says why it is not newer.
type ReleasesError struct {
	Repo        string
	RateLimited bool
	// RetryAt is when GitHub says the budget is back (Retry-After or X-RateLimit-Reset); zero when
	// the answer did not say.
	RetryAt time.Time
	Status  int   // the HTTP status, 0 for no answer
	Err     error // the transport error, nil for an HTTP answer
}

func (e *ReleasesError) Error() string { return e.message(time.Now()) }

// message is Error with the wait counted from now.
func (e *ReleasesError) message(now time.Time) string {
	if e.RateLimited {
		if m := e.RetryMinutes(now); m > 0 {
			return fmt.Sprintf("GitHub rate limit, try again in %d min: the release list of %s cannot be read now", m, e.Repo)
		}
		return fmt.Sprintf("GitHub rate limit, try again later: the release list of %s cannot be read now", e.Repo)
	}
	if e.Err != nil {
		return fmt.Sprintf("the release list of %s could not be read: %v", e.Repo, e.Err)
	}
	return fmt.Sprintf("the release list of %s could not be read: GitHub answered HTTP %d", e.Repo, e.Status)
}

func (e *ReleasesError) Unwrap() error { return e.Err }

// Code is CodeRateLimit or CodeUnreachable.
func (e *ReleasesError) Code() string {
	if e.RateLimited {
		return CodeRateLimit
	}
	return CodeUnreachable
}

// RetryMinutes is how many whole minutes from now until RetryAt, rounded up and at least 1; 0 when
// the time is unknown or has passed.
func (e *ReleasesError) RetryMinutes(now time.Time) int {
	if e.RetryAt.IsZero() || !e.RetryAt.After(now) {
		return 0
	}
	return max(1, int(math.Ceil(e.RetryAt.Sub(now).Minutes())))
}

// asReleasesError finds a ReleasesError in err.
func asReleasesError(err error) (*ReleasesError, bool) {
	var re *ReleasesError
	ok := errors.As(err, &re)
	return re, ok
}

// rateLimited reads a refused GitHub answer: whether it is the rate limit - 429, or 403 with the
// budget at zero, a Retry-After (the secondary limit) or the words in the body - and when to try
// again: Retry-After (seconds or a date) first, else X-RateLimit-Reset (Unix seconds).
func rateLimited(res *http.Response, body []byte, now time.Time) (bool, time.Time) {
	h := res.Header
	limited := res.StatusCode == http.StatusTooManyRequests ||
		(res.StatusCode == http.StatusForbidden && (h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != "" ||
			strings.Contains(strings.ToLower(string(body)), "rate limit")))
	if !limited {
		return false, time.Time{}
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return true, now.Add(time.Duration(n) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return true, t
		}
	}
	if v := strings.TrimSpace(h.Get("X-RateLimit-Reset")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return true, time.Unix(n, 0)
		}
	}
	return true, time.Time{}
}
