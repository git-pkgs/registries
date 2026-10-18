package client

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError stores seconds in an int, including on 32-bit targets.
const maxRetryAfter = time.Duration(math.MaxInt32) * time.Second

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maxRetryAfter/time.Second) {
			return maxRetryAfter, true
		}
		return time.Duration(seconds) * time.Second, seconds > 0
	}
	at, err := http.ParseTime(value)
	if err != nil || !at.After(now) {
		return 0, false
	}
	return min(at.Sub(now), maxRetryAfter), true
}
