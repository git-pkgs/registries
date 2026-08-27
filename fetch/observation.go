package fetch

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"time"

	"github.com/git-pkgs/artifacts"
	"github.com/opencontainers/go-digest"
)

var observedResponseHeaders = []string{
	"Accept-Ranges",
	"Cache-Control",
	"Content-Disposition",
	"Content-Encoding",
	"Content-Length",
	"Content-Range",
	"Digest",
	"ETag",
	"Expires",
	"Last-Modified",
}

// FetchObservation contains metadata about a successful artifact response.
// ByteCount and Digests are populated, and Complete is set to true, only after
// the response body reaches EOF.
type FetchObservation struct {
	RequestedURL string
	FinalURL     string
	ResponseTime time.Duration // Time until the final response headers arrived.
	StatusCode   int
	Headers      http.Header // Allow-listed response headers only.
	DeclaredSize int64
	MediaType    string
	ByteCount    int64
	Digests      map[string]string
	Complete     bool
}

// Artifact converts a completed observation into a shared artifact value.
func (observation *FetchObservation) Artifact(packageURL, filename string) (artifacts.Artifact, error) {
	if observation == nil || !observation.Complete {
		return artifacts.Artifact{}, fmt.Errorf("fetch observation: incomplete")
	}

	sha256Digest := observation.Digests["sha256"]
	if sha256Digest == "" {
		return artifacts.Artifact{}, fmt.Errorf("fetch observation: missing SHA-256 digest")
	}

	artifact, err := artifacts.New(
		packageURL,
		digest.Digest("sha256:"+sha256Digest),
		observation.ByteCount,
		filename,
		observation.MediaType,
	)
	if err != nil {
		return artifacts.Artifact{}, fmt.Errorf("fetch observation: %w", err)
	}
	return artifact, nil
}

// ObservedArtifact contains an artifact and its fetch observation.
type ObservedArtifact struct {
	*Artifact
	Observation *FetchObservation
}

type observedBody struct {
	body        io.ReadCloser
	observation *FetchObservation
	sha256      hash.Hash
	sha512      hash.Hash
	byteCount   int64
}

func newObservedBody(body io.ReadCloser, observation *FetchObservation) io.ReadCloser {
	return &observedBody{
		body:        body,
		observation: observation,
		sha256:      sha256.New(),
		sha512:      sha512.New(),
	}
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.byteCount += int64(n)
		_, _ = b.sha256.Write(p[:n])
		_, _ = b.sha512.Write(p[:n])
	}
	if err == io.EOF && !b.observation.Complete {
		b.observation.ByteCount = b.byteCount
		b.observation.Digests = map[string]string{
			"sha256": hex.EncodeToString(b.sha256.Sum(nil)),
			"sha512": hex.EncodeToString(b.sha512.Sum(nil)),
		}
		b.observation.Complete = true
	}
	return n, err
}

func (b *observedBody) Close() error {
	return b.body.Close()
}

func copyObservedHeaders(headers http.Header) http.Header {
	observed := make(http.Header)
	for _, name := range observedResponseHeaders {
		if values := headers.Values(name); len(values) > 0 {
			observed[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	return observed
}
