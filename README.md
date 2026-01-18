# registries

Go library for fetching package metadata from registry APIs. Supports 25 ecosystems with a unified interface.

## Installation

```bash
go get github.com/git-pkgs/registries
```

## Usage with PURLs

The simplest way to use this library is with Package URLs (PURLs). Pass a PURL string and get back package metadata.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/git-pkgs/registries"
    _ "github.com/git-pkgs/registries/all"
)

func main() {
    ctx := context.Background()

    // Fetch package info from a PURL
    pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:cargo/serde@1.0.0", nil)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(pkg.Name)       // serde
    fmt.Println(pkg.Repository) // https://github.com/serde-rs/serde
    fmt.Println(pkg.Licenses)   // MIT OR Apache-2.0
}
```

### PURL Functions

```go
// Fetch package metadata (works with or without version)
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:npm/lodash", nil)
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:npm/lodash@4.17.21", nil)

// Fetch specific version info (requires version in PURL)
version, err := registries.FetchVersionFromPURL(ctx, "pkg:cargo/serde@1.0.0", nil)
fmt.Println(version.PublishedAt)
fmt.Println(version.Licenses)

// Fetch dependencies for a version (requires version in PURL)
deps, err := registries.FetchDependenciesFromPURL(ctx, "pkg:npm/express@4.19.0", nil)
for _, d := range deps {
    fmt.Printf("%s %s\n", d.Name, d.Requirements)
}

// Fetch maintainers
maintainers, err := registries.FetchMaintainersFromPURL(ctx, "pkg:gem/rails", nil)
for _, m := range maintainers {
    fmt.Printf("%s <%s>\n", m.Login, m.Email)
}

// Fetch latest non-yanked version
latest, err := registries.FetchLatestVersionFromPURL(ctx, "pkg:cargo/serde", nil)
fmt.Println(latest.Number)      // e.g., "1.0.197"
fmt.Println(latest.PublishedAt)

// Parse a PURL to get the registry client
reg, name, version, err := registries.NewFromPURL("pkg:pypi/requests@2.31.0", nil)
// reg is a Registry for pypi
// name is "requests"
// version is "2.31.0"

// Parse a PURL to inspect its components
purl, err := registries.ParsePURL("pkg:maven/org.apache.commons/commons-lang3@3.12.0")
fmt.Println(purl.Type)      // maven
fmt.Println(purl.Namespace) // org.apache.commons
fmt.Println(purl.Name)      // commons-lang3
fmt.Println(purl.Version)   // 3.12.0
fmt.Println(purl.FullName()) // org.apache.commons:commons-lang3
```

### Bulk Operations

Fetch multiple packages in parallel (default concurrency: 15):

```go
purls := []string{
    "pkg:npm/lodash@4.17.21",
    "pkg:npm/express@4.19.0",
    "pkg:cargo/serde@1.0.0",
    "pkg:pypi/requests@2.31.0",
}

// Fetch all packages in parallel
packages := registries.BulkFetchPackages(ctx, purls, nil)
for purl, pkg := range packages {
    fmt.Printf("%s: %s\n", purl, pkg.Licenses)
}

// Fetch specific versions in parallel
versions := registries.BulkFetchVersions(ctx, purls, nil)
for purl, v := range versions {
    fmt.Printf("%s: published %s\n", purl, v.PublishedAt)
}

// Fetch latest versions in parallel
latest := registries.BulkFetchLatestVersions(ctx, purls, nil)
for purl, v := range latest {
    fmt.Printf("%s: latest is %s\n", purl, v.Number)
}

// Custom concurrency limit
packages = registries.BulkFetchPackagesWithConcurrency(ctx, purls, nil, 5)
```

### PURL Format Examples

| Ecosystem | PURL Example |
|-----------|-------------|
| Cargo | `pkg:cargo/serde@1.0.0` |
| npm | `pkg:npm/lodash@4.17.21` |
| npm (scoped) | `pkg:npm/%40babel/core@7.24.0` |
| PyPI | `pkg:pypi/requests@2.31.0` |
| Go | `pkg:golang/github.com/gorilla/mux@v1.8.0` |
| Maven | `pkg:maven/org.apache.commons/commons-lang3@3.12.0` |
| RubyGems | `pkg:gem/rails@7.1.0` |
| Terraform | `pkg:terraform/hashicorp/consul/aws@0.11.0` |

## Direct Registry Usage

You can also create registry clients directly by ecosystem name.

```go
import (
    "github.com/git-pkgs/registries"
    _ "github.com/git-pkgs/registries/internal/cargo"
)

reg, err := registries.New("cargo", "", nil)
if err != nil {
    log.Fatal(err)
}

pkg, err := reg.FetchPackage(ctx, "serde")
versions, err := reg.FetchVersions(ctx, "serde")
deps, err := reg.FetchDependencies(ctx, "serde", "1.0.0")
maintainers, err := reg.FetchMaintainers(ctx, "serde")
```

Import all ecosystems at once:

```go
import _ "github.com/git-pkgs/registries/all"
```

## Supported Ecosystems

| Ecosystem | PURL Type | Default Registry |
|-----------|-----------|------------------|
| Cargo | `cargo` | https://crates.io |
| npm | `npm` | https://registry.npmjs.org |
| RubyGems | `gem` | https://rubygems.org |
| PyPI | `pypi` | https://pypi.org |
| Go | `golang` | https://proxy.golang.org |
| Maven | `maven` | https://repo1.maven.org/maven2 |
| NuGet | `nuget` | https://api.nuget.org/v3 |
| Packagist | `composer` | https://packagist.org |
| Hex | `hex` | https://hex.pm |
| Pub | `pub` | https://pub.dev |
| CocoaPods | `cocoapods` | https://trunk.cocoapods.org |
| Clojars | `clojars` | https://clojars.org |
| CPAN | `cpan` | https://fastapi.metacpan.org |
| Hackage | `hackage` | https://hackage.haskell.org |
| CRAN | `cran` | https://cran.r-project.org |
| Conda | `conda` | https://api.anaconda.org |
| Julia | `julia` | https://raw.githubusercontent.com/JuliaRegistries/General/master |
| Elm | `elm` | https://package.elm-lang.org |
| Dub | `dub` | https://code.dlang.org |
| LuaRocks | `luarocks` | https://luarocks.org |
| Nimble | `nimble` | https://nimble.directory |
| Haxelib | `haxelib` | https://lib.haxe.org |
| Homebrew | `brew` | https://formulae.brew.sh |
| Deno | `deno` | https://apiland.deno.dev |
| Terraform | `terraform` | https://registry.terraform.io |

## Types

### Package

```go
type Package struct {
    Name          string
    Description   string
    Homepage      string
    Repository    string
    Licenses      string
    Keywords      []string
    Namespace     string         // @scope for npm, groupId for maven
    LatestVersion string         // latest version (populated by some registries)
    Metadata      map[string]any // registry-specific data
}
```

Some registries (npm, pub, deno, conda) populate `LatestVersion` directly. For others, use `FetchLatestVersionFromPURL`.

### Version

```go
type Version struct {
    Number      string
    PublishedAt time.Time
    Licenses    string
    Integrity   string        // sha256-..., sha512-...
    Status      VersionStatus // "", "yanked", "deprecated", "retracted"
    Metadata    map[string]any
}
```

### Dependency

```go
type Dependency struct {
    Name         string
    Requirements string
    Scope        Scope // runtime, development, test, build, optional
    Optional     bool
}
```

### Maintainer

```go
type Maintainer struct {
    UUID  string
    Login string
    Name  string
    Email string
    URL   string
    Role  string
}
```

## URL Builder

Each registry can generate URLs for packages:

```go
reg, _, _, _ := registries.NewFromPURL("pkg:cargo/serde@1.0.0", nil)
urls := reg.URLs()

urls.Registry("serde", "1.0.0")      // https://crates.io/crates/serde/1.0.0
urls.Download("serde", "1.0.0")      // https://static.crates.io/crates/serde/serde-1.0.0.crate
urls.Documentation("serde", "1.0.0") // https://docs.rs/serde/1.0.0
urls.PURL("serde", "1.0.0")          // pkg:cargo/serde@1.0.0
```

## Error Handling

```go
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:cargo/nonexistent", nil)
if err != nil {
    var notFound *registries.NotFoundError
    if errors.As(err, &notFound) {
        fmt.Printf("Package %s not found in %s\n", notFound.Name, notFound.Ecosystem)
    }
}
```

## HTTP Client

The default client includes:

- 30 second timeout
- 5 retries with exponential backoff (50ms base)
- Automatic retry on 429 and 5xx responses

Custom client:

```go
client := registries.NewClient(
    registries.WithTimeout(60 * time.Second),
    registries.WithMaxRetries(3),
)
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:npm/lodash", client)
```

## Private Registries

PURLs with a `repository_url` qualifier automatically use that URL:

```go
// This queries https://npm.mycompany.com instead of npmjs.org
purl := "pkg:npm/%40mycompany/utils@1.0.0?repository_url=https://npm.mycompany.com"
pkg, err := registries.FetchPackageFromPURL(ctx, purl, nil)

// Works with all PURL functions
versions := registries.BulkFetchPackages(ctx, []string{
    "pkg:npm/lodash@4.17.21",                                              // public
    "pkg:npm/%40internal/lib@1.0.0?repository_url=https://npm.internal",   // private
}, nil)
```

Or create a registry directly:

```go
reg, err := registries.New("npm", "https://npm.pkg.github.com", client)
```
