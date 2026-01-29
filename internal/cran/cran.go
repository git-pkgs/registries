// Package cran provides a registry client for CRAN (R packages).
package cran

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://cran.r-project.org"
	ecosystem  = "cran"
)

func init() {
	core.Register(ecosystem, DefaultURL, func(baseURL string, client *core.Client) core.Registry {
		return New(baseURL, client)
	})
}

type Registry struct {
	baseURL string
	client  *core.Client
	urls    *URLs
}

func New(baseURL string, client *core.Client) *Registry {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	r := &Registry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
	}
	r.urls = &URLs{baseURL: r.baseURL}
	return r
}

func (r *Registry) Ecosystem() string {
	return ecosystem
}

func (r *Registry) URLs() core.URLBuilder {
	return r.urls
}

// descriptionInfo holds parsed DESCRIPTION file data
type descriptionInfo struct {
	Package      string
	Version      string
	Title        string
	Description  string
	License      string
	URL          string
	BugReports   string
	Author       string
	Maintainer   string
	Depends      string
	Imports      string
	Suggests     string
	LinkingTo    string
	Published    string
	NeedsCompilation string
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	// Fetch the DESCRIPTION file
	descURL := fmt.Sprintf("%s/web/packages/%s/DESCRIPTION", r.baseURL, name)
	body, err := r.client.GetBody(ctx, descURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	desc := parseDescription(string(body))

	// Extract repository URL from URL field
	repository := extractRepository(desc.URL)

	return &core.Package{
		Name:        desc.Package,
		Description: desc.Title,
		Homepage:    getFirstURL(desc.URL),
		Repository:  repository,
		Licenses:    desc.License,
		Metadata: map[string]any{
			"author":       desc.Author,
			"maintainer":   desc.Maintainer,
			"bug_reports":  desc.BugReports,
			"needs_compilation": desc.NeedsCompilation,
		},
	}, nil
}

func parseDescription(content string) descriptionInfo {
	info := descriptionInfo{}
	scanner := bufio.NewScanner(strings.NewReader(content))

	var currentField string
	var currentValue strings.Builder

	commitField := func() {
		if currentField == "" {
			return
		}
		value := strings.TrimSpace(currentValue.String())
		switch currentField {
		case "Package":
			info.Package = value
		case "Version":
			info.Version = value
		case "Title":
			info.Title = value
		case "Description":
			info.Description = value
		case "License":
			info.License = value
		case "URL":
			info.URL = value
		case "BugReports":
			info.BugReports = value
		case "Author":
			info.Author = value
		case "Maintainer":
			info.Maintainer = value
		case "Depends":
			info.Depends = value
		case "Imports":
			info.Imports = value
		case "Suggests":
			info.Suggests = value
		case "LinkingTo":
			info.LinkingTo = value
		case "Published":
			info.Published = value
		case "NeedsCompilation":
			info.NeedsCompilation = value
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Check if this is a new field (starts with non-whitespace and contains colon)
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && strings.Contains(line, ":") {
			// Commit previous field
			commitField()

			// Start new field
			parts := strings.SplitN(line, ":", 2)
			currentField = strings.TrimSpace(parts[0])
			currentValue.Reset()
			if len(parts) > 1 {
				currentValue.WriteString(strings.TrimSpace(parts[1]))
			}
		} else if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// Continuation line
			if currentValue.Len() > 0 {
				currentValue.WriteString(" ")
			}
			currentValue.WriteString(strings.TrimSpace(line))
		}
	}
	// Commit last field
	commitField()

	return info
}

func extractRepository(urlField string) string {
	urls := strings.Split(urlField, ",")
	for _, u := range urls {
		if parsed := urlparser.Parse(strings.TrimSpace(u)); parsed != "" {
			return parsed
		}
	}
	return ""
}

func getFirstURL(urlField string) string {
	urls := strings.Split(urlField, ",")
	if len(urls) > 0 {
		return strings.TrimSpace(urls[0])
	}
	return ""
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	// CRAN only keeps the current version, but we can get archived versions
	// First get current version from DESCRIPTION
	descURL := fmt.Sprintf("%s/web/packages/%s/DESCRIPTION", r.baseURL, name)
	body, err := r.client.GetBody(ctx, descURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	desc := parseDescription(string(body))

	var versions []core.Version

	// Add current version
	var publishedAt time.Time
	if desc.Published != "" {
		publishedAt, _ = time.Parse("2006-01-02", desc.Published)
	}

	versions = append(versions, core.Version{
		Number:      desc.Version,
		PublishedAt: publishedAt,
		Licenses:    desc.License,
	})

	// Try to get archived versions
	archiveURL := fmt.Sprintf("%s/src/contrib/Archive/%s/", r.baseURL, name)
	archiveBody, err := r.client.GetBody(ctx, archiveURL)
	if err == nil {
		// Parse the HTML directory listing to extract version numbers
		archivedVersions := parseArchiveVersions(string(archiveBody), name)
		for _, v := range archivedVersions {
			if v != desc.Version {
				versions = append(versions, core.Version{
					Number: v,
				})
			}
		}
	}

	return versions, nil
}

func parseArchiveVersions(html, pkgName string) []string {
	var versions []string
	// Match patterns like: pkgname_1.2.3.tar.gz
	pattern := regexp.MustCompile(regexp.QuoteMeta(pkgName) + `_([0-9]+\.[0-9]+[0-9.-]*)\.tar\.gz`)
	matches := pattern.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		if len(m) > 1 {
			versions = append(versions, m[1])
		}
	}
	return versions
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// For current version, use DESCRIPTION; for archived, fetch from archive
	var body []byte
	var err error

	// Try current version first
	descURL := fmt.Sprintf("%s/web/packages/%s/DESCRIPTION", r.baseURL, name)
	body, err = r.client.GetBody(ctx, descURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	desc := parseDescription(string(body))

	// Note: If version doesn't match, we'd ideally fetch from archive, but CRAN
	// archive doesn't have extracted DESCRIPTION files. Using current version's
	// dependencies as an approximation.

	var deps []core.Dependency

	// Parse Depends (runtime, usually includes R version)
	deps = append(deps, parseDependencyList(desc.Depends, core.Runtime)...)

	// Parse Imports (runtime)
	deps = append(deps, parseDependencyList(desc.Imports, core.Runtime)...)

	// Parse Suggests (optional/test)
	deps = append(deps, parseDependencyList(desc.Suggests, core.Optional)...)

	// Parse LinkingTo (build-time for compiled code)
	deps = append(deps, parseDependencyList(desc.LinkingTo, core.Build)...)

	return deps, nil
}

func parseDependencyList(depString string, scope core.Scope) []core.Dependency {
	var deps []core.Dependency
	if depString == "" {
		return deps
	}

	// Dependencies are comma-separated, with optional version constraints in parens
	// Example: "R (>= 3.5.0), methods, utils"
	depRegex := regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9.]*)\s*(\([^)]+\))?`)

	parts := strings.Split(depString, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		matches := depRegex.FindStringSubmatch(part)
		if len(matches) > 1 {
			name := matches[1]
			// Skip R itself
			if name == "R" {
				continue
			}

			requirements := ""
			if len(matches) > 2 && matches[2] != "" {
				// Remove parens from version constraint
				requirements = strings.Trim(matches[2], "()")
			}

			optional := scope == core.Optional

			deps = append(deps, core.Dependency{
				Name:         name,
				Requirements: requirements,
				Scope:        scope,
				Optional:     optional,
			})
		}
	}

	return deps
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	descURL := fmt.Sprintf("%s/web/packages/%s/DESCRIPTION", r.baseURL, name)
	body, err := r.client.GetBody(ctx, descURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	desc := parseDescription(string(body))

	if desc.Maintainer == "" {
		return nil, nil
	}

	// Parse maintainer string: "Name <email>"
	maintainer := parseMaintainer(desc.Maintainer)
	if maintainer.Name == "" && maintainer.Email == "" {
		return nil, nil
	}

	return []core.Maintainer{maintainer}, nil
}

func parseMaintainer(s string) core.Maintainer {
	// Format: "Name <email>"
	emailRegex := regexp.MustCompile(`^([^<]+)?<?([^>]+@[^>]+)?>?$`)
	matches := emailRegex.FindStringSubmatch(strings.TrimSpace(s))

	m := core.Maintainer{}
	if len(matches) > 1 {
		m.Name = strings.TrimSpace(matches[1])
	}
	if len(matches) > 2 {
		m.Email = strings.TrimSpace(matches[2])
	}
	return m
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/web/packages/%s/index.html", u.baseURL, name)
	}
	return fmt.Sprintf("%s/web/packages/%s/index.html", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("%s/src/contrib/%s_%s.tar.gz", u.baseURL, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	return fmt.Sprintf("%s/web/packages/%s/%s.pdf", u.baseURL, name, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:cran/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:cran/%s", name)
}
