package urlparser

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// GitHub URLs - from librariesio-url-parser test suite
		{"https://github.com/maxcdn/shml/", "https://github.com/maxcdn/shml"},
		{"https://foo.github.io/bar", "https://github.com/foo/bar"},
		{"git+https://github.com/hugojosefson/express-cluster-stability.git", "https://github.com/hugojosefson/express-cluster-stability"},
		{"sughodke.github.com/linky.js/", "https://github.com/sughodke/linky.js"},
		{"www.github.com/37point2/brainfuckifyjs", "https://github.com/37point2/brainfuckifyjs"},
		{"ssh://git@github.org:brozeph/node-craigslist.git", "https://github.com/brozeph/node-craigslist"},
		{"ssh+git@github.com:omardelarosa/tonka-npm.git", "https://github.com/omardelarosa/tonka-npm"},
		{"scm:svn:https://github.com/tanhaichao/top4j/tags/top4j-0.0.1", "https://github.com/tanhaichao/top4j"},
		{"scm:https://vert-x@github.com/purplefox/vert.x", "https://github.com/purplefox/vert.x"},
		{"scm:https:https://github.com/vaadin/vaadin.git", "https://github.com/vaadin/vaadin"},
		{"scm:https://github.com/daimajia/AndroidAnimations.git", "https://github.com/daimajia/AndroidAnimations"},
		{"scm:git:ssh@github.com:claudius108/maven-plugins.git", "https://github.com/claudius108/maven-plugins"},
		{"scm:git:https://github.com/axet/sqlite4java", "https://github.com/axet/sqlite4java"},
		{"scm:git:https://github.com/celum/db-tool.git", "https://github.com/celum/db-tool"},
		{"scm:git:https://ffromm@github.com/jenkinsci/slave-setup-plugin.git", "https://github.com/jenkinsci/slave-setup-plugin"},
		{"scm:git:github.com/yfcai/CREG.git", "https://github.com/yfcai/CREG"},
		{"scm:git@github.com:urunimi/PullToRefreshAndroid.git", "https://github.com/urunimi/PullToRefreshAndroid"},
		{"scm:git://github.com/lihaoyi/ajax.git", "https://github.com/lihaoyi/ajax"},
		{"https://RobinQu@github.com/RobinQu/node-gear.git", "https://github.com/RobinQu/node-gear"},
		{"https://taylorhakes@github.com/taylorhakes/promise-polyfill.git", "https://github.com/taylorhakes/promise-polyfill"},
		{"https://hcnode.github.com/node-gitignore", "https://github.com/hcnode/node-gitignore"},
		{"https://github.org/srcagency/js-slash-tail.git", "https://github.com/srcagency/js-slash-tail"},
		{"https://gf3@github.com/gf3/IRC-js.git", "https://github.com/gf3/IRC-js"},
		{"https://crcn:KQ3Lc6za@github.com/crcn/verify.js.git", "https://github.com/crcn/verify.js"},
		{"https://bgrins.github.com/spectrum", "https://github.com/bgrins/spectrum"},
		{"//github.com/dtrejo/report.git", "https://github.com/dtrejo/report"},
		{"=https://github.com/amansatija/Cus360MavenCentralDemoLib.git", "https://github.com/amansatija/Cus360MavenCentralDemoLib"},
		{"git+https://bebraw@github.com/bebraw/colorjoe.git", "https://github.com/bebraw/colorjoe"},
		{"git:///github.com/NovaGL/homebridge-openremote.git", "https://github.com/NovaGL/homebridge-openremote"},
		{"git://git@github.com/jballant/webpack-strip-block.git", "https://github.com/jballant/webpack-strip-block"},
		{"git://github.com/2betop/yogurt-preprocessor-extlang.git", "https://github.com/2betop/yogurt-preprocessor-extlang"},
		{"git:/github.com/shibukawa/burrows-wheeler-transform.jsx.git", "https://github.com/shibukawa/burrows-wheeler-transform.jsx"},
		{"git:git://github.com/alaz/mongo-scala-driver.git", "https://github.com/alaz/mongo-scala-driver"},
		{"git:git@github.com:doug-martin/string-extended.git", "https://github.com/doug-martin/string-extended"},
		{"git:github.com//dominictarr/level-couch-sync.git", "https://github.com/dominictarr/level-couch-sync"},
		{"git:github.com/dominictarr/keep.git", "https://github.com/dominictarr/keep"},
		{"git:https://github.com/vaadin/cdi.git", "https://github.com/vaadin/cdi"},
		{"git@git@github.com:dead-horse/webT.git", "https://github.com/dead-horse/webT"},
		{"git@github.com:agilemd/then.git", "https://github.com/agilemd/then"},
		{"git@git.github.com:daddye/stitchme.git", "https://github.com/daddye/stitchme"},
		{"github.com/1995hnagamin/hubot-achievements", "https://github.com/1995hnagamin/hubot-achievements"},
		{"git//github.com/divyavanmahajan/jsforce_downloader.git", "https://github.com/divyavanmahajan/jsforce_downloader"},
		{"github.com/github/combobox-nav", "https://github.com/github/combobox-nav"},

		// Anchors and querystrings
		{"scm:git:https://michaelkrog@github.com/michaelkrog/filter4j.git#anchor", "https://github.com/michaelkrog/filter4j"},
		{"scm:git:https://michaelkrog@github.com/michaelkrog/filter4j.git?foo=bar&wut=wah", "https://github.com/michaelkrog/filter4j"},

		// Brackets
		{"[scm:git:https://michaelkrog@github.com/michaelkrog/filter4j.git]", "https://github.com/michaelkrog/filter4j"},
		{"<scm:git:https://michaelkrog@github.com/michaelkrog/filter4j.git>", "https://github.com/michaelkrog/filter4j"},
		{"(scm:git:https://michaelkrog@github.com/michaelkrog/filter4j.git)", "https://github.com/michaelkrog/filter4j"},

		// Quotes
		{`https://github.com/"/maxcdn/shml/`, "https://github.com/maxcdn/shml"},
		{`https://github.com/"sunilrumbalama/docxtopdf"`, "https://github.com/sunilrumbalama/docxtopdf"},

		// GitLab URLs
		{"https://gitlab.com/user/repo", "https://gitlab.com/user/repo"},
		{"git@gitlab.com:user/repo.git", "https://gitlab.com/user/repo"},
		{"https://gitlab.com/user/repo.git", "https://gitlab.com/user/repo"},

		// Bitbucket URLs
		{"https://bitbucket.org/user/repo", "https://bitbucket.org/user/repo"},
		{"git@bitbucket.org:user/repo.git", "https://bitbucket.org/user/repo"},
		{"https://bitbucket.org/user/repo.git", "https://bitbucket.org/user/repo"},

		// Codeberg
		{"https://codeberg.org/user/repo", "https://codeberg.org/user/repo"},
		{"git@codeberg.org:user/repo.git", "https://codeberg.org/user/repo"},

		// Sourcehut
		{"https://sr.ht/~user/repo", "https://sr.ht/~user/repo"},

		// Unknown hosts should still work
		{"https://git.example.com/user/repo", "https://git.example.com/user/repo"},
		{"git@git.mycompany.com:team/project.git", "https://git.mycompany.com/team/project"},
		{"https://gitea.mydomain.org/org/repo", "https://gitea.mydomain.org/org/repo"},

		// Non-matching URLs should return empty
		{"https://google.com", ""},
		{"https://github.com/foo", ""},
		{"https://github.com", ""},
		{"https://foo.github.io", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Parse(tt.input)
			if got != tt.want {
				t.Errorf("Parse(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractOwnerRepo(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/owner/repo", "owner/repo"},
		{"git@github.com:owner/repo.git", "owner/repo"},
		{"https://gitlab.com/owner/repo", "owner/repo"},
		{"https://bitbucket.org/owner/repo", "owner/repo"},
		{"https://github.com/owner/repo/tree/main/subdir", "owner/repo"},
		{"https://git.example.com/owner/repo", "owner/repo"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExtractOwnerRepo(tt.input)
			if got != tt.want {
				t.Errorf("ExtractOwnerRepo(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/owner/repo", "github.com"},
		{"git@gitlab.com:owner/repo.git", "gitlab.com"},
		{"https://git.example.com/owner/repo", "git.example.com"},
		{"foo.github.io/bar", "github.io"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExtractHost(tt.input)
			if got != tt.want {
				t.Errorf("ExtractHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsKnownHost(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"https://github.com/foo/bar", true},
		{"https://gitlab.com/foo/bar", true},
		{"https://bitbucket.org/foo/bar", true},
		{"https://codeberg.org/foo/bar", true},
		{"https://gitea.com/foo/bar", true},
		{"https://foo.github.io/bar", true},
		{"https://sourceforge.net/projects/foo", false},
		{"https://git.example.com/foo/bar", false},
		{"https://gitea.mydomain.org/foo/bar", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsKnownHost(tt.input)
			if got != tt.want {
				t.Errorf("IsKnownHost(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/foo/bar", "https://github.com/foo/bar"},
		{"https://gitea.com/foo/bar", "https://gitea.com/foo/bar"},
		{"https://sourceforge.net/projects/foo", ""},
		{"https://example.com/projects/foo", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CanonicalURL(tt.input)
			if got != tt.want {
				t.Errorf("CanonicalURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"git+https://github.com/foo/bar.git", "https://github.com/foo/bar"},
		{"git://github.com/foo/bar.git", "https://github.com/foo/bar"},
		{"github.com/foo/bar", "https://github.com/foo/bar"},
		{"gitlab.com/foo/bar", "https://gitlab.com/foo/bar"},
		{"bitbucket.org/foo/bar", "https://bitbucket.org/foo/bar"},
		{"https://github.com/foo/bar.git", "https://github.com/foo/bar"},
		{"scm:git:https://github.com/foo/bar.git", "https://github.com/foo/bar"},
		{"git@github.com:foo/bar.git", "https://github.com/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Normalize(tt.input)
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		input     string
		wantHost  string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/owner/repo", "github.com", "owner", "repo"},
		{"git@gitlab.com:owner/repo.git", "gitlab.com", "owner", "repo"},
		{"https://git.example.com/owner/repo", "git.example.com", "owner", "repo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseURL(tt.input)
			if got == nil {
				t.Fatalf("ParseURL(%q) = nil, want non-nil", tt.input)
			}
			if got.Host != tt.wantHost {
				t.Errorf("ParseURL(%q).Host = %q, want %q", tt.input, got.Host, tt.wantHost)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("ParseURL(%q).Owner = %q, want %q", tt.input, got.Owner, tt.wantOwner)
			}
			if got.Repo != tt.wantRepo {
				t.Errorf("ParseURL(%q).Repo = %q, want %q", tt.input, got.Repo, tt.wantRepo)
			}
		})
	}
}

func TestFirstRepoURL(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{
			name: "first valid",
			urls: []string{"https://github.com/foo/bar", "https://gitlab.com/baz/qux"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "skip empty",
			urls: []string{"", "https://github.com/foo/bar"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "skip invalid",
			urls: []string{"not a url", "https://github.com/foo/bar"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "all empty",
			urls: []string{"", ""},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FirstRepoURL(tt.urls...)
			if got != tt.want {
				t.Errorf("FirstRepoURL(%v) = %q, want %q", tt.urls, got, tt.want)
			}
		})
	}
}

func TestParseFromMap(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]string
		want string
	}{
		{
			name: "repository key",
			m:    map[string]string{"repository": "https://github.com/foo/bar"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "Source key",
			m:    map[string]string{"Source": "https://github.com/foo/bar"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "fallback to any repo URL",
			m:    map[string]string{"homepage": "https://github.com/foo/bar"},
			want: "https://github.com/foo/bar",
		},
		{
			name: "no repo URLs",
			m:    map[string]string{"homepage": "https://example.com"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFromMap(tt.m)
			if got != tt.want {
				t.Errorf("ParseFromMap(%v) = %q, want %q", tt.m, got, tt.want)
			}
		})
	}
}

func TestClean(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/foo/bar", "github.com/foo/bar"},
		{"git@github.com:foo/bar.git", "github.com/foo/bar"},
		{"  https://github.com/foo/bar  ", "github.com/foo/bar"},
		{`"https://github.com/foo/bar"`, "github.com/foo/bar"},
		{"https://user:pass@github.com/foo/bar", "github.com/foo/bar"},
		{"https://github.com/foo/bar#readme", "github.com/foo/bar"},
		{"https://github.com/foo/bar?ref=main", "github.com/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Clean(tt.input)
			if got != tt.want {
				t.Errorf("Clean(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
