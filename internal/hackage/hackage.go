// Package hackage provides a registry client for Hackage (Haskell).
package hackage

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://hackage.haskell.org"
	ecosystem  = "hackage"
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

type packageInfoResponse struct {
	PackageDescription packageDescription `json:"packageDescription"`
}

type packageDescription struct {
	Package struct {
		PkgName    string `json:"pkgName"`
		PkgVersion string `json:"pkgVersion"`
	} `json:"package"`
	Synopsis    string `json:"synopsis"`
	Description string `json:"description"`
	License     string `json:"license"`
	Homepage    string `json:"homepage"`
	Author      string `json:"author"`
	Maintainer  string `json:"maintainer"`
	Category    string `json:"category"`
	SourceRepos []sourceRepo `json:"sourceRepos"`
	Dependencies []struct {
		Components []string `json:"components"`
		Dependency string   `json:"dependency"`
	} `json:"condTreeConstraints"`
}

type sourceRepo struct {
	RepoType   string `json:"repoType"`
	RepoLocation string `json:"repoLocation"`
}

type uploadInfo struct {
	UploadTime string `json:"time"`
	IsRevised  bool   `json:"isRevised"`
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	// First get the package info (latest version)
	infoURL := fmt.Sprintf("%s/package/%s/preferred", r.baseURL, name)
	body, err := r.client.GetBody(ctx, infoURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	// Parse preferred versions to get the latest
	versions := parsePreferredVersions(string(body))
	if len(versions) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}
	latestVersion := versions[0]

	// Fetch the cabal file info
	cabalURL := fmt.Sprintf("%s/package/%s-%s/%s.cabal", r.baseURL, name, latestVersion, name)
	cabalBody, err := r.client.GetBody(ctx, cabalURL)
	if err != nil {
		// Try without version
		cabalURL = fmt.Sprintf("%s/package/%s/%s.cabal", r.baseURL, name, name)
		cabalBody, err = r.client.GetBody(ctx, cabalURL)
		if err != nil {
			return nil, err
		}
	}

	cabal := parseCabalFile(string(cabalBody))

	repository := urlparser.Parse(cabal.SourceRepository)

	var keywords []string
	if cabal.Category != "" {
		keywords = strings.Split(cabal.Category, ",")
		for i := range keywords {
			keywords[i] = strings.TrimSpace(keywords[i])
		}
	}

	return &core.Package{
		Name:        name,
		Description: cabal.Synopsis,
		Homepage:    cabal.Homepage,
		Repository:  repository,
		Licenses:    cabal.License,
		Keywords:    keywords,
		Metadata: map[string]any{
			"author":     cabal.Author,
			"maintainer": cabal.Maintainer,
		},
	}, nil
}

type cabalInfo struct {
	Name             string
	Version          string
	Synopsis         string
	Description      string
	License          string
	Homepage         string
	Author           string
	Maintainer       string
	Category         string
	SourceRepository string
}

func parseCabalFile(content string) cabalInfo {
	info := cabalInfo{}
	lines := strings.Split(content, "\n")

	var currentField string
	var inSourceRepo bool

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}

		// Check for source-repository section
		if strings.HasPrefix(strings.ToLower(trimmed), "source-repository") {
			inSourceRepo = true
			continue
		}

		// Check for other sections that end source-repo parsing
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if inSourceRepo && strings.Contains(trimmed, ":") {
				field := strings.ToLower(strings.TrimSpace(strings.SplitN(trimmed, ":", 2)[0]))
				if field != "location" && field != "type" && field != "branch" && field != "tag" {
					inSourceRepo = false
				}
			}
		}

		if strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			field := strings.ToLower(strings.TrimSpace(parts[0]))
			value := ""
			if len(parts) > 1 {
				value = strings.TrimSpace(parts[1])
			}

			if inSourceRepo && field == "location" {
				info.SourceRepository = value
				continue
			}

			currentField = field
			switch field {
			case "name":
				info.Name = value
			case "version":
				info.Version = value
			case "synopsis":
				info.Synopsis = value
			case "description":
				info.Description = value
			case "license":
				info.License = value
			case "homepage":
				info.Homepage = value
			case "author":
				info.Author = value
			case "maintainer":
				info.Maintainer = value
			case "category":
				info.Category = value
			}
		} else if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			// Continuation line
			switch currentField {
			case "description":
				info.Description += " " + trimmed
			case "author":
				info.Author += " " + trimmed
			}
		}
	}

	return info
}

func parsePreferredVersions(content string) []string {
	// Parse the preferred versions format
	// Format: "normal-versions: 1.0.0, 2.0.0, ..."
	var versions []string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "normal-versions:") {
			versionStr := strings.TrimPrefix(line, "normal-versions:")
			parts := strings.Split(versionStr, ",")
			for _, p := range parts {
				v := strings.TrimSpace(p)
				if v != "" {
					versions = append(versions, v)
				}
			}
		}
	}

	// Sort versions in descending order (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})

	return versions
}

func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}

		if aNum != bNum {
			return aNum - bNum
		}
	}
	return 0
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	// Get the list of versions
	infoURL := fmt.Sprintf("%s/package/%s/preferred", r.baseURL, name)
	body, err := r.client.GetBody(ctx, infoURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versionStrings := parsePreferredVersions(string(body))
	if len(versionStrings) == 0 {
		return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
	}

	// Fetch upload times for each version
	versions := make([]core.Version, len(versionStrings))
	for i, v := range versionStrings {
		versions[i] = core.Version{Number: v}

		// Try to get upload info
		uploadURL := fmt.Sprintf("%s/package/%s-%s/upload-time", r.baseURL, name, v)
		uploadBody, err := r.client.GetBody(ctx, uploadURL)
		if err == nil {
			// Parse the upload time (format: "2023-10-15T12:00:00Z")
			timeStr := strings.TrimSpace(string(uploadBody))
			if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
				versions[i].PublishedAt = t
			}
		}
	}

	return versions, nil
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	// Fetch the cabal file
	cabalURL := fmt.Sprintf("%s/package/%s-%s/%s.cabal", r.baseURL, name, version, name)
	cabalBody, err := r.client.GetBody(ctx, cabalURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name, Version: version}
		}
		return nil, err
	}

	deps := parseDependencies(string(cabalBody))
	return deps, nil
}

func parseDependencies(content string) []core.Dependency {
	var deps []core.Dependency
	seen := make(map[string]bool)

	// Regex to match dependency items
	depItemRegex := regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*)\s*([<>=^]+[^,]*)?`)

	lines := strings.Split(content, "\n")
	inBuildDepends := false

	for _, line := range lines {
		lowerLine := strings.ToLower(strings.TrimSpace(line))

		// Check for build-depends: line (case insensitive)
		if strings.HasPrefix(lowerLine, "build-depends:") {
			inBuildDepends = true
			// Get the part after build-depends:
			idx := strings.Index(strings.ToLower(line), "build-depends:")
			if idx >= 0 {
				rest := strings.TrimSpace(line[idx+14:])
				if rest != "" {
					processDeps(rest, &deps, seen, depItemRegex)
				}
			}
			continue
		}

		// Continue parsing if we're in a build-depends block (continuation lines start with whitespace)
		if inBuildDepends {
			trimmed := strings.TrimSpace(line)

			// If line doesn't start with whitespace, we're done with this build-depends block
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && line != "" {
				inBuildDepends = false
				continue
			}

			// Skip empty lines or comments
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}

			// Check if this looks like a new field (has a colon not in version constraint)
			if strings.Contains(trimmed, ":") {
				colonIdx := strings.Index(trimmed, ":")
				beforeColon := trimmed[:colonIdx]
				// If before colon doesn't look like a version constraint, it's a new field
				if !strings.ContainsAny(beforeColon, "<>=^") {
					inBuildDepends = false
					continue
				}
			}

			processDeps(trimmed, &deps, seen, depItemRegex)
		}
	}

	return deps
}

func processDeps(line string, deps *[]core.Dependency, seen map[string]bool, depRegex *regexp.Regexp) {
	// Split by comma
	parts := strings.Split(line, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		matches := depRegex.FindStringSubmatch(part)
		if len(matches) > 1 {
			name := matches[1]
			if name == "base" || seen[name] {
				continue
			}
			seen[name] = true

			requirements := ""
			if len(matches) > 2 {
				requirements = strings.TrimSpace(matches[2])
			}

			*deps = append(*deps, core.Dependency{
				Name:         name,
				Requirements: requirements,
				Scope:        core.Runtime,
			})
		}
	}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Get the cabal file for maintainer info
	pkg, err := r.FetchPackage(ctx, name)
	if err != nil {
		return nil, err
	}

	if pkg.Metadata == nil {
		return nil, nil
	}

	maintainerStr, ok := pkg.Metadata["maintainer"].(string)
	if !ok || maintainerStr == "" {
		return nil, nil
	}

	// Parse maintainer string (format: "Name <email>")
	emailRegex := regexp.MustCompile(`([^<]+)?<?([^>]+@[^>]+)?>?`)
	matches := emailRegex.FindStringSubmatch(maintainerStr)

	if len(matches) > 1 {
		m := core.Maintainer{
			Name: strings.TrimSpace(matches[1]),
		}
		if len(matches) > 2 {
			m.Email = strings.TrimSpace(matches[2])
		}
		return []core.Maintainer{m}, nil
	}

	return []core.Maintainer{{Name: maintainerStr}}, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/package/%s-%s", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/package/%s", u.baseURL, name)
}

func (u *URLs) Download(name, version string) string {
	if version == "" {
		return ""
	}
	return fmt.Sprintf("%s/package/%s-%s/%s-%s.tar.gz", u.baseURL, name, version, name, version)
}

func (u *URLs) Documentation(name, version string) string {
	if version != "" {
		return fmt.Sprintf("%s/package/%s-%s/docs", u.baseURL, name, version)
	}
	return fmt.Sprintf("%s/package/%s/docs", u.baseURL, name)
}

func (u *URLs) PURL(name, version string) string {
	if version != "" {
		return fmt.Sprintf("pkg:hackage/%s@%s", name, version)
	}
	return fmt.Sprintf("pkg:hackage/%s", name)
}
