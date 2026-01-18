// Package core provides shared types and the registry system.
package core

import "time"

// Package represents metadata about a package from a registry.
type Package struct {
	Name        string
	Description string
	Homepage    string
	Repository  string
	Licenses    string
	Keywords    []string
	Namespace   string         // @scope for npm, groupId for maven
	Metadata    map[string]any // registry-specific data
}

// Version represents a specific version of a package.
type Version struct {
	Number      string
	PublishedAt time.Time
	Licenses    string
	Integrity   string        // sha256-..., sha512-...
	Status      VersionStatus // "", "yanked", "deprecated", "retracted"
	Metadata    map[string]any
}

// VersionStatus represents the status of a package version.
type VersionStatus string

const (
	StatusNone       VersionStatus = ""
	StatusYanked     VersionStatus = "yanked"
	StatusDeprecated VersionStatus = "deprecated"
	StatusRetracted  VersionStatus = "retracted"
)

// Dependency represents a package dependency.
type Dependency struct {
	Name         string
	Requirements string
	Scope        Scope
	Optional     bool
}

// Scope indicates when a dependency is required.
// Aligns with github.com/git-pkgs/manifests core.Scope.
type Scope string

const (
	Runtime     Scope = "runtime"
	Development Scope = "development"
	Test        Scope = "test"
	Build       Scope = "build"
	Optional    Scope = "optional"
)

// Maintainer represents a package maintainer.
type Maintainer struct {
	UUID  string
	Login string
	Name  string
	Email string
	URL   string
	Role  string
}
