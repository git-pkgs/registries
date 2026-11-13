package deb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
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
	for len(h)%4 != 0 {
		h = append(h, 0)
	}
	h = append(h, 0, 0, 0, 0)
	h[0] = byte(len(h)/4 - 1)
	return h
}

func lzma2Props(bits byte) []byte { return []byte{lzma2Filter, 0x01, bits} }

// xzBlock is a block header and the LZMA2 chunks after it, without the end
// marker. Nothing here is meant to decode; checkXZ reads only the layout.
type xzBlock struct {
	header []byte
	chunks []byte
}

// xzFile describes a stream to build.
type xzFile struct {
	blocks    []xzBlock
	checkType byte // 1 is CRC32, four bytes after each block
	// records is how many blocks the index lists; -1 lists them all.
	records int
}

func (f xzFile) bytes() []byte {
	checkSize := xzCheckSizes[f.checkType]
	out := append(bytes.Clone(xzHeaderMagic), 0, f.checkType, 0, 0, 0, 0)
	records := f.records
	if records < 0 {
		records = len(f.blocks)
	}
	index := append([]byte{0}, xzVarint(uint64(records))...)
	for i, b := range f.blocks {
		block := append(bytes.Clone(b.header), b.chunks...)
		block = append(block, 0) // the LZMA2 end marker
		if i < records {
			index = append(index, xzVarint(uint64(len(block)+checkSize))...)
			index = append(index, xzVarint(1)...)
		}
		for len(block)%4 != 0 {
			block = append(block, 0)
		}
		out = append(out, block...)
		out = append(out, make([]byte, checkSize)...)
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

// xzStream builds a stream of blocks with these headers, a CRC32 check and
// an index listing them all.
func xzStream(headers ...[]byte) []byte {
	f := xzFile{checkType: 1, records: -1}
	for _, h := range headers {
		f.blocks = append(f.blocks, xzBlock{header: h})
	}
	return f.bytes()
}

func TestCheckXZContainer(t *testing.T) {
	small := xzBlockHeader(0x00, lzma2Props(22))
	large := xzBlockHeader(0x00, lzma2Props(38))
	valid := xzStream(small)

	// Chunks as LZMA2 frames them: stored (control, size-1, data) and LZMA
	// (control, uncompressed size-1, compressed size-1, properties on a
	// reset, data). Their data is filled with bytes that would read as a
	// block header asking for a large dictionary, or as an end marker, if
	// a chunk were walked by the wrong size.
	filler := func(n int) []byte { return bytes.Repeat(append([]byte{0}, large...), n)[:n] }
	stored := append([]byte{0x01, 0x00, 0x1F}, filler(0x20)...)
	storedNoReset := append([]byte{0x02, 0x01, 0x0F}, filler(0x110)...)
	lzmaChunk := append([]byte{0x80, 0xFF, 0xFF, 0x00, 0x2F}, filler(0x30)...)
	lzmaWithProps := append([]byte{0xE0, 0x00, 0x10, 0x01, 0x04, 0x5D}, filler(0x105)...)
	concat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

	for name, data := range map[string][]byte{
		"one block":                  valid,
		"two blocks":                 xzStream(small, small),
		"no blocks":                  xzStream(),
		"no check":                   xzFile{blocks: []xzBlock{{header: small}, {header: small}}, checkType: 0, records: -1}.bytes(),
		"crc64 check":                xzFile{blocks: []xzBlock{{header: small}, {header: small}}, checkType: 4, records: -1}.bytes(),
		"sha256 check":               xzFile{blocks: []xzBlock{{header: small}, {header: small}}, checkType: 10, records: -1}.bytes(),
		"stored chunk":               xzFile{blocks: []xzBlock{{small, stored}, {header: small}}, checkType: 1, records: -1}.bytes(),
		"stored without reset":       xzFile{blocks: []xzBlock{{small, storedNoReset}, {header: small}}, checkType: 1, records: -1}.bytes(),
		"lzma chunk":                 xzFile{blocks: []xzBlock{{small, lzmaChunk}, {header: small}}, checkType: 1, records: -1}.bytes(),
		"lzma chunk with properties": xzFile{blocks: []xzBlock{{small, lzmaWithProps}, {header: small}}, checkType: 1, records: -1}.bytes(),
		"several chunks":             xzFile{blocks: []xzBlock{{small, concat(lzmaWithProps, stored, lzmaChunk, storedNoReset)}, {header: small}}, checkType: 1, records: -1}.bytes(),
		"sizes then small":           xzStream(xzBlockHeader(0xC0, xzVarint(100), xzVarint(200), lzma2Props(22))),
		"delta then small":           xzStream(xzBlockHeader(0x01, []byte{0x03, 0x01, 0x00}, lzma2Props(22))),
		"padded streams":             concat(valid, make([]byte, 8), valid, make([]byte, 4)),
	} {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := checkXZ(data, maxXZDict); err != nil {
				t.Errorf("err = %v, want none", err)
			}
		})
	}

	patch := func(data []byte, at int, b ...byte) []byte {
		out := bytes.Clone(data)
		if at < 0 {
			at += len(out)
		}
		copy(out[at:], b)
		return out
	}
	tenByteOne := []byte{0x81, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}
	for name, data := range map[string][]byte{
		"footer magic":        patch(valid, -2, 'Y', 'X'),
		"header magic":        patch(valid, 0, 0xFE),
		"header past the end": xzStream(append([]byte{0xFF}, small[1:]...)),
		// The index lists one block; the decoder reads both headers.
		"block the index leaves out":     xzFile{blocks: []xzBlock{{header: small}, {header: large}}, checkType: 1, records: 1}.bytes(),
		"index lists more than it holds": xzFile{blocks: []xzBlock{{header: small}}, checkType: 1, records: 1 << 30}.bytes(),
		"large second block":             xzStream(small, large),
		"large after a stored chunk":     xzFile{blocks: []xzBlock{{small, stored}, {header: large}}, checkType: 1, records: -1}.bytes(),
		"large after an lzma chunk":      xzFile{blocks: []xzBlock{{small, lzmaWithProps}, {header: large}}, checkType: 1, records: -1}.bytes(),
		"large in a second stream":       concat(valid, make([]byte, 4), xzStream(large)),
		"bad chunk control":              xzFile{blocks: []xzBlock{{small, []byte{0x05, 0x00, 0x00}}}, checkType: 1, records: -1}.bytes(),
		"chunk past the end":             xzFile{blocks: []xzBlock{{small, []byte{0x01, 0xFF, 0xFF}}}, checkType: 1, records: -1}.bytes(),
		// Read as filter flags, these sizes would name a one-byte filter
		// that isn't LZMA2 and hide the one after them.
		"large after sizes":         xzStream(xzBlockHeader(0xC0, xzVarint(3), xzVarint(1), lzma2Props(38))),
		"compressed size only":      xzStream(xzBlockHeader(0x40, xzVarint(3), []byte{0x01, 0x21})),
		"uncompressed size only":    xzStream(xzBlockHeader(0x80, xzVarint(3), []byte{0x01, 0x21})),
		"large second filter":       xzStream(xzBlockHeader(0x01, []byte{0x03, 0x01, 0x00}, lzma2Props(38))),
		"last filter not lzma2":     xzStream(xzBlockHeader(0x01, lzma2Props(22), []byte{0x03, 0x01, 0x00})),
		"properties past 40":        xzStream(xzBlockHeader(0x00, lzma2Props(200))),
		"overlong varint":           xzStream(xzBlockHeader(0x00, []byte{lzma2Filter}, tenByteOne, []byte{22})),
		"padding of two bytes":      concat(valid, make([]byte, 2)),
		"padding with a stray byte": concat(valid, []byte{0, 1, 0, 0}, valid),
		"padding of six bytes":      concat(valid, make([]byte, 6), valid),
		"garbage after a stream":    concat(valid, []byte("garbage!")),
		"padding before a stream":   concat(make([]byte, 4), valid),
	} {
		t.Run("refused/"+name, func(t *testing.T) {
			if err := checkXZ(data, maxXZDict); !errors.Is(err, ErrMalformedIndex) {
				t.Errorf("err = %v, want ErrMalformedIndex", err)
			}
		})
	}
}

// TestCheckXZRealFiles walks files real encoders wrote: Debian's own index,
// and a multi-block file of compressible and incompressible data, which
// holds both kinds of chunk. Every truncation of them is refused.
func TestCheckXZRealFiles(t *testing.T) {
	debian, err := os.ReadFile("testdata/bookworm-updates-Packages.xz")
	if err != nil {
		t.Fatal(err)
	}
	noise := make([]byte, 200_000)
	state := uint32(1)
	for i := range noise {
		state = state*1664525 + 1013904223
		noise[i] = byte(state >> 24)
	}
	var buf bytes.Buffer
	w, err := xz.WriterConfig{BlockSize: 64 << 10}.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range [][]byte{[]byte(strings.Repeat(bookwormAMD64, 200)), noise, []byte(strings.Repeat("x", 300_000))} {
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string][]byte{"debian": debian, "multi-block": buf.Bytes()} {
		if err := checkXZ(data, maxXZDict); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		// The decoder agrees it is whole, so the walk covered every block.
		r, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			t.Fatalf("%s does not decode: %v", name, err)
		}
		step := 1
		if len(data) > 20_000 {
			step = 97
		}
		for n := 0; n < len(data); n += step {
			if err := checkXZ(data[:n], maxXZDict); !errors.Is(err, ErrMalformedIndex) {
				t.Fatalf("%s cut to %d bytes: err = %v, want ErrMalformedIndex", name, n, err)
			}
		}
	}
}
