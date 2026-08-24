package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSelectLatestVersion(t *testing.T) {
	older := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(24 * time.Hour)

	tests := []struct {
		name       string
		versions   []Version
		ecosystem  string
		advertised string
		want       string
	}{
		{
			name: "advertised version wins",
			versions: []Version{
				{Number: "1.0.0", PublishedAt: older},
				{Number: "2.0.0-beta.1", PublishedAt: newer},
			},
			ecosystem:  "npm",
			advertised: "1.0.0",
			want:       "1.0.0",
		},
		{
			name: "advertised scheme equivalent version returns metadata",
			versions: []Version{
				{Number: "1.0.0", Licenses: "MIT"},
			},
			ecosystem:  "pypi",
			advertised: "1.0",
			want:       "1.0.0",
		},
		{
			name: "advertised version wins regardless of status",
			versions: []Version{
				{Number: "1.0.0", Status: StatusDeprecated},
				{Number: "2.0.0"},
			},
			ecosystem:  "npm",
			advertised: "1.0.0",
			want:       "1.0.0",
		},
		{
			name:       "advertised version missing from list",
			versions:   []Version{{Number: "1.0.0"}},
			ecosystem:  "npm",
			advertised: "2.0.0",
			want:       "2.0.0",
		},
		{
			name: "newest publication wins",
			versions: []Version{
				{Number: "2.0.0", PublishedAt: older},
				{Number: "1.9.0", PublishedAt: newer},
			},
			ecosystem: "npm",
			want:      "1.9.0",
		},
		{
			name: "version ordering breaks timestamp ties",
			versions: []Version{
				{Number: "1.9.0", PublishedAt: newer},
				{Number: "2.0.0", PublishedAt: newer},
			},
			ecosystem: "npm",
			want:      "2.0.0",
		},
		{
			name: "ecosystem ordering without timestamps",
			versions: []Version{
				{Number: "1.9.0"},
				{Number: "1.10.0"},
			},
			ecosystem: "gem",
			want:      "1.10.0",
		},
		{
			name: "dated versions take precedence over undated versions",
			versions: []Version{
				{Number: "2.0.0"},
				{Number: "1.0.0", PublishedAt: older},
			},
			ecosystem: "npm",
			want:      "1.0.0",
		},
		{
			name: "prerelease is eligible without advertised version",
			versions: []Version{
				{Number: "1.9.0"},
				{Number: "2.0.0-beta.1"},
			},
			ecosystem: "npm",
			want:      "2.0.0-beta.1",
		},
		{
			name: "inactive and empty versions are excluded",
			versions: []Version{
				{Number: "", PublishedAt: newer},
				{Number: "4.0.0", Status: StatusYanked},
				{Number: "3.0.0", Status: StatusDeprecated},
				{Number: "2.0.0", Status: StatusRetracted},
				{Number: "1.0.0"},
			},
			ecosystem: "npm",
			want:      "1.0.0",
		},
		{
			name: "no active versions",
			versions: []Version{
				{Number: "2.0.0", Status: StatusYanked},
				{Number: "1.0.0", Status: StatusRetracted},
			},
			ecosystem: "npm",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := append([]Version(nil), test.versions...)
			got := SelectLatestVersion(test.versions, test.ecosystem, test.advertised)
			if test.want == "" {
				if got != nil {
					t.Fatalf("SelectLatestVersion() = %+v, want nil", got)
				}
			} else if got == nil || got.Number != test.want {
				t.Fatalf("SelectLatestVersion() = %+v, want number %q", got, test.want)
			}
			if !reflect.DeepEqual(test.versions, before) {
				t.Errorf("SelectLatestVersion() modified its input")
			}
		})
	}
}

type latestRegistry struct {
	ecosystem  string
	pkg        *Package
	packageErr error
	versions   []Version
	versionErr error
}

func (r *latestRegistry) Ecosystem() string { return r.ecosystem }

func (r *latestRegistry) FetchPackage(context.Context, string) (*Package, error) {
	return r.pkg, r.packageErr
}

func (r *latestRegistry) FetchVersions(context.Context, string) ([]Version, error) {
	return r.versions, r.versionErr
}

func (r *latestRegistry) FetchDependencies(context.Context, string, string) ([]Dependency, error) {
	return nil, nil
}

func (r *latestRegistry) FetchMaintainers(context.Context, string) ([]Maintainer, error) {
	return nil, nil
}

func (r *latestRegistry) URLs() URLBuilder { //nolint:ireturn // Registry requires this interface return type.
	return &BaseURLs{}
}

func TestFetchLatestVersion(t *testing.T) {
	t.Run("uses advertised latest", func(t *testing.T) {
		reg := &latestRegistry{
			ecosystem: "npm",
			pkg:       &Package{LatestVersion: "1.0.0"},
			versions: []Version{
				{Number: "1.0.0", Licenses: "MIT"},
				{Number: "2.0.0-beta.1"},
			},
		}
		got, err := FetchLatestVersion(context.Background(), reg, "example")
		if err != nil {
			t.Fatalf("FetchLatestVersion: %v", err)
		}
		if got == nil || got.Number != "1.0.0" || got.Licenses != "MIT" {
			t.Errorf("FetchLatestVersion() = %+v, want advertised version metadata", got)
		}
	})

	t.Run("falls back when package metadata fails", func(t *testing.T) {
		reg := &latestRegistry{
			ecosystem:  "npm",
			packageErr: errors.New("package endpoint unavailable"),
			versions:   []Version{{Number: "1.0.0"}, {Number: "2.0.0"}},
		}
		got, err := FetchLatestVersion(context.Background(), reg, "example")
		if err != nil {
			t.Fatalf("FetchLatestVersion: %v", err)
		}
		if got == nil || got.Number != "2.0.0" {
			t.Errorf("FetchLatestVersion() = %+v, want version-order fallback", got)
		}
	})

	t.Run("uses advertised version when versions fail", func(t *testing.T) {
		reg := &latestRegistry{
			ecosystem:  "npm",
			pkg:        &Package{LatestVersion: "1.0.0"},
			versionErr: errors.New("versions endpoint unavailable"),
		}
		got, err := FetchLatestVersion(context.Background(), reg, "example")
		if err != nil {
			t.Fatalf("FetchLatestVersion: %v", err)
		}
		if got == nil || got.Number != "1.0.0" {
			t.Errorf("FetchLatestVersion() = %+v, want advertised version", got)
		}
	})
}
