// Package xzlimit decodes .xz files with a bound on the memory a file can
// make the decoder allocate.
//
// An .xz block header names the LZMA2 dictionary its data needs, up to
// 4 GiB, and github.com/ulikunitz/xz allocates what it names: its DictCap
// is a minimum, not a limit (ulikunitz/xz#84). The header cannot be checked
// ahead of decoding either, since where a block's data ends is only known
// by decoding it. So this package reads the .xz container itself, refuses
// a block whose dictionary is over the limit as it reaches it, and hands
// the block's data to ulikunitz/xz's LZMA2 decoder with exactly the
// dictionary the block asked for.
package xzlimit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

// ErrMalformed is returned for a file that is not a well-formed .xz file
// this package can read, or whose blocks ask for too large a dictionary.
var ErrMalformed = errors.New("malformed xz file")

// MaxDict is the dictionary size callers allow by default. xz -9 uses
// 64 MiB; Debian's indexes use 8 MiB.
const MaxDict = 64 << 20

const (
	headerSize  = 12
	footerSize  = 12
	lzma2Filter = 0x21
	maxVarint   = 9

	checkNone   = 0x00
	checkCRC32  = 0x01
	checkCRC64  = 0x04
	checkSHA256 = 0x0A
)

var (
	headerMagic = []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	footerMagic = []byte{'Y', 'Z'}
	crc64Table  = crc64.MakeTable(crc64.ECMA)
)

// Reader decodes an .xz file: one or more streams, each of blocks whose
// only filter is LZMA2, as xz writes them by default.
type Reader struct {
	src     *source
	maxDict uint64
	err     error

	streams   int
	inStream  bool
	checkType byte
	flags     [2]byte
	records   []record // the blocks of the current stream

	// The block being decoded.
	block      io.Reader
	check      hash.Hash
	blockStart int64 // offset of the block header
	produced   int64
}

// record is what the index lists for a block.
type record struct {
	unpadded     int64
	uncompressed int64
}

// NewReader returns a Reader that refuses any block asking for a
// dictionary larger than maxDict bytes.
func NewReader(r io.Reader, maxDict uint64) *Reader {
	return &Reader{src: &source{r: bufio.NewReader(r)}, maxDict: maxDict}
}

// source counts the bytes read, which gives the size of each block. It is
// an io.ByteReader so the LZMA2 decoder reads no further than it decodes.
type source struct {
	r *bufio.Reader
	n int64
}

func (s *source) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.n += int64(n)
	return n, err
}

func (s *source) ReadByte() (byte, error) {
	b, err := s.r.ReadByte()
	if err == nil {
		s.n++
	}
	return b, err
}

// full reads exactly n bytes; the file ending inside them is malformed.
func (s *source) full(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(s, buf); err != nil {
		return nil, early(err)
	}
	return buf, nil
}

// early turns the end of the file, where more was expected, into an error.
func early(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for r.err == nil {
		if r.block == nil {
			r.err = r.next()
			continue
		}
		n, err := r.block.Read(p)
		if n > 0 {
			_, _ = r.check.Write(p[:n])
			r.produced += int64(n)
			return n, nil
		}
		if err == io.EOF {
			err = r.finishBlock()
		}
		r.err = err
	}
	if r.err == io.EOF {
		return 0, io.EOF
	}
	// Both are kept: a caller may look for the source's error, such as a
	// size limit, as well as for a malformed file.
	if !errors.Is(r.err, ErrMalformed) {
		r.err = fmt.Errorf("%w: %w", ErrMalformed, r.err)
	}
	return 0, r.err
}

// next reads up to the next block's data: a stream header, a block header,
// or the index and footer that end a stream. It returns io.EOF at the end
// of the file.
func (r *Reader) next() error {
	if !r.inStream {
		return r.startStream()
	}
	first, err := r.src.ReadByte()
	if err != nil {
		return early(err)
	}
	if first == 0 {
		return r.endStream()
	}
	return r.startBlock(first)
}

// startStream skips stream padding and reads a stream header.
func (r *Reader) startStream() error {
	for {
		peek, err := r.src.r.Peek(1)
		if err == io.EOF && r.streams > 0 {
			return io.EOF
		}
		if err != nil {
			return early(err)
		}
		if peek[0] != 0 || r.streams == 0 {
			break
		}
		// Stream padding comes in multiples of four zero bytes.
		padding, err := r.src.full(4)
		if err != nil {
			return err
		}
		if !bytes.Equal(padding, []byte{0, 0, 0, 0}) {
			return errors.New("bad stream padding")
		}
	}
	header, err := r.src.full(headerSize)
	if err != nil {
		return err
	}
	if !bytes.Equal(header[:6], headerMagic) {
		return errors.New("no stream header")
	}
	if crc32.ChecksumIEEE(header[6:8]) != binary.LittleEndian.Uint32(header[8:]) {
		return errors.New("stream header checksum")
	}
	if header[6] != 0 {
		return errors.New("unsupported stream flags")
	}
	switch header[7] {
	case checkNone, checkCRC32, checkCRC64, checkSHA256:
	default:
		return fmt.Errorf("unsupported check type %#x", header[7])
	}
	r.flags = [2]byte{header[6], header[7]}
	r.checkType = header[7]
	r.records = r.records[:0]
	r.inStream = true
	r.streams++
	return nil
}

// startBlock reads the block header whose first byte is sizeByte and
// starts decoding the block's data.
func (r *Reader) startBlock(sizeByte byte) error {
	r.blockStart = r.src.n - 1
	size := (int(sizeByte) + 1) * 4
	rest, err := r.src.full(size - 1)
	if err != nil {
		return err
	}
	header := append([]byte{sizeByte}, rest...)
	if crc32.ChecksumIEEE(header[:size-4]) != binary.LittleEndian.Uint32(header[size-4:]) {
		return errors.New("block header checksum")
	}
	dict, err := blockDictSize(header[1 : size-4])
	if err != nil {
		return err
	}
	if dict > r.maxDict {
		return fmt.Errorf("block asks for a dictionary of %d bytes, more than %d", dict, r.maxDict)
	}
	// Exactly what the block asked for, never more. The smallest size the
	// format can name, 4096, is also the decoder's minimum.
	block, err := lzma.Reader2Config{DictCap: int(dict)}.NewReader2(r.src)
	if err != nil {
		return err
	}
	r.block = block
	r.check = newCheck(r.checkType)
	r.produced = 0
	return nil
}

// blockDictSize reads a block header's fields, without its size byte and
// checksum, and returns the dictionary size of its LZMA2 filter.
func blockDictSize(fields []byte) (uint64, error) {
	flags := fields[0]
	if flags&0x3C != 0 {
		return 0, errors.New("reserved block flags")
	}
	if flags&0x03 != 0 {
		return 0, errors.New("unsupported filter chain: more than one filter")
	}
	pos := 1
	for _, present := range []byte{0x40, 0x80} { // compressed and uncompressed sizes
		if flags&present != 0 {
			_, n := uvarint(fields[pos:])
			if n == 0 {
				return 0, errors.New("bad block header")
			}
			pos += n
		}
	}
	// One filter: LZMA2, with one byte of properties.
	if len(fields)-pos < 3 || fields[pos] != lzma2Filter || fields[pos+1] != 1 {
		return 0, errors.New("unsupported filter chain: not LZMA2")
	}
	bits := fields[pos+2]
	if bits > 40 {
		return 0, errors.New("bad LZMA2 properties")
	}
	for _, b := range fields[pos+3:] {
		if b != 0 {
			return 0, errors.New("bad block header padding")
		}
	}
	return dictSize(bits), nil
}

// dictSize decodes the LZMA2 dictionary size property, 0 to 40.
func dictSize(bits byte) uint64 {
	if bits == 40 {
		return 1<<32 - 1
	}
	return uint64(2|bits&1) << (bits/2 + 11)
}

func newCheck(checkType byte) hash.Hash {
	switch checkType {
	case checkCRC32:
		return crc32.NewIEEE()
	case checkCRC64:
		return crc64.New(crc64Table)
	case checkSHA256:
		return sha256.New()
	}
	return noCheck{}
}

// noCheck is the check of a stream that has none.
type noCheck struct{ hash.Hash }

func (noCheck) Write(p []byte) (int, error) { return len(p), nil }
func (noCheck) Sum([]byte) []byte           { return nil }

// finishBlock reads what follows a block's data: its padding and check.
func (r *Reader) finishBlock() error {
	unpadded := r.src.n - r.blockStart
	if padding := int((4 - unpadded%4) % 4); padding > 0 {
		zeros, err := r.src.full(padding)
		if err != nil {
			return err
		}
		if !bytes.Equal(zeros, make([]byte, padding)) {
			return errors.New("bad block padding")
		}
	}
	want := r.check.Sum(nil)
	// CRCs are stored little-endian, and hash.Hash gives them big-endian.
	if r.checkType == checkCRC32 || r.checkType == checkCRC64 {
		for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
			want[i], want[j] = want[j], want[i]
		}
	}
	got, err := r.src.full(len(want))
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("block check mismatch")
	}
	r.records = append(r.records, record{unpadded: unpadded + int64(len(want)), uncompressed: r.produced})
	r.block = nil
	return nil
}

// endStream reads the index, whose indicator byte has been read, and the
// stream footer, and checks them against the blocks that were decoded.
func (r *Reader) endStream() error {
	indexStart := r.src.n - 1
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte{0})
	in := io.TeeReader(r.src, crc)
	readUvarint := func() (uint64, error) {
		var buf [maxVarint]byte
		for i := range buf {
			if _, err := io.ReadFull(in, buf[i:i+1]); err != nil {
				return 0, early(err)
			}
			if buf[i]&0x80 == 0 {
				v, _ := uvarint(buf[:i+1])
				return v, nil
			}
		}
		return 0, errors.New("bad index")
	}

	count, err := readUvarint()
	if err != nil {
		return err
	}
	if count != uint64(len(r.records)) {
		return fmt.Errorf("index lists %d blocks, the stream has %d", count, len(r.records))
	}
	for _, rec := range r.records {
		unpadded, err := readUvarint()
		if err != nil {
			return err
		}
		uncompressed, err := readUvarint()
		if err != nil {
			return err
		}
		if unpadded != uint64(rec.unpadded) || uncompressed != uint64(rec.uncompressed) {
			return errors.New("index does not match the blocks")
		}
	}
	if padding := int((4 - (r.src.n-indexStart)%4) % 4); padding > 0 {
		zeros := make([]byte, padding)
		if _, err := io.ReadFull(in, zeros); err != nil {
			return early(err)
		}
		if !bytes.Equal(zeros, make([]byte, padding)) {
			return errors.New("bad index padding")
		}
	}
	sum := crc.Sum32()
	stored, err := r.src.full(4)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(stored) != sum {
		return errors.New("index checksum")
	}
	indexSize := r.src.n - indexStart

	footer, err := r.src.full(footerSize)
	if err != nil {
		return err
	}
	switch {
	case !bytes.Equal(footer[10:], footerMagic):
		return errors.New("no stream footer")
	case crc32.ChecksumIEEE(footer[4:10]) != binary.LittleEndian.Uint32(footer):
		return errors.New("stream footer checksum")
	case int64(binary.LittleEndian.Uint32(footer[4:8])+1)*4 != indexSize:
		return errors.New("stream footer does not match the index")
	case footer[8] != r.flags[0] || footer[9] != r.flags[1]:
		return errors.New("stream footer does not match the header")
	}
	r.inStream = false
	return nil
}

// uvarint decodes an xz multibyte integer, returning 0 bytes read when it
// is malformed or truncated.
func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < maxVarint; i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}
