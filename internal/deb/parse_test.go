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
	got := parseDependencies("libc6 (>= 2.34)", "adduser, libssl3 (>= 3.0.13) | libssl3t64, zlib1g:any (>=1:1.1.4), libselinux1 [linux-any], libfoo <!nocheck>, ")
	want := []dependency{
		{"libc6", ">= 2.34"}, {"adduser", ""}, {"libssl3", ">= 3.0.13"},
		{"zlib1g", ">=1:1.1.4"}, {"libselinux1", ""}, {"libfoo", ""},
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

func TestCheckXZDictionary(t *testing.T) {
	small := xzData(t, bookwormAMD64)
	if err := checkXZ(small, maxXZDict); err != nil {
		t.Fatalf("checkXZ(8 MiB dictionary): %v", err)
	}
	// 2 GiB, past the 64 MiB limit.
	if err := checkXZ(setXZDict(t, small, 38), maxXZDict); !errors.Is(err, ErrMalformedIndex) {
		t.Errorf("2 GiB dictionary: err = %v, want ErrMalformedIndex", err)
	}
	// A large dictionary in a second stream, after padding, is found too.
	two := append(append(bytes.Clone(small), 0, 0, 0, 0), setXZDict(t, small, 38)...)
	if err := checkXZ(two, maxXZDict); !errors.Is(err, ErrMalformedIndex) {
		t.Errorf("second stream: err = %v, want ErrMalformedIndex", err)
	}
	if err := checkXZ(append(append(bytes.Clone(small), 0, 0, 0, 0), small...), maxXZDict); err != nil {
		t.Errorf("two small streams: %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":     nil,
		"truncated": small[:len(small)-3],
		"garbage":   []byte(strings.Repeat("x", 64)),
	} {
		if err := checkXZ(data, maxXZDict); !errors.Is(err, ErrMalformedIndex) {
			t.Errorf("%s: err = %v, want ErrMalformedIndex", name, err)
		}
	}
}

func TestXZDictSize(t *testing.T) {
	for bits, want := range map[byte]uint64{0: 4096, 1: 6144, 18: 2 << 20, 19: 3 << 20, 22: 8 << 20, 40: 1<<32 - 1} {
		if got := lzma2DictSize(bits); got != want {
			t.Errorf("lzma2DictSize(%d) = %d, want %d", bits, got, want)
		}
	}
}

// xzVarint encodes an xz multibyte integer.
func xzVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

// xzBlockHeader builds a block header from its flags byte and the bytes
// after it (sizes and filter flags), padded and with a CRC32 slot.
func xzBlockHeader(flags byte, body ...[]byte) []byte {
	h := []byte{0, flags}
	for _, b := range body {
		h = append(h, b...)
	}
	for (len(h)+4)%4 != 0 {
		h = append(h, 0)
	}
	h = append(h, 0, 0, 0, 0)
	h[0] = byte(len(h)/4 - 1)
	return h
}

func lzma2Props(bits byte) []byte { return []byte{lzma2Filter, 0x01, bits} }

// xzStream builds one stream whose blocks hold only their headers. Nothing
// here is meant to decode; checkXZ reads only the container.
func xzStream(headers ...[]byte) []byte {
	out := append(bytes.Clone(xzHeaderMagic), 0, 1, 0, 0, 0, 0)
	index := []byte{0}
	index = append(index, xzVarint(uint64(len(headers)))...)
	for _, h := range headers {
		out = append(out, h...)
		index = append(index, xzVarint(uint64(len(h)))...)
		index = append(index, xzVarint(1)...)
	}
	for len(index)%4 != 0 {
		index = append(index, 0)
	}
	index = append(index, 0, 0, 0, 0)
	out = append(out, index...)
	footer := make([]byte, 12)
	binary.LittleEndian.PutUint32(footer[4:8], uint32(len(index)/4-1))
	copy(footer[10:], xzFooterMagic)
	return append(out, footer...)
}

func TestCheckXZContainer(t *testing.T) {
	small := lzma2Props(22)
	large := lzma2Props(38)
	valid := xzStream(xzBlockHeader(0x00, small))
	if err := checkXZ(valid, maxXZDict); err != nil {
		t.Fatalf("valid synthetic stream: %v", err)
	}

	patch := func(data []byte, at int, b ...byte) []byte {
		out := bytes.Clone(data)
		if at < 0 {
			at += len(out)
		}
		copy(out[at:], b)
		return out
	}
	indexStart := len(valid) - 12 - 8 // one record: indicator, count, two sizes, padding, CRC
	tenByteOne := []byte{0x81, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}

	for name, data := range map[string][]byte{
		"footer magic":         patch(valid, -2, 'Y', 'X'),
		"header magic":         patch(valid, 0, 0xFE),
		"index indicator":      patch(valid, indexStart, 1),
		"index past the start": patch(valid, -8, 0xFF, 0xFF, 0xFF, 0x0F),
		"huge record count":    xzStreamCount(t, valid, 1<<40),
		"zero header size":     xzStream(append([]byte{0}, xzBlockHeader(0x00, small)[1:]...)),
		"header past block":    xzStream(append([]byte{0x40}, xzBlockHeader(0x00, small)[1:]...)),
		// Read as filter flags, these sizes would name a one-byte filter
		// that isn't LZMA2 and hide the one after them.
		"large after sizes": xzStream(xzBlockHeader(0xC0, xzVarint(3), xzVarint(1), large)),
		// The same with each size on its own: past the size, the filter
		// flags claim 33 bytes of properties, which aren't there.
		"compressed size only":   xzStream(xzBlockHeader(0x40, xzVarint(3), []byte{0x01, 0x21})),
		"uncompressed size only": xzStream(xzBlockHeader(0x80, xzVarint(3), []byte{0x01, 0x21})),
		"large second filter":    xzStream(xzBlockHeader(0x01, []byte{0x03, 0x01, 0x00}, large)),
		"properties past 40":     xzStream(xzBlockHeader(0x00, lzma2Props(200))),
		"overlong varint":        xzStream(xzBlockHeader(0x00, []byte{lzma2Filter}, tenByteOne, []byte{22})),
		"large second block":     xzStream(xzBlockHeader(0x00, small), xzBlockHeader(0x00, large)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkXZ(data, maxXZDict); !errors.Is(err, ErrMalformedIndex) {
				t.Errorf("err = %v, want ErrMalformedIndex", err)
			}
		})
	}
}

// xzStreamCount rewrites the record count of a one-block stream's index.
func xzStreamCount(t *testing.T, stream []byte, count uint64) []byte {
	t.Helper()
	indexStart := len(stream) - 12 - 8
	index := append([]byte{0}, xzVarint(count)...)
	if len(index) > 8 {
		t.Fatal("count too long for the index")
	}
	return append(append(bytes.Clone(stream[:indexStart]), append(index, make([]byte, 8-len(index))...)...), stream[len(stream)-12:]...)
}
