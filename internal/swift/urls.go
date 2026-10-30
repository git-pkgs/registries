// Package swift implements the Swift package registry protocol.
package swift

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/git-pkgs/purl"
	"github.com/git-pkgs/vers"
)

var (
	scopePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)
	namePattern  = regexp.MustCompile(`^[a-zA-Z0-9]+([-_][a-zA-Z0-9]+)*$`)
)

type URLs struct {
	baseURL string
}

func identityPath(identity string) (string, error) {
	const maxScope, maxName = 39, 100
	scope, name, ok := strings.Cut(identity, ".")
	if !ok || len(scope) > maxScope || len(name) > maxName ||
		!scopePattern.MatchString(scope) || !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid Swift registry identity %q: expected scope.name", identity)
	}
	return strings.ToLower(scope + "/" + name), nil
}

func (u *URLs) endpoint(name, version string) (string, error) {
	path, err := identityPath(name)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(u.baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
		base.RawQuery != "" || base.Fragment != "" || base.User != nil {
		return "", fmt.Errorf("invalid Swift registry URL %q", u.baseURL)
	}
	if version != "" {
		if !vers.ValidWithScheme(version, "semver") {
			return "", fmt.Errorf("invalid Swift release version %q", version)
		}
		path += "/" + url.PathEscape(version)
	}
	return strings.TrimRight(u.baseURL, "/") + "/" + path, nil
}

func (u *URLs) Registry(name, version string) string {
	endpoint, _ := u.endpoint(name, version)
	return endpoint
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	endpoint, err := u.endpoint(name, version)
	if err != nil {
		return ""
	}
	return endpoint + ".zip"
}

func (u *URLs) Documentation(_, _ string) string { return "" }

func (u *URLs) PURL(name, version string) string {
	if _, err := identityPath(name); err != nil {
		return ""
	}
	return purl.BuildPURLString(ecosystem, name, version, "")
}
