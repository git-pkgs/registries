# Ecosystem-Specific Notes

Each registry has unique API patterns and data formats. This documents the quirks.

## npm

**API:** `https://registry.npmjs.org/{name}`

**Scoped Packages:** Names like `@babel/core` use URL encoding: `@babel%2Fcore`

```go
func encodePackageName(name string) string {
    if strings.HasPrefix(name, "@") {
        return strings.Replace(name, "/", "%2F", 1)
    }
    return name
}
```

**Single Endpoint:** One request returns package info, all versions, and dependencies. No need for separate version fetches.

**Timestamps:** Version publish times are in the `time` object, keyed by version number.

## PyPI

**API:** `https://pypi.org/pypi/{name}/json`

**Name Normalization:** PEP 503 requires normalizing names:
- Lowercase
- Replace `_`, `.`, `-` with `-`
- Collapse consecutive `-`

```go
func normalizeName(name string) string {
    name = strings.ToLower(name)
    name = strings.Map(func(r rune) rune {
        if r == '_' || r == '.' {
            return '-'
        }
        return r
    }, name)
    // Collapse consecutive dashes
    for strings.Contains(name, "--") {
        name = strings.ReplaceAll(name, "--", "-")
    }
    return name
}
```

`typing-extensions`, `typing_extensions`, and `Typing-Extensions` all resolve to the same package.

**Classifiers:** License info may be in classifiers array rather than `license` field.

## Cargo

**API:** `https://crates.io/api/v1/crates/{name}`

**Clean API:** Returns structured JSON with crate info and versions array.

**Dependencies:** Requires separate request to `/versions/{id}/dependencies`.

**Yanked Versions:** Indicated by `yanked: true` in version object.

## Go

**API:** `https://proxy.golang.org/{module}/@v/list`

**Module Path Encoding:** Capital letters become `!` + lowercase:

```go
func encodePath(path string) string {
    var buf strings.Builder
    for _, r := range path {
        if 'A' <= r && r <= 'Z' {
            buf.WriteByte('!')
            buf.WriteRune(r + 32) // lowercase
        } else {
            buf.WriteRune(r)
        }
    }
    return buf.String()
}
```

`github.com/Azure/go-sdk` becomes `github.com/!azure/go-sdk`

**Module Info:** Fetch `/@v/{version}.info` for timestamp, `/@v/{version}.mod` for dependencies.

**Retracted Versions:** Indicated in go.mod with `retract` directive.

## Maven

**API:** `https://repo1.maven.org/maven2/{groupPath}/{artifactId}/maven-metadata.xml`

**Group Path:** Replace `.` with `/` in groupId: `org.apache.commons` → `org/apache/commons`

**Versions:** Listed in `maven-metadata.xml` of the configured repository, without publish dates. `search.maven.org` is not used, since it only knows Central and rate limits.

**POM Parsing:** Must parse XML POM files for metadata and dependencies.

**Parent POMs:** Dependencies may inherit from parent POMs, requiring recursive resolution.

**Version Ranges:** Maven uses complex version range syntax: `[1.0,2.0)`, `[1.0,]`

## NuGet

**API:** `https://api.nuget.org/v3/registration5-gz-semver2/{name}/index.json`

**Service Index:** Must first fetch service index to discover endpoints.

**Case Insensitive:** Package names are case-insensitive but preserve original casing.

**Compressed Responses:** Uses gzip compression by default.

## RubyGems

**API:** `https://rubygems.org/api/v1/gems/{name}.json`

**Versions:** Separate endpoint at `/api/v1/versions/{name}.json`

**Dependencies:** Returns runtime and development dependencies separately.

## Hex

**API:** `https://hex.pm/api/packages/{name}`

**Erlang/Elixir:** Serves both Erlang and Elixir ecosystems.

**Releases:** Version info nested in `releases` array with download URLs.

## Pub

**API:** `https://pub.dev/api/packages/{name}`

**Flutter/Dart:** Serves both Flutter and Dart packages.

**Versions:** Listed in `versions` array with `pubspec` containing dependencies.

## CocoaPods

**API:** `https://trunk.cocoapods.org/api/v1/pods/{name}`

**Spec Format:** Pod specs can have dependencies in various formats:
- String: `"AFNetworking"`
- Array: `["AFNetworking", ">= 2.0"]`
- Hash: `{"AFNetworking": ">= 2.0"}`

**License:** Can be string or object with `type` field.

## CRAN

**API:** No REST API. Fetch DESCRIPTION files directly.

**URL:** `https://cran.r-project.org/web/packages/{name}/DESCRIPTION`

**DCF Format:** Debian Control File format with continuation lines:

```
Package: ggplot2
Version: 3.4.4
Description: A system for 'declaratively' creating graphics,
    based on "The Grammar of Graphics".
```

**Archived Versions:** Listed in HTML directory at `/src/contrib/Archive/{name}/`

## Conda

**API:** `https://api.anaconda.org/package/{channel}/{name}`

**Channels:** Default is `conda-forge`. Can specify channel in name: `bioconda/samtools`

**Multiple Files:** Each version may have multiple files for different platforms/Python versions.

## Julia

**API:** No REST API. Fetch TOML files from GitHub.

**Registry:** `https://raw.githubusercontent.com/JuliaRegistries/General/master/{letter}/{name}/`

**Files:**
- `Package.toml` - name, uuid, repo
- `Versions.toml` - version → git-tree-sha1
- `Deps.toml` - version → dependencies

## Elm

**API:** `https://package.elm-lang.org/packages/{author}/{name}/releases.json`

**Author/Name:** All packages are namespaced: `elm/json`, `elm-community/list-extra`

**elm.json:** Per-version metadata at `/packages/{author}/{name}/{version}/elm.json`

## Clojars

**API:** `https://clojars.org/api/artifacts/{group}/{name}`

**Maven Coordinates:** Uses group/artifact pattern. Single-segment names use name as both.

**Versions:** Listed in `recent_versions` array.

## CPAN

**API:** `https://fastapi.metacpan.org/v1/release/{distribution}`

**Distribution Names:** Use `-` not `::`: `Moose-2.2201` not `Moose::2.2201`

**Author:** Maintainer info via `/author/{pauseid}` endpoint.

## Hackage

**API:** No REST API. Fetch Cabal files.

**URL:** `https://hackage.haskell.org/package/{name}/{name}.cabal`

**Cabal Format:** Custom format with `build-depends` for dependencies.

## Dub (D)

**API:** `https://code.dlang.org/api/packages/{name}`

**Dependencies:** Can be string constraints or objects with version field.

## LuaRocks

**API:** `https://luarocks.org/api/1/{name}`

**Rockspec:** Dependencies in `dependencies` array as strings: `"lua >= 5.1"`

## Nimble

**API:** `https://nimble.directory/api/packages/{name}`

**Git-based:** Most packages installed from Git, versions list available releases.

## Haxelib

**API:** `https://lib.haxe.org/api/3.0/package-info/{name}`

**Versions:** Array with version objects containing dependencies map.

## Homebrew

**API:** `https://formulae.brew.sh/api/formula/{name}.json`

**Casks:** Separate endpoint at `/api/cask/{name}.json` for GUI apps.

**Dependencies:** Multiple types:
- `dependencies` - runtime
- `build_dependencies` - build time only
- `test_dependencies` - test only
- `optional_dependencies` - optional
- `recommended_dependencies` - recommended

**Versions:** Only latest version available via API. Historical versions in Git.

## Deno

**API:** `https://apiland.deno.dev/v2/modules/{name}`

**URL Imports:** Deno uses URL imports rather than a manifest. Dependencies determined by analyzing source.

**GitHub Linked:** Most modules linked to GitHub, repository info in `upload_options`.

**Versions:** Listed directly in module info response.

## Terraform

**API:** `https://registry.terraform.io/v1/modules/{namespace}/{name}/{provider}`

**Module Names:** Three-part format: `namespace/name/provider` (e.g., `hashicorp/consul/aws`)

**Versions:** Fetch via `/versions` endpoint. Modules list in response may contain multiple entries.

**Dependencies:** Two types in version detail:
- `root.dependencies` - module dependencies
- `root.providers` - required providers with version constraints
