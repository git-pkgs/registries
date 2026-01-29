package maven

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/git-pkgs/registries/internal/core"
)

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		input     string
		groupID   string
		artifactID string
		version   string
	}{
		{"com.google.guava:guava", "com.google.guava", "guava", ""},
		{"com.google.guava:guava:32.1.0", "com.google.guava", "guava", "32.1.0"},
		{"org.apache:commons-lang3:3.12.0", "org.apache", "commons-lang3", "3.12.0"},
		{"invalid", "", "", ""},
	}

	for _, tt := range tests {
		g, a, v := ParseCoordinates(tt.input)
		if g != tt.groupID || a != tt.artifactID || v != tt.version {
			t.Errorf("ParseCoordinates(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.input, g, a, v, tt.groupID, tt.artifactID, tt.version)
		}
	}
}

func TestFetchPackage(t *testing.T) {
	mux := http.NewServeMux()

	// Search API endpoint
	mux.HandleFunc("/solrsearch/select", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Response: searchResponseBody{
				NumFound: 1,
				Docs: []searchDoc{
					{
						ID:         "com.google.guava:guava",
						GroupID:    "com.google.guava",
						ArtifactID: "guava",
						Version:    "32.1.0-jre",
						VersionCount: 150,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// POM endpoint
	mux.HandleFunc("/com/google/guava/guava/32.1.0-jre/guava-32.1.0-jre.pom", func(w http.ResponseWriter, r *http.Request) {
		pom := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <groupId>com.google.guava</groupId>
  <artifactId>guava</artifactId>
  <version>32.1.0-jre</version>
  <name>Guava: Google Core Libraries for Java</name>
  <description>Guava is a suite of core and expanded libraries.</description>
  <url>https://github.com/google/guava</url>
  <licenses>
    <license>
      <name>Apache License, Version 2.0</name>
    </license>
  </licenses>
  <scm>
    <url>https://github.com/google/guava</url>
  </scm>
</project>`
		_, _ = w.Write([]byte(pom))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	reg.searchURL = server.URL

	pkg, err := reg.FetchPackage(context.Background(), "com.google.guava:guava")
	if err != nil {
		t.Fatalf("FetchPackage failed: %v", err)
	}

	if pkg.Name != "com.google.guava:guava" {
		t.Errorf("expected name 'com.google.guava:guava', got %q", pkg.Name)
	}
	if pkg.Namespace != "com.google.guava" {
		t.Errorf("expected namespace 'com.google.guava', got %q", pkg.Namespace)
	}
	if pkg.Repository != "https://github.com/google/guava" {
		t.Errorf("unexpected repository: %q", pkg.Repository)
	}
	if pkg.Licenses != "Apache License, Version 2.0" {
		t.Errorf("unexpected licenses: %q", pkg.Licenses)
	}
}

func TestFetchVersions(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/solrsearch/select", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Response: searchResponseBody{
				NumFound: 3,
				Docs: []searchDoc{
					{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.14.0", Timestamp: 1699900000000},
					{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.13.0", Timestamp: 1689100000000},
					{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.12.0", Timestamp: 1678300000000},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	reg.searchURL = server.URL

	versions, err := reg.FetchVersions(context.Background(), "org.apache.commons:commons-lang3")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	if versions[0].Number != "3.14.0" {
		t.Errorf("expected version '3.14.0', got %q", versions[0].Number)
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero published time")
	}
}

func TestFetchVersionsFallback(t *testing.T) {
	mux := http.NewServeMux()

	// Search API returns empty
	mux.HandleFunc("/solrsearch/select", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{Response: searchResponseBody{NumFound: 0}}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Fallback to maven-metadata.xml
	mux.HandleFunc("/com/example/test/maven-metadata.xml", func(w http.ResponseWriter, r *http.Request) {
		metadata := `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <versioning>
    <latest>2.0.0</latest>
    <versions>
      <version>1.0.0</version>
      <version>1.5.0</version>
      <version>2.0.0</version>
    </versions>
  </versioning>
</metadata>`
		_, _ = w.Write([]byte(metadata))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	reg.searchURL = server.URL

	versions, err := reg.FetchVersions(context.Background(), "com.example:test")
	if err != nil {
		t.Fatalf("FetchVersions failed: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
}

func TestFetchDependencies(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.pom", func(w http.ResponseWriter, r *http.Request) {
		pom := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <groupId>org.slf4j</groupId>
  <artifactId>slf4j-api</artifactId>
  <version>2.0.9</version>
  <dependencies>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-simple</artifactId>
      <version>2.0.9</version>
      <scope>test</scope>
    </dependency>
    <dependency>
      <groupId>ch.qos.logback</groupId>
      <artifactId>logback-classic</artifactId>
      <version>1.4.11</version>
      <optional>true</optional>
    </dependency>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>3.12.0</version>
    </dependency>
  </dependencies>
</project>`
		_, _ = w.Write([]byte(pom))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())

	deps, err := reg.FetchDependencies(context.Background(), "org.slf4j:slf4j-api", "2.0.9")
	if err != nil {
		t.Fatalf("FetchDependencies failed: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}

	scopeMap := make(map[string]core.Scope)
	optMap := make(map[string]bool)
	for _, d := range deps {
		scopeMap[d.Name] = d.Scope
		optMap[d.Name] = d.Optional
	}

	if scopeMap["org.slf4j:slf4j-simple"] != core.Test {
		t.Errorf("expected test scope for slf4j-simple, got %q", scopeMap["org.slf4j:slf4j-simple"])
	}
	if scopeMap["ch.qos.logback:logback-classic"] != core.Optional {
		t.Errorf("expected optional scope for logback-classic, got %q", scopeMap["ch.qos.logback:logback-classic"])
	}
	if !optMap["ch.qos.logback:logback-classic"] {
		t.Error("expected logback-classic to be optional")
	}
	if scopeMap["org.apache.commons:commons-lang3"] != core.Runtime {
		t.Errorf("expected runtime scope for commons-lang3, got %q", scopeMap["org.apache.commons:commons-lang3"])
	}
}

func TestFetchMaintainers(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/solrsearch/select", func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Response: searchResponseBody{
				NumFound: 1,
				Docs: []searchDoc{
					{GroupID: "com.example", ArtifactID: "test", Version: "1.0.0"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/com/example/test/1.0.0/test-1.0.0.pom", func(w http.ResponseWriter, r *http.Request) {
		pom := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
  <developers>
    <developer>
      <id>jdoe</id>
      <name>John Doe</name>
      <email>john@example.com</email>
    </developer>
    <developer>
      <id>jsmith</id>
      <name>Jane Smith</name>
      <email>jane@example.com</email>
    </developer>
  </developers>
</project>`
		_, _ = w.Write([]byte(pom))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())
	reg.searchURL = server.URL

	maintainers, err := reg.FetchMaintainers(context.Background(), "com.example:test")
	if err != nil {
		t.Fatalf("FetchMaintainers failed: %v", err)
	}

	if len(maintainers) != 2 {
		t.Fatalf("expected 2 maintainers, got %d", len(maintainers))
	}

	if maintainers[0].Login != "jdoe" {
		t.Errorf("expected login 'jdoe', got %q", maintainers[0].Login)
	}
	if maintainers[0].Name != "John Doe" {
		t.Errorf("expected name 'John Doe', got %q", maintainers[0].Name)
	}
}

func TestParentPOMResolution(t *testing.T) {
	mux := http.NewServeMux()

	// Child POM
	mux.HandleFunc("/com/example/child/1.0.0/child-1.0.0.pom", func(w http.ResponseWriter, r *http.Request) {
		pom := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>child</artifactId>
  <name>Child Project</name>
</project>`
		_, _ = w.Write([]byte(pom))
	})

	// Parent POM
	mux.HandleFunc("/com/example/parent/1.0.0/parent-1.0.0.pom", func(w http.ResponseWriter, r *http.Request) {
		pom := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <description>Parent project description</description>
  <url>https://example.com</url>
  <licenses>
    <license>
      <name>MIT</name>
    </license>
  </licenses>
  <scm>
    <url>https://github.com/example/parent</url>
  </scm>
</project>`
		_, _ = w.Write([]byte(pom))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	reg := New(server.URL, core.DefaultClient())

	pom, err := reg.fetchPOM(context.Background(), "com.example", "child", "1.0.0", 0)
	if err != nil {
		t.Fatalf("fetchPOM failed: %v", err)
	}

	// Should inherit from parent
	if pom.Description != "Parent project description" {
		t.Errorf("expected inherited description, got %q", pom.Description)
	}
	if pom.GroupID != "com.example" {
		t.Errorf("expected groupId from parent, got %q", pom.GroupID)
	}
	if len(pom.Licenses) != 1 || pom.Licenses[0].Name != "MIT" {
		t.Errorf("expected inherited license, got %v", pom.Licenses)
	}
}

func TestURLBuilder(t *testing.T) {
	reg := New("https://repo1.maven.org/maven2", nil)
	urls := reg.URLs()

	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"registry", func() string { return urls.Registry("com.google.guava:guava", "32.1.0") }, "https://search.maven.org/artifact/com.google.guava/guava/32.1.0/jar"},
		{"download", func() string { return urls.Download("com.google.guava:guava", "32.1.0") }, "https://repo1.maven.org/maven2/com/google/guava/guava/32.1.0/guava-32.1.0.jar"},
		{"documentation", func() string { return urls.Documentation("com.google.guava:guava", "32.1.0") }, "https://javadoc.io/doc/com.google.guava/guava/32.1.0"},
		{"purl", func() string { return urls.PURL("com.google.guava:guava", "32.1.0") }, "pkg:maven/com.google.guava/guava@32.1.0"},
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
	if reg.Ecosystem() != "maven" {
		t.Errorf("expected ecosystem 'maven', got %q", reg.Ecosystem())
	}
}
