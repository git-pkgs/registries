package terraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/git-pkgs/registries/internal/core"
)

const (
	discoveryPath    = "/.well-known/terraform.json"
	modulesService   = "modules.v1"
	providersService = "providers.v1"
)

// services are the base URLs of a registry's module and provider APIs, each
// ending in a slash.
type services struct {
	modules   string
	providers string
}

// serviceURL reads the discovery document once; a failure to read it is
// returned and retried on the next call.
func (r *Registry) serviceURL(ctx context.Context, service string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services == nil {
		found, err := r.discover(ctx)
		if err != nil {
			return "", err
		}
		r.services = found
	}
	if service == modulesService {
		return r.services.modules, nil
	}
	return r.services.providers, nil
}

// refused reports whether a registry answered with a 4xx other than 429: it
// doesn't serve what was asked for, as opposed to failing for now.
func refused(err error) bool {
	var httpErr *core.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 &&
		httpErr.StatusCode != http.StatusTooManyRequests
}

func (r *Registry) discover(ctx context.Context) (*services, error) {
	found := &services{modules: r.baseURL + "/v1/modules/", providers: r.baseURL + "/v1/providers/"}
	// registry.terraform.io serves these paths, and the PURL helpers build a
	// Registry per call, so asking it would double every lookup.
	if r.baseURL == DefaultURL {
		return found, nil
	}
	base, err := url.Parse(r.baseURL)
	if err != nil || base.Host == "" {
		return found, nil //nolint:nilerr // a base URL without a host has no discovery document
	}
	document := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: discoveryPath}
	var entries map[string]json.RawMessage
	// Service URLs are relative to where the document was found, after
	// redirects.
	final, err := r.client.GetJSONWithFinalURL(ctx, document.String(), &entries)
	if err != nil {
		// A refusal, or a body that isn't a JSON object, such as an HTML
		// page for any unknown path, means the host has no document.
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if refused(err) || errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			return found, nil
		}
		return nil, fmt.Errorf("terraform service discovery: %w", err)
	}
	for service, target := range map[string]*string{modulesService: &found.modules, providersService: &found.providers} {
		var location string
		// Other services, and entries that aren't a URL, are left alone.
		if raw, ok := entries[service]; !ok || json.Unmarshal(raw, &location) != nil || location == "" {
			continue
		}
		resolved, err := final.Parse(location)
		if err != nil || (resolved.Scheme != "https" && resolved.Scheme != "http") || resolved.Host == "" {
			return nil, fmt.Errorf("terraform service discovery: %s is %q, not an http or https URL", service, location)
		}
		*target = strings.TrimSuffix(resolved.String(), "/") + "/"
	}
	return found, nil
}
