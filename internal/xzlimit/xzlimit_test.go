package xzlimit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

const sample = "Package: curl\nVersion: 7.88.1-10\nArchitecture: amd64\n\n"

func compress(t *testing.T, config xz.WriterConfig, parts ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := config.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// noise is bytes that don't compress, which LZMA2 stores in stored chunks.
func noise(n int) []byte {
	out := make([]byte, n)
	state := uint32(1)
	for i := range out {
		state = state*1664525 + 1013904223
		out[i] = byte(state >> 24)
	}
	return out
}

func decode(data []byte, maxDict uint64) ([]byte, error) {
	return io.ReadAll(NewReader(bytes.NewReader(data), maxDict))
}

// reference decodes with the library's own reader.
func reference(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("the reference reader fails: %v", err)
	}
	return out
}

func TestDecodesWhatTheLibraryDecodes(t *testing.T) {
	debian, err := os.ReadFile("testdata/bookworm-updates-Packages.xz")
	if err != nil {
		t.Fatal(err)
	}
	text := []byte(strings.Repeat(sample, 5000))
	mixed := [][]byte{text, noise(200_000), []byte(strings.Repeat("x", 300_000))}
	for name, data := range map[string][]byte{
		"debian's own index": debian,
		"one small block":    compress(t, xz.WriterConfig{}, []byte(sample)),
		"empty":              compress(t, xz.WriterConfig{}),
		"several blocks":     compress(t, xz.WriterConfig{BlockSize: 64 << 10}, mixed...),
		"crc32 check":        compress(t, xz.WriterConfig{CheckSum: xz.CRC32, BlockSize: 64 << 10}, mixed...),
		"sha256 check":       compress(t, xz.WriterConfig{CheckSum: xz.SHA256, BlockSize: 64 << 10}, mixed...),
		"no check":           compress(t, xz.WriterConfig{NoCheckSum: true, BlockSize: 64 << 10}, mixed...),
		"small dictionary":   compress(t, xz.WriterConfig{DictCap: 4096}, text),
		"two streams":        append(compress(t, xz.WriterConfig{}, text), compress(t, xz.WriterConfig{CheckSum: xz.CRC32}, []byte(sample))...),
		"two padded streams": bytes.Join([][]byte{compress(t, xz.WriterConfig{}, text), make([]byte, 8), compress(t, xz.WriterConfig{}, []byte(sample)), make([]byte, 4)}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			want := reference(t, data)
			got, err := decode(data, MaxDict)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("decoded %d bytes, the library decodes %d; they differ", len(got), len(want))
			}
		})
	}
}

// TestReadsInSmallPieces checks nothing depends on how much a Read asks for.
func TestReadsInSmallPieces(t *testing.T) {
	data := compress(t, xz.WriterConfig{BlockSize: 4 << 10}, []byte(strings.Repeat(sample, 500)), noise(9000))
	want := reference(t, data)
	r := NewReader(iotestOneByte{bytes.NewReader(data)}, MaxDict)
	var got []byte
	buf := make([]byte, 7)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Errorf("decoded %d bytes, want %d", len(got), len(want))
	}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Errorf("Read(nil) = %d, %v", n, err)
	}
}

// iotestOneByte hands out one byte per Read.
type iotestOneByte struct{ r io.Reader }

func (o iotestOneByte) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

// setDict rewrites the dictionary size of the block header at offset in
// data, fixing the header's checksum, so a small block claims a large
// dictionary.
func setDict(t *testing.T, data []byte, offset int, bits byte) []byte {
	t.Helper()
	out := bytes.Clone(data)
	size := (int(out[offset]) + 1) * 4
	header := out[offset : offset+size]
	i := bytes.Index(header, []byte{lzma2Filter, 0x01})
	if i < 0 {
		t.Fatal("no LZMA2 filter in the block header")
	}
	header[i+2] = bits
	binary.LittleEndian.PutUint32(header[size-4:], crc32.ChecksumIEEE(header[:size-4]))
	return out
}

// blockOffsets returns where each block header of a one-stream file starts,
// from the sizes its index lists.
func blockOffsets(t *testing.T, data []byte) []int {
	t.Helper()
	r := NewReader(bytes.NewReader(data), MaxDict)
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	var offsets []int
	offset := headerSize
	for _, rec := range r.records {
		offsets = append(offsets, offset)
		offset += int((rec.unpadded + 3) &^ 3)
	}
	return offsets
}

func TestDictionaryLimit(t *testing.T) {
	small := compress(t, xz.WriterConfig{}, []byte(sample))
	// 128 MiB, past the limit.
	if _, err := decode(setDict(t, small, headerSize, 30), MaxDict); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "dictionary") {
		t.Errorf("128 MiB dictionary: err = %v, want ErrMalformed naming the dictionary", err)
	}
	// The limit itself is allowed: 64 MiB is property 28.
	if dictSize(28) != MaxDict {
		t.Fatalf("dictSize(28) = %d", dictSize(28))
	}
	if _, err := decode(small, 8<<20); err != nil {
		t.Errorf("a block at the limit: %v", err)
	}
	if _, err := decode(small, 8<<20-1); !errors.Is(err, ErrMalformed) {
		t.Errorf("a block one byte over the limit: err = %v, want ErrMalformed", err)
	}

	// A later block is held to the limit when the reader gets to it, after
	// the blocks before it were decoded.
	blocks := compress(t, xz.WriterConfig{BlockSize: 4 << 10}, []byte(strings.Repeat(sample, 500)))
	offsets := blockOffsets(t, blocks)
	if len(offsets) < 3 {
		t.Fatalf("the fixture has %d blocks, want several", len(offsets))
	}
	got, err := decode(setDict(t, blocks, offsets[2], 38), MaxDict)
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("large third block: err = %v, want ErrMalformed", err)
	}
	if len(got) != 2*(4<<10) {
		t.Errorf("decoded %d bytes before the refused block, want the two blocks before it", len(got))
	}
	// And one in a second stream.
	two := bytes.Join([][]byte{small, make([]byte, 4), setDict(t, small, headerSize, 38)}, nil)
	if got, err := decode(two, MaxDict); !errors.Is(err, ErrMalformed) || string(got) != sample {
		t.Errorf("large block in a second stream: %q, %v", got, err)
	}
}

func TestDictSize(t *testing.T) {
	for bits, want := range map[byte]uint64{0: 4096, 1: 6144, 18: 2 << 20, 19: 3 << 20, 22: 8 << 20, 40: 1<<32 - 1} {
		if got := dictSize(bits); got != want {
			t.Errorf("dictSize(%d) = %d, want %d", bits, got, want)
		}
	}
}

// fixCRC recomputes the CRC32 that ends a header of size bytes at offset.
func fixCRC(data []byte, offset, size int) {
	binary.LittleEndian.PutUint32(data[offset+size-4:], crc32.ChecksumIEEE(data[offset:offset+size-4]))
}

func TestMalformedFiles(t *testing.T) {
	valid := compress(t, xz.WriterConfig{CheckSum: xz.CRC32}, []byte(strings.Repeat(sample, 20)))
	blockHeaderSize := (int(valid[headerSize]) + 1) * 4
	patch := func(at int, b ...byte) []byte {
		out := bytes.Clone(valid)
		if at < 0 {
			at += len(out)
		}
		copy(out[at:], b)
		return out
	}
	// The block header: size, flags, filter id, properties size, properties.
	header := func(edit func(h []byte)) []byte {
		out := bytes.Clone(valid)
		edit(out[headerSize : headerSize+blockHeaderSize])
		fixCRC(out, headerSize, blockHeaderSize)
		return out
	}
	streamFlags := func(flag0, checkType byte) []byte {
		out := patch(6, flag0, checkType)
		binary.LittleEndian.PutUint32(out[8:], crc32.ChecksumIEEE(out[6:8]))
		copy(out[len(out)-4:], []byte{flag0, checkType})
		binary.LittleEndian.PutUint32(out[len(out)-12:], crc32.ChecksumIEEE(out[len(out)-8:len(out)-2]))
		return out
	}

	for name, data := range map[string][]byte{
		"empty":                 nil,
		"not xz":                []byte(strings.Repeat("plain text ", 10)),
		"header magic":          patch(0, 0xFE),
		"header checksum":       patch(8, valid[8]^1),
		"stream flags":          streamFlags(1, checkCRC32),
		"unsupported check":     streamFlags(0, 0x02),
		"block header checksum": patch(headerSize+blockHeaderSize-1, valid[headerSize+blockHeaderSize-1]^1),
		"reserved block flags":  header(func(h []byte) { h[1] = 0x04 }),
		"two filters":           header(func(h []byte) { h[1] = 0x01 }),
		"filter not lzma2":      header(func(h []byte) { h[2] = 0x03 }),
		"properties size":       header(func(h []byte) { h[3] = 0x02 }),
		// 200 would shift the size to nothing and read as a tiny dictionary.
		"properties past 40":        header(func(h []byte) { h[4] = 200 }),
		"block header padding":      header(func(h []byte) { h[5] = 1 }),
		"corrupt data":              patch(headerSize+blockHeaderSize+20, valid[headerSize+blockHeaderSize+20]^0xFF),
		"block check":               patch(-12-8-4, valid[len(valid)-12-8-4]^1),
		"index checksum":            patch(-12-1, valid[len(valid)-12-1]^1),
		"footer magic":              patch(-2, 'Y', 'X'),
		"footer checksum":           patch(-12, valid[len(valid)-12]^1),
		"garbage after the stream":  append(bytes.Clone(valid), "garbage!"...),
		"padding of two bytes":      append(bytes.Clone(valid), 0, 0),
		"padding with a stray byte": bytes.Join([][]byte{valid, {0, 1, 0, 0}, valid}, nil),
		"padding before the stream": append(make([]byte, 4), valid...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(data, MaxDict); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

// The index and footer must describe the blocks that were decoded.
func TestIndexAndFooterAreChecked(t *testing.T) {
	valid := compress(t, xz.WriterConfig{CheckSum: xz.CRC32}, []byte(strings.Repeat(sample, 20)))
	// A one-block index is 8 bytes when both sizes fit a byte or two:
	// find it from the footer's backward size.
	indexSize := (int(binary.LittleEndian.Uint32(valid[len(valid)-8:])) + 1) * 4
	indexStart := len(valid) - footerSize - indexSize
	reindex := func(edit func(index []byte)) []byte {
		out := bytes.Clone(valid)
		index := out[indexStart : indexStart+indexSize]
		edit(index)
		binary.LittleEndian.PutUint32(index[indexSize-4:], crc32.ChecksumIEEE(index[:indexSize-4]))
		return out
	}
	refooter := func(edit func(footer []byte)) []byte {
		out := bytes.Clone(valid)
		footer := out[len(out)-footerSize:]
		edit(footer)
		binary.LittleEndian.PutUint32(footer, crc32.ChecksumIEEE(footer[4:10]))
		return out
	}
	for name, data := range map[string][]byte{
		"record count":          reindex(func(index []byte) { index[1] = 2 }),
		"no records":            reindex(func(index []byte) { index[1] = 0 }),
		"unpadded size":         reindex(func(index []byte) { index[2]++ }),
		"uncompressed size":     reindex(func(index []byte) { index[indexSize-5-1]++ }),
		"backward size":         refooter(func(footer []byte) { footer[4]++ }),
		"footer flags":          refooter(func(footer []byte) { footer[9] = checkCRC64 }),
		"footer reserved flags": refooter(func(footer []byte) { footer[8] = 1 }),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(data, MaxDict); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

// Every truncation of a file is an error, never a short result.
func TestTruncatedFiles(t *testing.T) {
	debian, err := os.ReadFile("testdata/bookworm-updates-Packages.xz")
	if err != nil {
		t.Fatal(err)
	}
	blocks := compress(t, xz.WriterConfig{BlockSize: 4 << 10}, []byte(strings.Repeat(sample, 300)), noise(5000))
	for name, data := range map[string][]byte{"debian": debian, "several blocks": blocks} {
		step := 1
		if len(data) > 4000 {
			step = 7
		}
		for n := 0; n < len(data); n += step {
			if _, err := decode(data[:n], MaxDict); !errors.Is(err, ErrMalformed) {
				t.Fatalf("%s cut to %d of %d bytes: err = %v, want ErrMalformed", name, n, len(data), err)
			}
		}
	}
}

// An error from the source is still found in the error returned.
func TestSourceErrorIsKept(t *testing.T) {
	valid := compress(t, xz.WriterConfig{}, noise(50_000))
	boom := errors.New("response too large")
	for _, cut := range []int{0, 5, headerSize, headerSize + 3, 2000, len(valid) - 20, len(valid) - 3} {
		r := NewReader(io.MultiReader(bytes.NewReader(valid[:cut]), failing{boom}), MaxDict)
		if _, err := io.Copy(io.Discard, r); !errors.Is(err, boom) {
			t.Errorf("source failing after %d bytes: err = %v, want it to wrap the source's error", cut, err)
		}
	}
}

type failing struct{ err error }

func (f failing) Read([]byte) (int, error) { return 0, f.err }

func TestUnsupportedCheckIsNamed(t *testing.T) {
	data := compress(t, xz.WriterConfig{CheckSum: xz.CRC32}, []byte(sample))
	data[7] = 0x02
	binary.LittleEndian.PutUint32(data[8:], crc32.ChecksumIEEE(data[6:8]))
	if _, err := decode(data, MaxDict); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "unsupported check type") {
		t.Errorf("err = %v, want ErrMalformed naming the check type", err)
	}
}

func TestUvarint(t *testing.T) {
	for _, tt := range []struct {
		in   []byte
		want uint64
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7F, 0xFF}, 127, 1},
		{[]byte{0x80, 0x01}, 128, 2},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, 1<<63 - 1, 9},
		{[]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, 0, 0}, // ten bytes
		{[]byte{0x80}, 0, 0},
		{nil, 0, 0},
	} {
		if got, n := uvarint(tt.in); got != tt.want || n != tt.n {
			t.Errorf("uvarint(%x) = %d, %d; want %d, %d", tt.in, got, n, tt.want, tt.n)
		}
	}
}

// An index whose record count is an overlong integer is refused, here in a
// stream with no blocks, where the count would otherwise read as zero.
func TestOverlongIndexCount(t *testing.T) {
	some := compress(t, xz.WriterConfig{}, []byte(sample))
	// A stream with no blocks: the header, an index and a footer.
	stream := func(index []byte) []byte {
		for len(index)%4 != 0 {
			index = append(index, 0)
		}
		index = binary.LittleEndian.AppendUint32(index, crc32.ChecksumIEEE(index))
		footer := bytes.Clone(some[len(some)-footerSize:])
		binary.LittleEndian.PutUint32(footer[4:], uint32(len(index)/4-1))
		binary.LittleEndian.PutUint32(footer, crc32.ChecksumIEEE(footer[4:10]))
		return bytes.Join([][]byte{some[:headerSize], index, footer}, nil)
	}
	if got, err := decode(stream([]byte{0x00, 0x00}), MaxDict); err != nil || len(got) != 0 {
		t.Fatalf("a stream with no blocks: %q, %v", got, err)
	}
	overlong := append([]byte{0x00}, bytes.Repeat([]byte{0x80}, 9)...)
	if _, err := decode(stream(overlong), MaxDict); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "bad index") {
		t.Errorf("err = %v, want ErrMalformed for the index", err)
	}
}
