package terraform

import (
	"context"
	"encoding/json"
	"fmt"
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

// serviceURL returns the base URL of a service, read from the registry's
// service discovery document at its host's /.well-known/terraform.json. A
// registry without the document, or without an entry for the service,
// serves the protocol at {base}/v1/modules/ and {base}/v1/providers/. The
// document is read once; a failure to read it is returned and retried on
// the next call.
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

func (r *Registry) discover(ctx context.Context) (*services, error) {
	found := &services{modules: r.baseURL + "/v1/modules/", providers: r.baseURL + "/v1/providers/"}
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
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
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
