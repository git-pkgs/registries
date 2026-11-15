package deb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
)

func TestParseStanzas(t *testing.T) {
	long := strings.Repeat("libfoo, ", 10_000) // longer than bufio's 64 KiB default
	index := "package: Foo\nVERSION: 1.0-1\nArchitecture: amd64\n" +
		"Depends: libc6 (>= 2.34),\n libssl3 (>= 3)\n" +
		"Provides: " + long + "\n" +
		"Description: first line\n second line\n" +
		"Filename: pool/f/foo_1.0-1_amd64.deb\n \t\n" +
		"Package: bar\nVersion: 2\nArchitecture: all\n"
	var got []record
	err := parseStanzas(strings.NewReader(index), func(rec record) { got = append(got, rec) })
	if err != nil {
		t.Fatalf("parseStanzas: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("records = %+v, want two", got)
	}
	foo := got[0]
	if foo.name != "Foo" || foo.version != "1.0-1" || foo.arch != "amd64" || foo.filename != "pool/f/foo_1.0-1_amd64.deb" {
		t.Errorf("foo = %+v", foo)
	}
	if foo.depends != "libc6 (>= 2.34), libssl3 (>= 3)" {
		t.Errorf("folded Depends = %q", foo.depends)
	}
	if foo.description != "first line" {
		t.Errorf("description = %q, want its first line", foo.description)
	}
	if got[1].name != "bar" || got[1].arch != "all" {
		t.Errorf("bar = %+v", got[1])
	}
}

func TestParseStanzasMalformed(t *testing.T) {
	for name, index := range map[string]string{
		"no colon":             "Package: foo\nVersion 1\n",
		"no package":           "Version: 1\nArchitecture: amd64\n",
		"no version":           "Package: foo\n\nPackage: bar\nVersion: 1\n",
		"leading continuation": " continued\nPackage: foo\nVersion: 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := parseStanzas(strings.NewReader(index), func(record) {})
			if !errors.Is(err, ErrMalformedIndex) {
				t.Errorf("err = %v, want ErrMalformedIndex", err)
			}
		})
	}
}

func TestParseRelease(t *testing.T) {
	data := "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" +
		"Suite: oldstable-updates\nCodename: bookworm-updates\n" +
		"Architectures: all amd64\nMD5Sum:\n 0123 12 main/binary-amd64/Packages.xz\n 4567 3 main/binary-arm64/Packages\n" +
		"SHA256:\n abcd     12 main/binary-amd64/Packages.xz\n ef01 0 main/binary-all/Packages\n" +
		"- Dash-Escaped: kept\n" +
		"-----BEGIN PGP SIGNATURE-----\n\niQIz\n-----END PGP SIGNATURE-----\n" +
		// Text after the signature isn't signed, and is not read.
		"Codename: unsigned\nSHA256:\n 9999 1 main/binary-amd64/Packages\n"
	rel, err := parseRelease([]byte(data))
	if err != nil {
		t.Fatalf("parseRelease: %v", err)
	}
	if rel.codename != "bookworm-updates" {
		t.Errorf("codename = %q", rel.codename)
	}
	want := map[string]fileSum{
		"main/binary-amd64/Packages.xz": {sha256: "abcd", size: 12},
		"main/binary-all/Packages":      {sha256: "ef01", size: 0},
	}
	if !reflect.DeepEqual(rel.files, want) {
		t.Errorf("files = %+v, want %+v", rel.files, want)
	}
}

// A clearsigned file may use CRLF line endings.
func TestParseReleaseCRLF(t *testing.T) {
	data := "-----BEGIN PGP SIGNED MESSAGE-----\r\nHash: SHA256\r\n\r\n" +
		"Codename: bookworm\r\nSHA256:\r\n abcd 12 main/binary-amd64/Packages.xz\r\n" +
		"-----BEGIN PGP SIGNATURE-----\r\n\r\niQIz\r\n-----END PGP SIGNATURE-----\r\n" +
		"Codename: unsigned\r\n"
	rel, err := parseRelease([]byte(data))
	if err != nil {
		t.Fatalf("parseRelease: %v", err)
	}
	want := map[string]fileSum{"main/binary-amd64/Packages.xz": {sha256: "abcd", size: 12}}
	if rel.codename != "bookworm" || !reflect.DeepEqual(rel.files, want) {
		t.Errorf("release = %+v", rel)
	}
}

// A clearsigned file without its header separator or signature is not
// read as an empty Release, which would leave the indexes unchecked.
func TestParseReleaseBrokenEnvelope(t *testing.T) {
	for name, data := range map[string]string{
		"no blank line": "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\nCodename: bookworm\n",
		"no signature":  "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nCodename: bookworm\n",
	} {
		if _, err := parseRelease([]byte(data)); !errors.Is(err, ErrMalformedIndex) {
			t.Errorf("%s: err = %v, want ErrMalformedIndex", name, err)
		}
	}
}

func TestParseReleaseMalformedEntry(t *testing.T) {
	for _, entry := range []string{" abcd notasize main/binary-amd64/Packages", " abcd 12 main/binary-amd64/Packages extra", " abcd 12"} {
		if _, err := parseRelease([]byte("SHA256:\n" + entry + "\n")); !errors.Is(err, ErrMalformedIndex) {
			t.Errorf("%q: err = %v, want ErrMalformedIndex", entry, err)
		}
	}
}

func TestDistroFromCodename(t *testing.T) {
	for in, want := range map[string]string{
		"bookworm":          "bookworm",
		"bookworm-updates":  "bookworm",
		"bookworm-security": "bookworm",
		"jammy-updates":     "jammy",
	} {
		if got := distro(in); got != want {
			t.Errorf("distro(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDependencies(t *testing.T) {
	got := parseDependencies("libc6 (>= 2.34)", "adduser, libssl3 (>= 3.0.13) | libssl3t64, zlib1g:any (>=1:1.1.4), libselinux1 [linux-any], libfoo <!nocheck>, "+
		"libold (<< 2.0), libnew (>> 1.0) [amd64] <!stage1>, libexact (= 1:2-3)<cross>, libbare[i386], libfirst | libsecond (>= 1)")
	want := []dependency{
		{"libc6", ">= 2.34"}, {"adduser", ""}, {"libssl3", ">= 3.0.13"},
		{"zlib1g", ">=1:1.1.4"}, {"libselinux1", ""}, {"libfoo", ""},
		{"libold", "<< 2.0"}, {"libnew", ">> 1.0"}, {"libexact", "= 1:2-3"}, {"libbare", ""}, {"libfirst", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDependencies =\n %+v\nwant\n %+v", got, want)
	}
}

func TestPoolPath(t *testing.T) {
	for in, ok := range map[string]bool{
		"pool/main/c/curl/curl_7.88.1-10_amd64.deb": true,
		"":                          false,
		"/etc/passwd":               false,
		"pool/../../etc/passwd":     false,
		"pool/x.deb?query":          false,
		"pool/x.deb#frag":           false,
		`pool\x.deb`:                false,
		"pool/%2e%2e/x.deb":         false,
		"http://evil.example/x.deb": false,
	} {
		if got := safePoolPath(in); got != ok {
			t.Errorf("safePoolPath(%q) = %v, want %v", in, got, ok)
		}
	}
}

// setXZDict rewrites the dictionary size of every LZMA2 block header in
// data, fixing the header CRC, so a small file claims a large dictionary.
func setXZDict(t *testing.T, data []byte, bits byte) []byte {
	t.Helper()
	out := bytes.Clone(data)
	// A file written by xzData holds one stream with one block, whose
	// header starts after the 12-byte stream header.
	const start = 12
	size := (int(out[start]) + 1) * 4
	header := out[start : start+size]
	i := bytes.Index(header, []byte{0x21, 0x01})
	if i < 0 {
		t.Fatal("no LZMA2 filter in the block header")
	}
	header[i+2] = bits
	binary.LittleEndian.PutUint32(header[size-4:], crc32.ChecksumIEEE(header[:size-4]))
	return out
}
