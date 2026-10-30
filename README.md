# registries

Go library for fetching package metadata from registry APIs. Supports 29 ecosystems with a unified interface. Also provides sub-packages for HTTP client usage (`client/`), streaming artifact downloads (`fetch/`) and registry change feeds (`changes/`).

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

// Fetch the registry-advertised latest version, with an active-release fallback
latest, err := registries.FetchLatestVersionFromPURL(ctx, "pkg:cargo/serde", nil)
fmt.Println(latest.Number)      // e.g., "1.0.197"
fmt.Println(latest.PublishedAt)

// Apply the same policy to versions fetched elsewhere
cargoPackage, err := registries.FetchPackageFromPURL(ctx, "pkg:cargo/serde", nil)
cargoRegistry, cargoName, _, err := registries.NewFromPURL("pkg:cargo/serde", nil)
cargoVersions, err := cargoRegistry.FetchVersions(ctx, cargoName)
latest = registries.SelectLatestVersion(cargoVersions, cargoRegistry.Ecosystem(), cargoPackage.LatestVersion)

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
| GitHub Actions | `pkg:githubactions/actions/checkout@v4` |
| Alpine | `pkg:apk/alpine/curl@8.22.0-r0?arch=x86_64&repository_url=https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64` |

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
| Helm | `helm` | URL required |
| Homebrew | `brew` | https://formulae.brew.sh |
| Deno | `deno` | https://apiland.deno.dev |
| Terraform | `terraform` | https://registry.terraform.io |
| Swift | `swift` | URL required |
| Alpine | `apk` | URL required |
| GitHub Actions | `githubactions` | https://api.github.com |

The Alpine URL is one repository architecture directory, such as `https://dl-cdn.alpinelinux.org/alpine/v3.23/main/x86_64`, or the `APKINDEX.tar.gz` inside it. The client doesn't pick a release, component or architecture from a mirror root. It reads v2 `APKINDEX.tar.gz` indexes. When a directory serves a v3 `Packages.adb`, the client returns an error rather than reading the v2 index.

GitHub Actions are git repositories, so `FetchPackage` reads `/repos/{owner}/{repo}` and `FetchVersions` lists tags from the GitHub REST API. Unauthenticated requests are limited to 60 per hour. A subdirectory action such as `pkg:githubactions/github/codeql-action/init@v3` resolves to the `github/codeql-action` repository. `URLs().Download` returns the `/archive/{ref}.tar.gz` source tarball for any tag, branch or commit SHA. GitHub doesn't guarantee these tarballs are byte-stable, so versions carry no `Integrity`. Dependencies and maintainers return an error wrapping `errors.ErrUnsupported`. For GitHub Enterprise Server, pass the `/api/v3` API root as the base URL.

### Swift registries

Swift lookups require an explicit registry URL and a registry identity:

```go
reg, err := registries.New("swift", "https://registry.example.com", nil)
// Handle err before using reg.
pkg, err := reg.FetchPackage(ctx, "scope.package-name")
versions, err := reg.FetchVersions(ctx, "scope.package-name")
```

Import `registries/all` to register the adapter. Identities are case-insensitive.
Source coordinates such as `github.com/owner/repo` are rejected; the adapter does
not infer registry identities from repository names. Swift source-based PURLs
cannot drive these lookups, and `URLs().PURL` returns an empty string.

`FetchPackage` reads metadata from the release named by the registry's
`latest-version` link, falling back to the highest available stable release and
then the highest prerelease. Pagination and release metadata URLs must stay on
the registry's origin.
`FetchVersions` follows pagination and fetches each available release
for its publication time, source archive URL and SHA256 checksum. A release
reported with a protocol `problem` is returned as `retracted`, with the problem
retained in its metadata. Other fetch or decoding failures return an error rather
than a partial version list.

Release metadata retains fields such as `licenseURL`, `author` and
`repositoryURLs`. A license URL is not converted into an SPDX expression, and an
author is not treated as a maintainer. Original publication time takes precedence
over registry publication time. Archive signatures are retained in artifact
metadata but are not verified.

`FetchDependencies` and `FetchMaintainers` return errors wrapping
`errors.ErrUnsupported`. The registry protocol supplies executable Swift
manifests rather than structured dependency data, and has no maintainer endpoint.
The adapter does not execute Swift or download archives. Authentication can be
provided through the shared client's HTTP transport.

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
    Maintainers   []Maintainer   // populated when the package endpoint includes them
    FundingLinks  []string       // funding or sponsorship URLs
}
```

Some registries (npm, pub, deno, conda) populate `LatestVersion` directly. For others, use `FetchLatestVersionFromPURL`.

`Maintainers` is filled in by npm and hex, whose package endpoints already return maintainer data. Other registries leave it nil; use `FetchMaintainers` for those. `FundingLinks` is filled in from npm `funding`, RubyGems `funding_uri`, Hex links and PyPI `project_urls` whose labels mention funding, sponsoring or donating.

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

`BuildURLs` collects all non-empty URLs into a map:

```go
allURLs := registries.BuildURLs(urls, "serde", "1.0.0")
// map[string]string{
//   "registry": "https://crates.io/crates/serde/1.0.0",
//   "download": "https://static.crates.io/crates/serde/serde-1.0.0.crate",
//   "docs":     "https://docs.rs/serde/1.0.0",
//   "purl":     "pkg:cargo/serde@1.0.0",
// }
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

## HTTP Client (`client/`)

The `client` sub-package provides an HTTP client with retry logic, error types, and URL building. You can use it through the top-level `registries` package or import it directly.

The default client includes:

- 30 second timeout
- 5 retries with exponential backoff (50ms base, 10% jitter)
- Automatic retry on 429 without a usable Retry-After and on 5xx responses
- Buffered response bodies limited to 10 MiB and streamed bodies to 512 MiB; a larger body fails with `ErrResponseTooLarge` (change with `WithMaxResponseSize` and `WithMaxStreamSize`)

Custom client via the top-level package:

```go
c := registries.NewClient(
    registries.WithTimeout(60 * time.Second),
    registries.WithMaxRetries(3),
)
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:npm/lodash", c)
```

Or import the sub-package directly:

```go
import "github.com/git-pkgs/registries/client"

c := client.NewClient(
    client.WithTimeout(60 * time.Second),
    client.WithMaxRetries(3),
)

// JSON decoding
var data map[string]any
err := c.GetJSON(ctx, "https://registry.npmjs.org/lodash", &data)

// Raw body
body, err := c.GetBody(ctx, "https://crates.io/api/v1/crates/serde")

// Streamed JSON, for documents too large to buffer
err = c.StreamJSON(ctx, "https://registry.npmjs.org/renovate", func(dec *json.Decoder) error {
    return dec.Decode(&data)
})

// HEAD request
statusCode, err := c.Head(ctx, "https://registry.npmjs.org/lodash")
```

`client.NewRateLimitGate(transport)` optionally holds requests by host after a
429, or a 503 with Retry-After. The first response passes through; later requests
fail locally with `*client.HeldError`, matching `client.ErrUpstreamHeld`.
`Held(url)` supports prechecks and `Holds()` reports active holds. Share one gate
between metadata and artifact HTTP clients to apply the same holds to both.

The gate accepts delay seconds and HTTP dates, caps header delays at one hour,
and doubles missing or unusable delays from one minute up to fifteen minutes.
`OnHold` and `OnRelease` callbacks support caller-owned logging and metrics;
configure them before concurrent use. Metadata clients return held errors and
429 responses with usable Retry-After immediately. `RateLimitError.RetryAfter`
reports seconds, rounded up for HTTP dates and capped to fit a 32-bit integer.

## Artifact Downloads (`fetch/`)

The `fetch` sub-package provides streaming artifact downloads with retry, circuit breaking, DNS caching, and URL resolution.

Under TinyGo, pass a host-compatible `*http.Client` through `fetch.WithHTTPClient`; the default fetch transport returns `errors.ErrUnsupported`. Registry API clients also accept a supplied client through `registries.WithHTTPClient`. The host must enforce address and redirect restrictions. `safehttp.New` and `WithSafeHTTP` return clients whose requests fail with `errors.ErrUnsupported`, since TinyGo transports bypass their dial-time address checks. The standalone IP-checking functions remain available. Native Go retains DNS caching and transport protection.

### Fetching artifacts

```go
import "github.com/git-pkgs/registries/fetch"

f := fetch.NewFetcher(
    fetch.WithMaxRetries(3),
    fetch.WithBaseDelay(500 * time.Millisecond),
    fetch.WithUserAgent("my-app/1.0"),
)

artifact, err := f.Fetch(ctx, "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz")
if err != nil {
    log.Fatal(err)
}
defer artifact.Body.Close()

// artifact.Body is an io.ReadCloser
// artifact.Size is the content length (-1 if unknown)
// artifact.ContentType and artifact.ETag are also available
io.Copy(dst, artifact.Body)
```

The fetcher uses DNS caching (5-minute refresh), connection pooling, and a 5-minute timeout suited for large artifacts. It retries on rate limits and server errors with exponential backoff and jitter.

Use `fetch.WithMaxArtifactSize(bytes)` to reject oversized declared bodies before
reading and enforce the same limit while streaming unknown-length bodies. Errors
match `fetch.ErrArtifactTooLarge`. Bodies exactly at the limit are accepted;
detecting overflow consumes at most one extra byte. Zero or negative limits leave
downloads unlimited. The option also covers observed fetches: rejected bodies
never produce a completed observation or content digests. HEAD requests still
report the upstream size, and size rejections do not trip the circuit breaker.

### Observing artifact responses

Use `FetchObserved` when the response metadata and downloaded content digests need to be retained:

```go
artifact, err := f.FetchObserved(ctx, url)
if err != nil {
    log.Fatal(err)
}
defer artifact.Body.Close()

if _, err := io.Copy(dst, artifact.Body); err != nil {
    log.Fatal(err)
}
if !artifact.Observation.Complete {
    log.Fatal("artifact body did not reach EOF")
}

sharedArtifact, err := artifact.Observation.Artifact(
    "pkg:npm/lodash@4.17.21",
    "lodash-4.17.21.tgz",
)
if err != nil {
    log.Fatal(err)
}

fmt.Println(artifact.Observation.RequestedURL)
fmt.Println(artifact.Observation.FinalURL)
fmt.Println(artifact.Observation.ByteCount)
fmt.Println(artifact.Observation.Digests["sha256"])
fmt.Println(sharedArtifact.PURL)
fmt.Println(sharedArtifact.Digest)
```

The observation includes the time to receive the final response headers, status, declared size, media type, and an allow-list of response headers: `Accept-Ranges`, `Cache-Control`, `Content-Disposition`, `Content-Encoding`, `Content-Length`, `Content-Range`, `Digest`, `ETag`, `Expires`, and `Last-Modified`. SHA-256 and SHA-512 digests use lowercase hexadecimal encoding. Byte counts and digests remain unset until the stream reaches EOF, so a partial download cannot appear complete. Request and authentication headers are not copied into the observation.

After EOF, `FetchObservation.Artifact` converts the SHA-256 digest, byte count, and media type into an `artifacts.Artifact`. The caller supplies the package URL and filename. The conversion rejects incomplete observations and missing or malformed SHA-256 digests.

### Per-request headers

Use `FetchWithHeaders` to pass HTTP headers for a single request. This is useful when the auth token varies per request or is obtained dynamically (e.g. Docker Hub token exchange):

```go
headers := http.Header{"Authorization": {"Bearer " + token}}
artifact, err := f.FetchWithHeaders(ctx, url, headers)
```

### Authentication

For static credentials that apply to all requests matching a URL pattern, pass a function at construction time:

```go
f := fetch.NewFetcher(
    fetch.WithAuthFunc(func(url string) (string, string) {
        if strings.Contains(url, "npm.pkg.github.com") {
            return "Authorization", "Bearer " + token
        }
        return "", ""
    }),
)
```

When both `WithAuthFunc` and `FetchWithHeaders` set the same header, `WithAuthFunc` takes precedence.

### Circuit breaker

Wrap a fetcher with per-host circuit breakers to avoid hammering a failing registry. The breaker trips once 5 failures land inside its rolling 10 second failure window, then retries with exponential backoff (30s initial, 5min max). While it is open, one request per backoff interval is let through as a probe and the rest fail with `ErrUpstreamDown` without contacting the registry; a probe that succeeds closes the breaker again. Retries never give up, so a breaker recovers no matter how long the registry stayed down.

```go
f := fetch.NewFetcher()
cbf := fetch.NewCircuitBreakerFetcher(f)

// Same interface as Fetcher
artifact, err := cbf.Fetch(ctx, url)

// Check breaker states for health monitoring
states := cbf.GetBreakerState()
// map[string]string{"registry.npmjs.org": "closed", "crates.io": "open"}
```

### URL resolution

The resolver maps ecosystem/name/version to download URLs and filenames. It uses each registry's `URLBuilder` when available, and falls back to hardcoded URL patterns for common ecosystems (npm, cargo, gem, golang, hex, pub, maven, nuget). For ecosystems with dynamic URLs (like PyPI), it fetches version metadata to find the download link.

```go
resolver := fetch.NewResolver()

// Register a registry for URL building and metadata fallback
reg, _ := registries.New("cargo", "", nil)
resolver.RegisterRegistry(reg)

info, err := resolver.Resolve(ctx, "cargo", "serde", "1.0.0")
// info.URL      = "https://static.crates.io/crates/serde/serde-1.0.0.crate"
// info.Filename = "serde-1.0.0.crate"
// info.Integrity may be populated for metadata-resolved URLs
```

When a version has several files, `ResolveWithOptions` picks one by PURL qualifiers. `arch`, `distro`, `subdir` and `build` are matched against artifact metadata, and an artifact that doesn't record a qualifier isn't ruled out by it. `repository_url` and `channel` are ignored: register a registry for that URL instead.

```go
info, err := resolver.ResolveWithOptions(ctx, "conda", "zlib", "1.3.1", fetch.ResolveOptions{
	Qualifiers: map[string]string{"subdir": "osx-arm64"},
})
```

The resolver also works without a registered registry for ecosystems with predictable URL patterns:

```go
resolver := fetch.NewResolver()
info, _ := resolver.Resolve(ctx, "npm", "lodash", "4.17.21")
// info.URL = "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"
```

`WithBaseURLs` points those patterns at a mirror instead of the public host. Ecosystems left out of the map keep their defaults:

```go
resolver := fetch.NewResolver(fetch.WithBaseURLs(map[string]string{
	"npm":   "https://npm.mycompany.com",
	"cargo": "https://crates.mycompany.com/crates",
}))
```

Download URLs taken from registry metadata must use https, and their host must not resolve to a loopback, private, link-local or other internal address. `WithAddressPolicy` relaxes that with the same `safehttp.Options` as the client that downloads them, for example when a private Composer repository points at an internal GitLab:

```go
opts := safehttp.Options{AllowPrivateHosts: []string{"gitlab.internal"}}
resolver := fetch.NewResolver(fetch.WithAddressPolicy(opts))
client := safehttp.New(nil, opts)
```

## Change Feeds (`changes/`)

Some registries publish a feed of the packages that changed since a given point. `changes` reads them for npm (`replicate.npmjs.com`) and Packagist (`metadata/changes.json`), so a cache or mirror can refresh just those packages.

Feeds are stateless: the caller keeps the cursor, and moves on to the one `Changes` returns only after acting on the changes, so a failure leaves them to be read again. A `Cursor` is a string and can be stored across restarts.

```go
feed := changes.NewNPM() // or changes.NewPackagist(changes.WithURL(...))

cursor, err := feed.Start(ctx)
// later, repeatedly:
c, next, err := feed.Changes(ctx, cursor)
if err != nil {
    return err // cursor stays where it was
}
if c.All {
    // the feed lost track; treat every package as changed
}
refresh(c.Names)
cursor = next
```

Both take `changes.WithClient` for a configured `*client.Client` and `changes.WithURL` for a registry other than the public one. Packagist names the metadata files that changed (`vendor/pkg`, `vendor/pkg~dev`); a deleted package is reported as both.

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

### Limitations

The library makes direct HTTP requests to registry APIs. It doesn't read package manager config files (`.npmrc`, `.pypirc`, `pip.conf`, etc.) for registry URLs or credentials. To use a private registry, you must either:

1. Include the `repository_url` qualifier in the PURL
2. Pass the URL explicitly when creating a registry client

Authentication for private registries isn't currently supported. Unauthenticated endpoints work, but registries requiring tokens or credentials will fail.

## License

[MIT](LICENSE).
