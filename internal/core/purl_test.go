package core

import (
	"testing"
)

func TestParsePURL(t *testing.T) {
	tests := []struct {
		input    string
		wantType string
		wantNS   string
		wantName string
		wantVer  string
		wantFull string
		wantErr  bool
	}{
		// Basic package without version
		{"pkg:cargo/serde", "cargo", "", "serde", "", "serde", false},
		{"pkg:npm/lodash", "npm", "", "lodash", "", "lodash", false},
		{"pkg:pypi/requests", "pypi", "", "requests", "", "requests", false},

		// Package with version
		{"pkg:cargo/serde@1.0.0", "cargo", "", "serde", "1.0.0", "serde", false},
		{"pkg:npm/lodash@4.17.21", "npm", "", "lodash", "4.17.21", "lodash", false},
		{"pkg:gem/rails@7.0.0", "gem", "", "rails", "7.0.0", "rails", false},

		// npm scoped packages (packageurl-go keeps @ in namespace)
		{"pkg:npm/%40babel/core", "npm", "@babel", "core", "", "@babel/core", false},
		{"pkg:npm/%40babel/core@7.24.0", "npm", "@babel", "core", "7.24.0", "@babel/core", false},

		// Maven with groupId
		{"pkg:maven/org.apache.commons/commons-lang3", "maven", "org.apache.commons", "commons-lang3", "", "org.apache.commons:commons-lang3", false},
		{"pkg:maven/org.apache.commons/commons-lang3@3.12.0", "maven", "org.apache.commons", "commons-lang3", "3.12.0", "org.apache.commons:commons-lang3", false},

		// Go modules
		{"pkg:golang/github.com/gorilla/mux", "golang", "github.com/gorilla", "mux", "", "github.com/gorilla/mux", false},
		{"pkg:golang/github.com/gorilla/mux@v1.8.0", "golang", "github.com/gorilla", "mux", "v1.8.0", "github.com/gorilla/mux", false},

		// Terraform modules (namespace/name/provider - library parses as namespace=hashicorp/consul, name=aws)
		{"pkg:terraform/hashicorp/consul/aws", "terraform", "hashicorp/consul", "aws", "", "hashicorp/consul/aws", false},
		{"pkg:terraform/hashicorp/consul/aws@0.11.0", "terraform", "hashicorp/consul", "aws", "0.11.0", "hashicorp/consul/aws", false},

		// Hex
		{"pkg:hex/phoenix", "hex", "", "phoenix", "", "phoenix", false},
		{"pkg:hex/phoenix@1.7.0", "hex", "", "phoenix", "1.7.0", "phoenix", false},

		// Errors
		{"cargo/serde", "", "", "", "", "", true}, // missing pkg: prefix
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p, err := ParsePURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParsePURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if p.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", p.Type, tt.wantType)
			}
			if p.Namespace != tt.wantNS {
				t.Errorf("Namespace = %q, want %q", p.Namespace, tt.wantNS)
			}
			if p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}
			if p.Version != tt.wantVer {
				t.Errorf("Version = %q, want %q", p.Version, tt.wantVer)
			}
			if p.FullName() != tt.wantFull {
				t.Errorf("FullName() = %q, want %q", p.FullName(), tt.wantFull)
			}
		})
	}
}

func TestFullName(t *testing.T) {
	tests := []struct {
		purl string
		want string
	}{
		{"pkg:cargo/serde", "serde"},
		{"pkg:npm/lodash", "lodash"},
		{"pkg:npm/%40babel/core@7.24.0", "@babel/core"},
		{"pkg:maven/org.apache.commons/commons-lang3@3.12.0", "org.apache.commons:commons-lang3"},
		{"pkg:golang/github.com/gorilla/mux@v1.8.0", "github.com/gorilla/mux"},
		{"pkg:terraform/hashicorp/consul/aws@0.11.0", "hashicorp/consul/aws"},
	}

	for _, tt := range tests {
		t.Run(tt.purl, func(t *testing.T) {
			p, err := ParsePURL(tt.purl)
			if err != nil {
				t.Fatalf("ParsePURL(%q) error = %v", tt.purl, err)
			}
			if got := p.FullName(); got != tt.want {
				t.Errorf("FullName() = %q, want %q", got, tt.want)
			}
		})
	}
}
