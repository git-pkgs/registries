// Package urlparser extracts repository paths from git URLs.
// Works with any git hosting service, not just the major ones.
// Inspired by github.com/librariesio/librariesio-url-parser
package urlparser

import (
	"net/url"
	"regexp"
	"strings"
)

// Precompiled regexes - only used where string ops won't work
var (
	githubioRe = regexp.MustCompile(`(?i)^([\w.-]+)\.github\.(io|com|org)(?:$|/)`)
)

// Known hosts and their canonical domains
var knownHosts = map[string]string{
	"github.com":            "https://github.com",
	"github.io":             "https://github.com",
	"github.org":            "https://github.com",
	"githubusercontent.com": "https://github.com",
	"gitlab.com":            "https://gitlab.com",
	"bitbucket.org":         "https://bitbucket.org",
	"bitbucket.com":         "https://bitbucket.org",
	"codeberg.org":          "https://codeberg.org",
	"gitea.com":             "https://gitea.com",
	"sr.ht":                 "https://sr.ht",
}

// Subdomains to strip only for known hosts
var knownSubdomains = map[string]bool{
	"www":  true,
	"ssh":  true,
	"raw":  true,
	"git":  true,
	"wiki": true,
	"svn":  true,
}

// Precomputed subdomain prefixes for fast lookup: "www.github.com" -> "github.com"
var subdomainPrefixes map[string]string

func init() {
	subdomainPrefixes = make(map[string]string)
	for domain := range knownHosts {
		for subdomain := range knownSubdomains {
			prefix := subdomain + "." + domain
			subdomainPrefixes[prefix] = domain
		}
	}
}

// Clean removes common noise from git URLs: schemes, auth, brackets, anchors, etc.
// Returns just the host/path portion ready for further processing.
func Clean(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}

	// Remove whitespace, quotes, brackets using a single pass
	s = removeChars(s, " \t\n\r\"'><()[]")

	// Remove anchors
	if idx := strings.Index(s, "#"); idx != -1 {
		s = s[:idx]
	}

	// Remove querystring
	if idx := strings.Index(s, "?"); idx != -1 {
		s = s[:idx]
	}

	// Remove auth (user:pass@ or user@)
	s = removeAuth(s)

	// Remove leading =
	s = strings.TrimPrefix(s, "=")

	// Remove all scheme-like prefixes
	s = removeSchemes(s)

	// Strip common subdomains for known hosts
	s = stripKnownSubdomains(s)

	// Remove .git suffix (case insensitive)
	s = trimGitSuffix(s)

	// Remove trailing slashes
	s = strings.TrimSuffix(s, "/")

	// Handle : separator (git@host:path)
	s = normalizeColonSeparator(s)

	// Clean up double slashes
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}

	return s
}

// removeChars removes all characters in chars from s
func removeChars(s string, chars string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(chars, c) == -1 {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// trimGitSuffix removes .git or .git/ suffix case-insensitively
func trimGitSuffix(s string) string {
	const gitSuffix = ".git"
	const gitSlashSuffix = ".git/"
	if len(s) >= len(gitSuffix) {
		suffix := strings.ToLower(s[len(s)-len(gitSuffix):])
		if suffix == gitSuffix {
			return s[:len(s)-len(gitSuffix)]
		}
	}
	if len(s) >= len(gitSlashSuffix) {
		suffix := strings.ToLower(s[len(s)-len(gitSlashSuffix):])
		if suffix == gitSlashSuffix {
			return s[:len(s)-len(gitSlashSuffix)]
		}
	}
	return s
}

// removeSchemes strips all scheme prefixes from a URL
func removeSchemes(s string) string {
	for {
		prev := s

		// Remove scm:git:, scm:svn:, scm:hg: prefixes
		sLower := strings.ToLower(s)
		switch {
		case strings.HasPrefix(sLower, "scm:git:"):
			s = s[len("scm:git:"):]
		case strings.HasPrefix(sLower, "scm:svn:"):
			s = s[len("scm:svn:"):]
		case strings.HasPrefix(sLower, "scm:hg:"):
			s = s[len("scm:hg:"):]
		}

		// Remove standard schemes
		s = removeScheme(s)

		// Remove git// pattern
		if strings.HasPrefix(strings.ToLower(s), "git//") {
			s = s[5:]
		}

		// Remove leading slashes
		s = strings.TrimLeft(s, "/")

		if s == prev {
			break
		}
	}
	return s
}

// removeScheme removes a single URL scheme prefix
func removeScheme(s string) string {
	sLower := strings.ToLower(s)

	schemes := []string{
		"git+https://", "git+https:",
		"git+ssh://", "git+ssh:",
		"https://", "https:",
		"http://", "http:",
		"git://", "git:",
		"ssh://", "ssh:",
		"svn://", "svn:",
		"hg://", "hg:",
		"scm://", "scm:",
	}

	for _, scheme := range schemes {
		if strings.HasPrefix(sLower, scheme) {
			return s[len(scheme):]
		}
	}
	return s
}

// removeAuth removes user:pass@ or user@ from URLs
func removeAuth(s string) string {
	// Find @ but make sure we're past any scheme
	schemeEnd := 0
	const schemeSep = "://"
	if idx := strings.Index(s, schemeSep); idx != -1 {
		schemeEnd = idx + len(schemeSep)
	}

	rest := s[schemeEnd:]
	idx := strings.LastIndex(rest, "@")
	if idx == -1 {
		return s
	}

	// Make sure @ comes before first / (it's in the auth section)
	slashIdx := strings.Index(rest, "/")
	if slashIdx != -1 && idx >= slashIdx {
		return s
	}

	// Make sure there's a valid host after @
	afterAt := rest[idx+1:]
	if !strings.Contains(afterAt, ".") {
		colonIdx := strings.Index(afterAt, ":")
		slashInAfter := strings.Index(afterAt, "/")
		if colonIdx == -1 || (slashInAfter != -1 && colonIdx >= slashInAfter) {
			return s
		}
	}

	return s[:schemeEnd] + rest[idx+1:]
}

// stripKnownSubdomains removes common subdomains like www., git. from known hosts
func stripKnownSubdomains(s string) string {
	// Quick check: must contain at least one dot
	if !strings.Contains(s, ".") {
		return s
	}

	// Find where the potential subdomain.domain ends (at / or : or end)
	end := len(s)
	if idx := strings.Index(s, "/"); idx != -1 && idx < end {
		end = idx
	}
	if idx := strings.Index(s, ":"); idx != -1 && idx < end {
		end = idx
	}

	hostPart := strings.ToLower(s[:end])

	// Try to find a matching prefix
	if domain, ok := subdomainPrefixes[hostPart]; ok {
		return domain + s[end:]
	}

	return s
}

// normalizeColonSeparator handles git@host:path format
func normalizeColonSeparator(s string) string {
	idx := strings.Index(s, ":")
	if idx == -1 {
		return s
	}

	afterColon := s[idx+1:]

	// If it starts with /, it's already path-style
	if len(afterColon) > 0 && afterColon[0] == '/' {
		return s
	}

	// If it's numeric, it's a port
	if len(afterColon) > 0 && afterColon[0] >= '0' && afterColon[0] <= '9' {
		return s
	}

	// Convert host:path to host/path
	return s[:idx] + "/" + afterColon
}

// ExtractPath returns just the path portion after the domain.
func ExtractPath(rawURL string) string {
	s := Clean(rawURL)
	if s == "" {
		return ""
	}

	// Handle user.github.io/repo pattern
	if match := githubioRe.FindStringSubmatch(s); len(match) >= 2 { //nolint:mnd // regex groups
		user := match[1]
		rest := githubioRe.ReplaceAllString(s, "")
		if rest != "" {
			return user + "/" + rest
		}
		return ""
	}

	// Split on first /
	idx := strings.Index(s, "/")
	if idx == -1 || idx == len(s)-1 {
		return ""
	}

	path := s[idx+1:]

	// Clean up double slashes
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}

	return path
}

// ExtractOwnerRepo returns just the owner/repo portion.
func ExtractOwnerRepo(rawURL string) string {
	path := ExtractPath(rawURL)
	if path == "" {
		return ""
	}

	// Find first and second /
	firstSlash := strings.Index(path, "/")
	if firstSlash == -1 {
		return ""
	}

	owner := path[:firstSlash]
	rest := path[firstSlash+1:]

	if owner == "" || rest == "" {
		return ""
	}

	// Find end of repo (next / or end)
	secondSlash := strings.Index(rest, "/")
	var repo string
	if secondSlash == -1 {
		repo = rest
	} else {
		repo = rest[:secondSlash]
	}

	if repo == "" {
		return ""
	}

	return owner + "/" + repo
}

// ExtractHost returns the host portion of the URL.
func ExtractHost(rawURL string) string {
	s := Clean(rawURL)
	if s == "" {
		return ""
	}

	// Handle user.github.io pattern
	if match := githubioRe.FindStringSubmatch(s); len(match) >= 3 { //nolint:mnd // regex groups
		return "github." + match[2]
	}

	idx := strings.Index(s, "/")
	if idx == -1 {
		return s
	}
	return s[:idx]
}

// Parse attempts to parse a URL and return a canonical form.
func Parse(rawURL string) string {
	ownerRepo := ExtractOwnerRepo(rawURL)
	if ownerRepo == "" {
		return ""
	}

	host := ExtractHost(rawURL)
	if host == "" {
		return ""
	}

	canonical, normalizedHost := canonicalizeHost(host)
	if canonical != "" {
		return canonical + "/" + ownerRepo
	}

	return "https://" + normalizedHost + "/" + ownerRepo
}

// canonicalizeHost returns the canonical base URL and normalized host.
func canonicalizeHost(host string) (canonical string, normalizedHost string) {
	hostLower := strings.ToLower(host)

	if c, ok := knownHosts[hostLower]; ok {
		return c, hostLower
	}

	for domain, c := range knownHosts {
		if strings.HasSuffix(hostLower, "."+domain) {
			subdomain := strings.TrimSuffix(hostLower, "."+domain)
			if knownSubdomains[subdomain] {
				return c, domain
			}
			return c, hostLower
		}
	}

	return "", host
}

// IsKnownHost returns true if the URL is from a recognized git hosting service.
func IsKnownHost(rawURL string) bool {
	host := strings.ToLower(ExtractHost(rawURL))
	if host == "" {
		return false
	}

	if _, ok := knownHosts[host]; ok {
		return true
	}

	for domain := range knownHosts {
		if strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// Normalize cleans a git URL and ensures it has an https scheme.
func Normalize(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}

	s = removeAuth(s)
	s = removeSchemes(s)
	s = trimGitSuffix(s)
	s = strings.TrimSuffix(s, "/")
	s = normalizeColonSeparator(s)

	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}

	return s
}

// CanonicalURL returns the canonical URL for known hosts, or empty string.
func CanonicalURL(rawURL string) string {
	if !IsKnownHost(rawURL) {
		return ""
	}
	return Parse(rawURL)
}

// ParseURL is like Parse but returns structured data.
func ParseURL(rawURL string) *RepoURL {
	ownerRepo := ExtractOwnerRepo(rawURL)
	if ownerRepo == "" {
		return nil
	}

	idx := strings.Index(ownerRepo, "/")
	host := ExtractHost(rawURL)

	return &RepoURL{
		Host:  host,
		Owner: ownerRepo[:idx],
		Repo:  ownerRepo[idx+1:],
	}
}

// RepoURL represents a parsed repository URL.
type RepoURL struct {
	Host  string
	Owner string
	Repo  string
}

// String returns the canonical URL form.
func (r *RepoURL) String() string {
	if r == nil {
		return ""
	}

	canonical, normalizedHost := canonicalizeHost(r.Host)
	if canonical != "" {
		return canonical + "/" + r.Owner + "/" + r.Repo
	}

	return "https://" + normalizedHost + "/" + r.Owner + "/" + r.Repo
}

// OwnerRepo returns "owner/repo".
func (r *RepoURL) OwnerRepo() string {
	if r == nil {
		return ""
	}
	return r.Owner + "/" + r.Repo
}

// ParseFromMap extracts a repository URL from common field names in a map.
func ParseFromMap(m map[string]string, priorityKeys ...string) string {
	if len(priorityKeys) == 0 {
		priorityKeys = []string{"repository", "Repository", "source", "Source", "source_code", "Source Code", "Code"}
	}

	for _, key := range priorityKeys {
		if val, ok := m[key]; ok && val != "" {
			if result := Parse(val); result != "" {
				return result
			}
		}
	}

	for _, val := range m {
		if result := Parse(val); result != "" {
			if strings.Contains(val, "/sponsors") {
				continue
			}
			return result
		}
	}

	return ""
}

// FirstRepoURL returns the first URL from the list that parses as a repo.
func FirstRepoURL(urls ...string) string {
	for _, u := range urls {
		if u == "" {
			continue
		}
		if result := Parse(u); result != "" {
			return result
		}
	}
	return ""
}

// TryParseURL attempts to parse a URL using Go's url.Parse.
func TryParseURL(rawURL string) *url.URL {
	normalized := Normalize(rawURL)
	if normalized == "" {
		return nil
	}

	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil
	}

	return parsed
}
