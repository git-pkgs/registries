# Contributing

## Adding a New Ecosystem

Each ecosystem lives in its own package under `internal/`. Use Cargo as a template since it has the cleanest API.

### 1. Create the Package

```
internal/
└── neweco/
    ├── neweco.go
    └── neweco_test.go
```

### 2. Implement the Registry Interface

```go
package neweco

import (
    "context"
    "github.com/git-pkgs/registries/internal/core"
)

const (
    DefaultURL = "https://registry.example.com"
    ecosystem  = "neweco"  // Use the PURL type as the ecosystem name
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

func (r *Registry) FetchPackage(ctx context.Context, name string) (*core.Package, error) {
    // Fetch from API
    // Return NotFoundError for 404s
}

func (r *Registry) FetchVersions(ctx context.Context, name string) ([]core.Version, error) {
    // ...
}

func (r *Registry) FetchDependencies(ctx context.Context, name, version string) ([]core.Dependency, error) {
    // ...
}

func (r *Registry) FetchMaintainers(ctx context.Context, name string) ([]core.Maintainer, error) {
    // Return nil if the registry doesn't expose maintainers
    return nil, nil
}
```

### 3. Implement URLBuilder

```go
type URLs struct {
    baseURL string
}

func (u *URLs) Registry(name, version string) string {
    // Human-readable URL for the package page
}

func (u *URLs) Download(name, version string) string {
    // Direct download URL for the package archive
    // Return "" if version is empty
}

func (u *URLs) Documentation(name, version string) string {
    // Documentation URL
}

func (u *URLs) PURL(name, version string) string {
    // Package URL per https://github.com/package-url/purl-spec
    // Format: pkg:type/namespace/name@version
}
```

### 4. Handle Errors

Use the core error types:

```go
if httpErr, ok := err.(*core.HTTPError); ok && httpErr.IsNotFound() {
    return nil, &core.NotFoundError{Ecosystem: ecosystem, Name: name}
}
```

### 5. Map Dependency Scopes

Use the standard scope constants:

```go
// core.Runtime     - production dependencies
// core.Development - dev dependencies
// core.Test        - test dependencies
// core.Build       - build-time dependencies
// core.Optional    - optional/peer dependencies
```

### 6. Write Tests

Use httptest to mock the registry API:

```go
func TestFetchPackage(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "name": "example",
            // ...
        }
        json.NewEncoder(w).Encode(resp)
    }))
    defer server.Close()

    reg := New(server.URL, core.DefaultClient())
    pkg, err := reg.FetchPackage(context.Background(), "example")
    // assertions
}
```

Test at minimum:
- FetchPackage success
- FetchPackage not found
- FetchVersions
- FetchDependencies with different scopes
- URLBuilder methods

### 7. Add to all/all.go

```go
import (
    _ "github.com/git-pkgs/registries/internal/cargo"
    _ "github.com/git-pkgs/registries/internal/neweco"  // Add this
    // ...
)
```

### 8. Update registries_test.go

Add the new ecosystem to the test cases:

```go
func TestSupportedEcosystems(t *testing.T) {
    ecosystems := registries.SupportedEcosystems()
    expected := []string{"cargo", "gem", "golang", "neweco", "npm", "pypi"}
    // ...
}

func TestDefaultURL(t *testing.T) {
    tests := []struct{...}{
        // ...
        {"neweco", "https://registry.example.com"},
    }
}
```

## Guidelines

### Naming

Use the PURL type as the ecosystem identifier. Common examples:
- `cargo` not `crates` or `rust`
- `gem` not `rubygems`
- `golang` not `go`
- `npm` not `node`

### Package Struct

Only populate fields you have data for:

```go
return &core.Package{
    Name:        resp.Name,
    Description: resp.Summary,
    Repository:  extractRepoURL(resp),  // Parse/normalize repository URLs
    Licenses:    resp.License,
    // Namespace only if the ecosystem has namespaces (npm scopes, maven groupId)
    // Keywords only if the API returns them
    Metadata: map[string]any{
        // Registry-specific fields that don't fit the common schema
        "downloads": resp.Downloads,
    },
}, nil
```

### Version Status

Map registry-specific statuses to the standard values:

```go
var status core.VersionStatus
if v.Yanked {
    status = core.StatusYanked
} else if v.Deprecated {
    status = core.StatusDeprecated
} else if v.Retracted {
    status = core.StatusRetracted
}
```

### Integrity

Prefix checksums with the algorithm:

```go
if sha256 != "" {
    integrity = "sha256-" + sha256
} else if sha1 != "" {
    integrity = "sha1-" + sha1
}
```

### HTTP Client

Use the provided client for all requests:

```go
// JSON response
var resp apiResponse
if err := r.client.GetJSON(ctx, url, &resp); err != nil {
    return nil, err
}

// Text response
body, err := r.client.GetText(ctx, url)

// Raw bytes
data, err := r.client.GetBody(ctx, url)
```

The client handles retries and timeouts.

## Running Tests

```bash
go test ./...
```

For verbose output:

```bash
go test ./... -v
```

## Reference Implementations

Look at the existing implementations for patterns:
- `internal/cargo/cargo.go` - cleanest API, good starting point
- `internal/npm/npm.go` - scoped package handling
- `internal/pypi/pypi.go` - name normalization, PEP 508 parsing
- `internal/golang/golang.go` - proxy protocol encoding
