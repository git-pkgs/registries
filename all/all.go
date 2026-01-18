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
//	// ["cargo", "gem", "golang", "npm", "pypi"]
package all

import (
	_ "github.com/git-pkgs/registries/internal/cargo"
	_ "github.com/git-pkgs/registries/internal/golang"
	_ "github.com/git-pkgs/registries/internal/npm"
	_ "github.com/git-pkgs/registries/internal/pypi"
	_ "github.com/git-pkgs/registries/internal/rubygems"
)
