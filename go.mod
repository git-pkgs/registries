module github.com/git-pkgs/registries

go 1.25.6

require (
	github.com/cenk/backoff v2.2.1+incompatible
	github.com/git-pkgs/purl v0.1.8
	github.com/git-pkgs/spdx v0.1.0
	github.com/rs/dnscache v0.0.0-20230804202142-fc85eb664529
	github.com/rubyist/circuitbreaker v2.2.1+incompatible
)

require (
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/git-pkgs/packageurl-go v0.2.1 // indirect
	github.com/git-pkgs/vers v0.2.2 // indirect
	github.com/github/go-spdx/v2 v2.3.6 // indirect
	github.com/peterbourgon/g2s v0.0.0-20170223122336-d4e7ad98afea // indirect
	golang.org/x/sync v0.0.0-20190423024810-112230192c58 // indirect
)

replace github.com/package-url/packageurl-go => github.com/git-pkgs/packageurl-go v0.0.0-20260115093137-a0c26f7ee19e
