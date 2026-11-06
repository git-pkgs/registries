package fetch

import "fmt"

// StatusError reports an upstream response whose status the fetcher does not
// handle itself: not a 404, not rate limiting and not a server error.
type StatusError struct {
	Code int
	// Body is what the upstream sent with the status, truncated to
	// maxErrBodySize. Head reads no body, so it is empty for a Head request.
	Body string
	// ContentType is the Content-Type the upstream sent with the status, so a
	// caller can forward the body as the upstream labelled it.
	ContentType string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("unexpected status %d", e.Code)
	}
	return fmt.Sprintf("unexpected status %d: %s", e.Code, e.Body)
}
