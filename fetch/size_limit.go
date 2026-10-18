package fetch

import (
	"errors"
	"fmt"
	"io"
)

// ErrArtifactTooLarge reports a declared or streamed artifact over the limit.
var ErrArtifactTooLarge = errors.New("artifact exceeds size limit")

// WithMaxArtifactSize limits declared and streamed artifact bodies. Zero or
// negative values leave downloads unlimited. Observations remain incomplete
// when the limit is exceeded. HEAD requests still report the upstream size.
func WithMaxArtifactSize(maxSize int64) Option {
	return func(f *Fetcher) { f.maxArtifactSize = maxSize }
}

func (f *Fetcher) limitArtifact(artifact *Artifact) error {
	if f.maxArtifactSize <= 0 {
		return nil
	}
	if artifact.Size > f.maxArtifactSize {
		_ = artifact.Body.Close()
		return fmt.Errorf("%w: upstream declared %d bytes, limit is %d", ErrArtifactTooLarge, artifact.Size, f.maxArtifactSize)
	}
	artifact.Body = &sizeLimitedBody{ReadCloser: artifact.Body, remaining: f.maxArtifactSize}
	return nil
}

type sizeLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *sizeLimitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, ErrArtifactTooLarge
	}
	// Probe one byte beyond the limit without overflowing an unlimited int64.
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n + int(b.remaining), ErrArtifactTooLarge
	}
	return n, err
}
