// Package julia provides a registry client for Julia packages.
package julia

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/git-pkgs/registries/internal/core"
	"github.com/git-pkgs/registries/internal/urlparser"
)

const (
	DefaultURL = "https://raw.githubusercontent.com/JuliaRegistries/General/master"
	ecosystem  = "julia"
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

func (r *Registry) URLs() core.URLBuilder { //nolint:ireturn
	return r.urls
}

// getPackagePath returns the registry path for a package
// Julia uses first letter as directory prefix: A/Algorithms, C/CSV, etc.
func getPackagePath(name string) string {
	if len(name) == 0 {
		return ""
	}
	firstLetter := strings.ToUpper(string(name[0]))
	return fmt.Sprintf("%s/%s", firstLetter, name)
}

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
	path := getPackagePath(name)
	pkgURL := fmt.Sprintf("%s/%s/Package.toml", r.baseURL, path)

	body, err := r.client.GetBody(ctx, pkgURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	pkg := parsePackageToml(string(body))

	return &core.Package{
		Name:       pkg.name,
		Repository: urlparser.Parse(pkg.repo),
		Metadata: map[string]any{
			"uuid":    pkg.uuid,
			"subdir":  pkg.subdir,
		},
	}, nil
}

type packageInfo struct {
	name   string
	uuid   string
	repo   string
	subdir string
}

func parsePackageToml(content string) packageInfo {
	info := packageInfo{}
	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2) //nolint:mnd // key=value split
		if len(parts) != 2 {                  //nolint:mnd
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")

		switch key {
		case "name":
			info.name = value
		case "uuid":
			info.uuid = value
		case "repo":
			info.repo = value
		case "subdir":
			info.subdir = value
		}
	}

	return info
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
	path := getPackagePath(name)
	versionsURL := fmt.Sprintf("%s/%s/Versions.toml", r.baseURL, path)

	body, err := r.client.GetBody(ctx, versionsURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
		}
		return nil, err
	}

	versionMap := parseVersionsToml(string(body))

	// Sort versions in descending order (newest first)
	versionNumbers := make([]string, 0, len(versionMap))
	for v := range versionMap {
		versionNumbers = append(versionNumbers, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versionNumbers)))

	versions := make([]core.Version, 0, len(versionNumbers))
	for _, v := range versionNumbers {
		info := versionMap[v]
		var integrity string
		if info.gitTreeSha1 != "" {
			integrity = "sha1-" + info.gitTreeSha1
		}
		versions = append(versions, core.Version{
			Number:    v,
			Integrity: integrity,
			Metadata: map[string]any{
				"git-tree-sha1": info.gitTreeSha1,
			},
		})
	}

	return versions, nil
}

type versionInfo struct {
	gitTreeSha1 string
}

func parseVersionsToml(content string) map[string]versionInfo {
	versions := make(map[string]versionInfo)
	scanner := bufio.NewScanner(strings.NewReader(content))

	var currentVersion string
	var currentInfo versionInfo

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for version section header: ["1.2.3"]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Save previous version if any
			if currentVersion != "" {
				versions[currentVersion] = currentInfo
			}
			currentVersion = strings.Trim(line, "[]\"")
			currentInfo = versionInfo{}
			continue
		}

		// Parse key-value pairs within version section
		parts := strings.SplitN(line, "=", 2) //nolint:mnd // key=value split
		if len(parts) != 2 {                  //nolint:mnd
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")

		if key == "git-tree-sha1" {
			currentInfo.gitTreeSha1 = value
		}
	}

	// Save last version
	if currentVersion != "" {
		versions[currentVersion] = currentInfo
	}

	return versions
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
	path := getPackagePath(name)
	depsURL := fmt.Sprintf("%s/%s/Deps.toml", r.baseURL, path)

	body, err := r.client.GetBody(ctx, depsURL)
	if err != nil {
		if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
			// No Deps.toml means no dependencies
			return nil, nil
		}
		return nil, err
	}

	depsByVersion := parseDepsToml(string(body))

	// Get dependencies for the specific version
	var deps []core.Dependency
	if verDeps, ok := depsByVersion[version]; ok {
		for depName := range verDeps {
			deps = append(deps, core.Dependency{
				Name:  depName,
				Scope: core.Runtime,
			})
		}
	}

	// Sort dependencies by name for consistent output
	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})

	return deps, nil
}

// parseDepsToml parses Julia's Deps.toml format
// Format:
// ["1.0"]
// PackageA = "uuid-a"
// PackageB = "uuid-b"
// ["1.1-2.0"]
// PackageA = "uuid-a"
func parseDepsToml(content string) map[string]map[string]string {
	deps := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))

	var currentVersions []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for version section header: ["1.0"] or ["1.0-2.0"]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			versionRange := strings.Trim(line, "[]\"")
			currentVersions = expandVersionRange(versionRange)
			// Initialize maps for all versions in range
			for _, v := range currentVersions {
				if deps[v] == nil {
					deps[v] = make(map[string]string)
				}
			}
			continue
		}

		// Parse dependency: PackageName = "uuid"
		parts := strings.SplitN(line, "=", 2) //nolint:mnd // key=value split
		if len(parts) != 2 {                  //nolint:mnd
			continue
		}

		depName := strings.TrimSpace(parts[0])
		uuid := strings.Trim(strings.TrimSpace(parts[1]), "\"")

		// Add dependency to all current versions
		for _, v := range currentVersions {
			if deps[v] != nil {
				deps[v][depName] = uuid
			}
		}
	}

	return deps
}

// expandVersionRange expands a version range like "1.0-2.0" or just "1.0"
// For simplicity, we return it as-is since Julia uses semver ranges in section headers
func expandVersionRange(versionRange string) []string {
	// Handle ranges like "1.0-2.0" - we'll store under both endpoints
	parts := strings.Split(versionRange, "-")
	if len(parts) == 2 { //nolint:mnd // range has start-end
		return parts
	}
	return []string{versionRange}
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
	// Julia's General registry doesn't store maintainer info
	// That information is in the package's GitHub repo
	return nil, nil
}

type URLs struct {
	baseURL string
}

func (u *URLs) Registry(name, version string) string {
	// Link to the package on JuliaHub
	if version != "" {
		return fmt.Sprintf("https://juliahub.com/ui/Packages/General/%s/%s", name, version)
	}
	return fmt.Sprintf("https://juliahub.com/ui/Packages/General/%s", name)
}

func (u *URLs) Download(name, version string) string {
	// Julia packages are installed from git repos, no direct download URL
	return ""
}

func (u *URLs) Documentation(name, version string) string {
	// Julia documentation is typically on GitHub or JuliaHub
	return fmt.Sprintf("https://juliahub.com/docs/General/%s", name)
}

func (u *URLs) PURL(name, version string) string {
	// Clean the name of any non-alphanumeric chars for PURL
	cleanName := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return -1
	}, name)

	if version != "" {
		return fmt.Sprintf("pkg:julia/%s@%s", cleanName, version)
	}
	return fmt.Sprintf("pkg:julia/%s", cleanName)
}
