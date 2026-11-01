package fetch

import "fmt"

// StatusError reports an upstream response whose status the fetcher does not
// handle itself: not a 404, not rate limiting and not a server error. The
// registry answered, so the status and the body it sent are the artifact's
// answer rather than a failure to reach the registry.
type StatusError struct {
	Code int
	// Body is what the upstream sent with the status, truncated to
	// maxErrBodySize. Head reads no body, so it is empty for a Head request.
	Body string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("unexpected status %d", e.Code)
	}
	return fmt.Sprintf("unexpected status %d: %s", e.Code, e.Body)
}
