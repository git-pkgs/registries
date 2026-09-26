//go:build tinygo

package safehttp_test

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/git-pkgs/registries/safehttp"
)

type rejectingTransport struct{ t *testing.T }

func (r rejectingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Error("unprotected transport called")
	return nil, errors.New("unexpected request")
}

func TestTinyGoSafeHTTPRejectsRequests(t *testing.T) {
	transport := rejectingTransport{t}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })
	base := &http.Client{Timeout: time.Second, Transport: transport}
	for _, input := range []*http.Client{nil, base} {
		for _, opts := range []safehttp.Options{{}, {AllowLoopback: true, AllowPrivate: true, AllowPrivateHosts: []string{"repo.example.test"}}} {
			client := safehttp.New(input, opts)
			_, err := client.Get("https://repo.example.test/demo")
			if !errors.Is(err, errors.ErrUnsupported) {
				t.Errorf("Get error = %v, want ErrUnsupported", err)
			}
			if input != nil && (client == input || client.Timeout != input.Timeout || input.Transport != transport) {
				t.Error("New must copy the client without modifying its settings")
			}
		}
	}
}

func TestTinyGoSafeHTTPAddressChecks(t *testing.T) {
	if err := safehttp.CheckIP(net.ParseIP("127.0.0.1"), safehttp.Options{}); err == nil {
		t.Error("CheckIP accepted loopback")
	}
	if err := safehttp.CheckHostIP("repo.example.test", net.ParseIP("10.0.0.1"), safehttp.Options{
		AllowPrivateHosts: []string{"repo.example.test"},
	}); err != nil {
		t.Errorf("CheckHostIP rejected an allowed private host: %v", err)
	}
}
