package cran

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

const sampleDescription = `Package: ggplot2
Version: 3.4.4
Title: Create Elegant Data Visualisations Using the Grammar of Graphics
Description: A system for 'declaratively' creating graphics,
    based on "The Grammar of Graphics".
License: MIT + file LICENSE
URL: https://ggplot2.tidyverse.org, https://github.com/tidyverse/ggplot2
BugReports: https://github.com/tidyverse/ggplot2/issues
Depends: R (>= 3.3)
Imports: cli, glue, grDevices, grid, gtable (>= 0.1.1), isoband,
    lifecycle (> 1.0.1), MASS, mgcv, rlang (>= 1.1.0), scales (>=
    1.2.0), stats, tibble, vctrs (>= 0.5.0), withr (>= 2.5.0)
Suggests: covr, dplyr, ggplot2movies, hexbin, Hmisc, knitr, lattice,
    mapproj, maps, multcomp, munsell, nlme, profvis, quantreg,
    RColorBrewer, rgeos, rmarkdown, rpart, sf (>= 0.7-3), svglite (>=
    1.2.0.9001), testthat (>= 3.1.2), vdiffr (>= 1.0.0), xml2
Author: Hadley Wickham [aut, cre], Winston Chang [aut]
Maintainer: Hadley Wickham <hadley@posit.co>
Published: 2023-10-12
NeedsCompilation: no
`

func TestFetchPackage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/packages/ggplot2/DESCRIPTION" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(sampleDescription))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	pkg, err := reg.FetchPackage(context.Background(), "ggplot2")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "ggplot2" {
		t.Errorf("expected name 'ggplot2', got %q", pkg.Name)
	}
	if pkg.Description != "Create Elegant Data Visualisations Using the Grammar of Graphics" {
		t.Errorf("unexpected description: %q", pkg.Description)
	}
	if pkg.Repository != "https://github.com/tidyverse/ggplot2" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "MIT + file LICENSE" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
	if pkg.Homepage != "https://ggplot2.tidyverse.org" {
		t.Errorf("unexpected homepage: %q", pkg.Homepage)
	}
}

func TestFetchVersions(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/web/packages/dplyr/DESCRIPTION", func(w http.ResponseWriter, r *http.Request) {
		desc := `Package: dplyr
Version: 1.1.4
Title: A Grammar of Data Manipulation
License: MIT + file LICENSE
Published: 2023-11-17
`
		_, _ = w.Write([]byte(desc))
	})

	mux.HandleFunc("/src/contrib/Archive/dplyr/", func(w http.ResponseWriter, r *http.Request) {
		html := `<html><body>
<a href="dplyr_1.1.3.tar.gz">dplyr_1.1.3.tar.gz</a>
<a href="dplyr_1.1.2.tar.gz">dplyr_1.1.2.tar.gz</a>
<a href="dplyr_1.0.0.tar.gz">dplyr_1.0.0.tar.gz</a>
</body></html>`
		_, _ = w.Write([]byte(html))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	versions, err := reg.FetchVersions(context.Background(), "dplyr")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) < 1 {
		t.Fatalf("expected at least 1 version, got %d", len(versions))
	}

	if versions[0].Number != "1.1.4" {
		t.Errorf("expected current version '1.1.4', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time for current version")
	}
}

func TestFetchDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleDescription))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	deps, err := reg.FetchDependencies(context.Background(), "ggplot2", "3.4.4")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	// Should have Imports + Suggests (Depends only has R)
	if len(deps) < 10 {
		t.Fatalf("expected at least 10 dependencies, got %d", len(deps))
	}

	// Check that R is filtered out
	for _, d := range deps {
		if d.Name == "R" {
			t.Error("R should be filtered from dependencies")
		}
	}

	// Check scope mapping
	scopeMap := make(map[string]core.Scope)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
	}

	// Imports should be runtime
	if scopeMap["cli"] != core.Runtime {
		t.Errorf("expected runtime scope for cli, got %q", scopeMap["cli"])
	}

	// Suggests should be optional
	if scopeMap["testthat"] != core.Optional {
		t.Errorf("expected optional scope for testthat, got %q", scopeMap["testthat"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleDescription))
	}))
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	maintainers, err := reg.FetchMaintainers(context.Background(), "ggplot2")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 1 {
		t.Fatalf("expected 1 maintainer, got %d", len(maintainers))
	}

	if maintainers[0].Name != "Hadley Wickham" {
		t.Errorf("expected name 'Hadley Wickham', got %q", maintainers[0].Name)
	}
	if maintainers[0].Email != "hadley@posit.co" {
		t.Errorf("unexpected email: %q", maintainers[0].Email)
	}
}

func TestParseDescription(t *testing.T) {
	desc := parseDescription(sampleDescription)

	if desc.Package != "ggplot2" {
		t.Errorf("expected Package 'ggplot2', got %q", desc.Package)
	}
	if desc.Version != "3.4.4" {
		t.Errorf("expected Version '3.4.4', got %q", desc.Version)
	}
	if desc.NeedsCompilation != "no" {
		t.Errorf("expected NeedsCompilation 'no', got %q", desc.NeedsCompilation)
	}
}

func TestParseDependencyList(t *testing.T) {
	deps := parseDependencyList("R (>= 3.3), cli, glue, scales (>= 1.2.0)", core.Runtime)

	// R should be filtered
	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies (R filtered), got %d", len(deps))
	}

	reqMap := make(map[string]string)
	for _, d := range deps {
		reqMap[d.Name] = d.Requirements
	}

	if reqMap["cli"] != "" {
		t.Errorf("expected no requirement for cli, got %q", reqMap["cli"])
	}
	if reqMap["scales"] != ">= 1.2.0" {
		t.Errorf("expected '>= 1.2.0' for scales, got %q", reqMap["scales"])
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://cran.r-project.org", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("ggplot2", "3.4.4") }, "https://cran.r-project.org/web/packages/ggplot2/index.html"},
		{"download", func() string { return urls.Download("ggplot2", "3.4.4") }, "https://cran.r-project.org/src/contrib/ggplot2_3.4.4.tar.gz"},
		{"documentation", func() string { return urls.Documentation("ggplot2", "") }, "https://cran.r-project.org/web/packages/ggplot2/ggplot2.pdf"},
		{"purl", func() string { return urls.PURL("ggplot2", "3.4.4") }, "pkg:cran/ggplot2@3.4.4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestEcosystem(t *testing.T) {
	reg := New("", nil)
	if reg.Ecosystem() != "cran" {
		t.Errorf("expected ecosystem 'cran', got %q", reg.Ecosystem())
	}
}
