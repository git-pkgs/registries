// Package all imports all supported registry implementations.
//
// Import this package for its side effects to register all ecosystems:
//
//	import (
//		"github.com/git-pkgs/registries"
//		_ "github.com/git-pkgs/registries/all"
//	)
//
//	// Now all ecosystems are available
//	ecosystems := registries.SupportedEcosystems()
//	// ["brew", "cargo", "clojars", "cocoapods", "composer", "conda", "cpan", "cran", "deno", "dub", "elm", "gem", "golang", "hackage", "haxelib", "helm", "hex", "julia", "luarocks", "maven", "nimble", "npm", "nuget", "pub", "pypi", "terraform"]
package all

import (
	_ "github.com/git-pkgs/registries/internal/cargo"
	_ "github.com/git-pkgs/registries/internal/clojars"
	_ "github.com/git-pkgs/registries/internal/cocoapods"
	_ "github.com/git-pkgs/registries/internal/conda"
	_ "github.com/git-pkgs/registries/internal/cpan"
	_ "github.com/git-pkgs/registries/internal/cran"
	_ "github.com/git-pkgs/registries/internal/deno"
	_ "github.com/git-pkgs/registries/internal/dub"
	_ "github.com/git-pkgs/registries/internal/elm"
	_ "github.com/git-pkgs/registries/internal/golang"
	_ "github.com/git-pkgs/registries/internal/hackage"
	_ "github.com/git-pkgs/registries/internal/haxelib"
	_ "github.com/git-pkgs/registries/internal/helm"
	_ "github.com/git-pkgs/registries/internal/hex"
	_ "github.com/git-pkgs/registries/internal/homebrew"
	_ "github.com/git-pkgs/registries/internal/julia"
	_ "github.com/git-pkgs/registries/internal/luarocks"
	_ "github.com/git-pkgs/registries/internal/maven"
	_ "github.com/git-pkgs/registries/internal/nimble"
	_ "github.com/git-pkgs/registries/internal/npm"
	_ "github.com/git-pkgs/registries/internal/nuget"
	_ "github.com/git-pkgs/registries/internal/packagist"
	_ "github.com/git-pkgs/registries/internal/pub"
	_ "github.com/git-pkgs/registries/internal/pypi"
	_ "github.com/git-pkgs/registries/internal/rubygems"
	_ "github.com/git-pkgs/registries/internal/terraform"
)
