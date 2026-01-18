# git-pkgs Integration

This library provides the registry lookup layer for git-pkgs features like license compliance, outdated dependency detection, and package metadata enrichment.

## Setup

```go
import (
    "github.com/git-pkgs/registries"
    _ "github.com/git-pkgs/registries/all"
)
```

## Working with PURLs

The primary interface uses Package URLs (PURLs). If you have PURLs from manifest parsing or SBOM data, you can fetch metadata directly.

### Fetch Package Info

```go
pkg, err := registries.FetchPackageFromPURL(ctx, "pkg:npm/lodash@4.17.21", nil)
if err != nil {
    return err
}
fmt.Println(pkg.Licenses)    // MIT
fmt.Println(pkg.Repository)  // https://github.com/lodash/lodash
```

### Fetch Version Details

```go
version, err := registries.FetchVersionFromPURL(ctx, "pkg:cargo/serde@1.0.0", nil)
if err != nil {
    return err
}
fmt.Println(version.PublishedAt)
fmt.Println(version.Status) // "", "yanked", "deprecated", "retracted"
```

### Fetch Dependencies

```go
deps, err := registries.FetchDependenciesFromPURL(ctx, "pkg:npm/express@4.19.0", nil)
if err != nil {
    return err
}
for _, d := range deps {
    fmt.Printf("%s %s (%s)\n", d.Name, d.Requirements, d.Scope)
}
```

### Parse PURL Components

```go
purl, err := registries.ParsePURL("pkg:maven/org.apache.commons/commons-lang3@3.12.0")
if err != nil {
    return err
}
fmt.Println(purl.Type)       // maven
fmt.Println(purl.FullName()) // org.apache.commons:commons-lang3
fmt.Println(purl.Version)    // 3.12.0
```

### Get Registry Client from PURL

```go
reg, name, version, err := registries.NewFromPURL("pkg:pypi/requests@2.31.0", nil)
if err != nil {
    return err
}
// reg is a pypi Registry
// name is "requests"
// version is "2.31.0"

// Use the registry directly for additional operations
maintainers, _ := reg.FetchMaintainers(ctx, name)
```

## License Compliance

Look up licenses for a list of PURLs:

```go
func FetchLicenses(ctx context.Context, purls []string) (map[string]string, error) {
    licenses := make(map[string]string)

    for _, purl := range purls {
        pkg, err := registries.FetchPackageFromPURL(ctx, purl, nil)
        if err != nil {
            var notFound *registries.NotFoundError
            if errors.As(err, &notFound) {
                continue // skip packages not in registry
            }
            return nil, err
        }
        licenses[purl] = pkg.Licenses
    }
    return licenses, nil
}
```

Some ecosystems store licenses per-version. For these, check the version:

```go
func FetchVersionLicense(ctx context.Context, purl string) (string, error) {
    version, err := registries.FetchVersionFromPURL(ctx, purl, nil)
    if err != nil {
        return "", err
    }
    return version.Licenses, nil
}
```

Ecosystems with version-level licenses: Cargo, npm, NuGet, Hex, Pub.

## Outdated Detection

Compare versions in PURLs against latest available:

```go
type OutdatedPackage struct {
    PURL    string
    Current string
    Latest  string
}

func FindOutdated(ctx context.Context, purls []string) ([]OutdatedPackage, error) {
    var outdated []OutdatedPackage

    for _, purl := range purls {
        p, err := registries.ParsePURL(purl)
        if err != nil {
            continue
        }

        if p.Version == "" {
            continue // no version to compare
        }

        reg, name, _, err := registries.NewFromPURL(purl, nil)
        if err != nil {
            continue
        }

        versions, err := reg.FetchVersions(ctx, name)
        if err != nil || len(versions) == 0 {
            continue
        }

        // Find latest non-yanked version
        var latest string
        for _, v := range versions {
            if v.Status == "" {
                latest = v.Number
                break
            }
        }

        if latest != "" && p.Version != latest {
            outdated = append(outdated, OutdatedPackage{
                PURL:    purl,
                Current: p.Version,
                Latest:  latest,
            })
        }
    }
    return outdated, nil
}
```

## Repository URLs

Get source repository URLs from PURLs:

```go
func FetchRepositories(ctx context.Context, purls []string) (map[string]string, error) {
    repos := make(map[string]string)

    for _, purl := range purls {
        pkg, err := registries.FetchPackageFromPURL(ctx, purl, nil)
        if err != nil {
            continue
        }
        if pkg.Repository != "" {
            repos[purl] = pkg.Repository
        }
    }
    return repos, nil
}
```

## Batch Fetching

For large PURL lists, fetch in parallel:

```go
func FetchPackagesBatch(ctx context.Context, purls []string, concurrency int) map[string]*registries.Package {
    results := make(map[string]*registries.Package)
    var mu sync.Mutex
    sem := make(chan struct{}, concurrency)
    var wg sync.WaitGroup

    for _, purl := range purls {
        wg.Add(1)
        go func(p string) {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()

            pkg, err := registries.FetchPackageFromPURL(ctx, p, nil)
            if err == nil {
                mu.Lock()
                results[p] = pkg
                mu.Unlock()
            }
        }(purl)
    }
    wg.Wait()
    return results
}
```

## Caching

The HTTP client doesn't cache responses. For repeated lookups, add a caching layer:

```go
type CachedClient struct {
    cache *lru.Cache // or sync.Map, redis, etc.
    ttl   time.Duration
}

func (c *CachedClient) FetchPackage(ctx context.Context, purl string) (*registries.Package, error) {
    if cached, ok := c.cache.Get(purl); ok {
        return cached.(*registries.Package), nil
    }

    pkg, err := registries.FetchPackageFromPURL(ctx, purl, nil)
    if err != nil {
        return nil, err
    }

    c.cache.Add(purl, pkg)
    return pkg, nil
}
```

## Error Handling

```go
func FetchWithFallback(ctx context.Context, purl string) (*registries.Package, error) {
    pkg, err := registries.FetchPackageFromPURL(ctx, purl, nil)
    if err != nil {
        var notFound *registries.NotFoundError
        if errors.As(err, &notFound) {
            return nil, nil // Package doesn't exist
        }

        var httpErr *registries.HTTPError
        if errors.As(err, &httpErr) {
            if httpErr.StatusCode == 429 {
                return nil, fmt.Errorf("rate limited")
            }
        }

        return nil, err
    }
    return pkg, nil
}
```
