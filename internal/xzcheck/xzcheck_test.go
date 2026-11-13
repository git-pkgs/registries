package xzcheck

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func xzData(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

const sample = "Package: curl\nVersion: 7.88.1-10\n"

func TestCheckXZDictionary(t *testing.T) {
	small := xzData(t, sample)
	if err := Check(small, MaxDict); err != nil {
		t.Fatalf("Check(8 MiB dictionary): %v", err)
	}
	// 2 GiB, past the 64 MiB limit.
	if err := Check(setXZDict(t, small, 38), MaxDict); !errors.Is(err, ErrMalformed) {
		t.Errorf("2 GiB dictionary: err = %v, want ErrMalformed", err)
	}
	// A large dictionary in a second stream, after padding, is found too.
	two := append(append(bytes.Clone(small), 0, 0, 0, 0), setXZDict(t, small, 38)...)
	if err := Check(two, MaxDict); !errors.Is(err, ErrMalformed) {
		t.Errorf("second stream: err = %v, want ErrMalformed", err)
	}
	if err := Check(append(append(bytes.Clone(small), 0, 0, 0, 0), small...), MaxDict); err != nil {
		t.Errorf("two small streams: %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":     nil,
		"truncated": small[:len(small)-3],
		"garbage":   []byte(strings.Repeat("x", 64)),
	} {
		if err := Check(data, MaxDict); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", name, err)
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
	out := append(bytes.Clone(headerMagic), 0, 1, 0, 0, 0, 0)
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
	copy(footer[10:], footerMagic)
	return append(out, footer...)
}

func TestCheckXZContainer(t *testing.T) {
	small := lzma2Props(22)
	large := lzma2Props(38)
	valid := xzStream(xzBlockHeader(0x00, small))
	if err := Check(valid, MaxDict); err != nil {
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
			if err := Check(data, MaxDict); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
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
