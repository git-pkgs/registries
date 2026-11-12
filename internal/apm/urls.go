// Package apm implements Microsoft APM's experimental package registry API.
package apm

import (
	"fmt"
	"net/url"
	"strings"
)

type URLs struct {
	baseURL string
}

// splitIdentity splits owner/repo, treating every segment before the last as the owner as APM does.
func splitIdentity(name string) (owner, repo string, err error) {
	segments := strings.Split(name, "/")
	if len(segments) < 2 {
		return "", "", fmt.Errorf("invalid APM registry identity %q: expected owner/repo", name)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", fmt.Errorf("invalid APM registry identity %q: expected owner/repo", name)
		}
	}
	last := len(segments) - 1
	return strings.Join(segments[:last], "/"), segments[last], nil
}

// escape percent-encodes everything but RFC 3986 unreserved characters, matching APM's client.
func escape(segment string) string {
	var b strings.Builder
	for i := range len(segment) {
		c := segment[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// endpoint returns the versions URL, or the download URL when version is set.
func (u *URLs) endpoint(name, version string) (string, error) {
	owner, repo, err := splitIdentity(name)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(u.baseURL)
	// Rejecting any ? or # also catches empty delimiters, which url.Parse drops.
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
		strings.ContainsAny(u.baseURL, "?#") || base.User != nil {
		return "", fmt.Errorf("invalid APM registry URL %q", u.baseURL)
	}
	endpoint := strings.TrimRight(u.baseURL, "/") + "/v1/packages/" + escape(owner) + "/" + escape(repo) + "/versions"
	if version == "" {
		return endpoint, nil
	}
	if version == "." || version == ".." {
		return "", fmt.Errorf("invalid APM registry version %q", version)
	}
	return endpoint + "/" + escape(version) + "/download", nil
}

func (u *URLs) Registry(name, _ string) string {
	endpoint, _ := u.endpoint(name, "")
	return endpoint
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	endpoint, _ := u.endpoint(name, version)
	return endpoint
}

func (u *URLs) Documentation(_, _ string) string { return "" }

// PURL is empty: there is no APM PURL type, and owner/repo alone does not name a Git source.
func (u *URLs) PURL(_, _ string) string { return "" }
