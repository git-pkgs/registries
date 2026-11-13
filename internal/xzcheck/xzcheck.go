// Package xzcheck bounds the memory an .xz file can make its decoder
// allocate, by reading the file's layout before it is decoded.
package xzcheck

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrMalformed is returned for a file that is not a well-formed .xz file,
// or whose blocks ask for too large a dictionary.
var ErrMalformed = errors.New("malformed xz file")

// MaxDict is the LZMA2 dictionary size callers allow by default. The
// ulikunitz/xz decoder allocates the dictionary a block header names, up to
// 4 GiB, and its own DictCap does not lower that (ulikunitz/xz#84). xz -9
// uses 64 MiB; Debian's indexes use 8 MiB.
const MaxDict = 64 << 20

const (
	xzHeaderSize = 12
	xzFooterSize = 12
	lzma2Filter  = 0x21
	maxVarint    = 9
)

var (
	xzHeaderMagic = []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	xzFooterMagic = []byte{'Y', 'Z'}

	errXZTruncated = errors.New("file ends early")

	// xzCheckSizes is the size of the check after each block, by the check
	// type in the stream flags.
	xzCheckSizes = [16]int{0, 4, 4, 4, 8, 8, 8, 16, 16, 16, 32, 32, 32, 64, 64, 64}
)

// Check checks every block of every stream in an .xz file for an LZMA2
// dictionary larger than maxDict, before the file is decoded.
//
// It reads the file front to back as the decoder does: each block header,
// then the LZMA2 chunks of the block, whose headers give their sizes, to
// where the next block header starts. The index at the end of a stream
// also lists the blocks, but the decoder reads a block header and
// allocates its dictionary before it compares the blocks with the index,
// so an index that leaves a block out would hide it from a check that
// trusted it.
func Check(data []byte, maxDict uint64) error {
	pos, streams := 0, 0
	for pos < len(data) {
		if streams > 0 && data[pos] == 0 {
			// Stream padding comes in multiples of four zero bytes.
			if len(data)-pos < 4 || !bytes.Equal(data[pos:pos+4], []byte{0, 0, 0, 0}) {
				return fmt.Errorf("%w: bad stream padding", ErrMalformed)
			}
			pos += 4
			continue
		}
		next, err := checkXZStream(data, pos, maxDict)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrMalformed, err)
		}
		pos = next
		streams++
	}
	if streams == 0 {
		return fmt.Errorf("%w: no stream", ErrMalformed)
	}
	return nil
}

// checkXZStream checks the stream that starts at pos and returns where it
// ends.
func checkXZStream(data []byte, pos int, maxDict uint64) (int, error) {
	if len(data)-pos < xzHeaderSize {
		return 0, errXZTruncated
	}
	header := data[pos : pos+xzHeaderSize]
	if !bytes.Equal(header[:len(xzHeaderMagic)], xzHeaderMagic) {
		return 0, errors.New("no stream header")
	}
	checkSize := xzCheckSizes[header[7]&0x0F]
	pos += xzHeaderSize

	for {
		if pos >= len(data) {
			return 0, errXZTruncated
		}
		if data[pos] == 0 {
			break // the index indicator: no more blocks
		}
		next, err := checkXZBlock(data, pos, checkSize, maxDict)
		if err != nil {
			return 0, err
		}
		pos = next
	}

	pos, err := skipXZIndex(data, pos)
	if err != nil {
		return 0, err
	}
	if len(data)-pos < xzFooterSize {
		return 0, errXZTruncated
	}
	if !bytes.Equal(data[pos+10:pos+xzFooterSize], xzFooterMagic) {
		return 0, errors.New("no stream footer")
	}
	return pos + xzFooterSize, nil
}

// checkXZBlock checks the header of the block that starts at pos and
// returns where the block ends: after its LZMA2 chunks, padding and check.
func checkXZBlock(data []byte, pos, checkSize int, maxDict uint64) (int, error) {
	start := pos
	headerSize := (int(data[pos]) + 1) * 4
	if len(data)-pos < headerSize {
		return 0, errXZTruncated
	}
	if err := checkXZBlockHeader(data[pos:pos+headerSize], maxDict); err != nil {
		return 0, err
	}
	pos += headerSize

	// LZMA2 is the last filter of every block, so the block's data is a
	// run of LZMA2 chunks up to an end marker.
	for {
		if pos >= len(data) {
			return 0, errXZTruncated
		}
		control := data[pos]
		if control == 0 {
			pos++
			break
		}
		var chunk int
		switch {
		case control == 1 || control == 2: // stored: control, size
			if len(data)-pos < 3 {
				return 0, errXZTruncated
			}
			chunk = 3 + int(binary.BigEndian.Uint16(data[pos+1:])) + 1
		case control >= 0x80: // LZMA: control, sizes, properties on a reset
			if len(data)-pos < 5 {
				return 0, errXZTruncated
			}
			chunk = 5 + int(binary.BigEndian.Uint16(data[pos+3:])) + 1
			if control >= 0xC0 {
				chunk++
			}
		default:
			return 0, errors.New("bad LZMA2 chunk")
		}
		// A chunk running past the end is caught by the next read.
		pos += chunk
	}

	// The block is padded to four bytes, then comes its check.
	return pos + (4-(pos-start)%4)%4 + checkSize, nil
}

// skipXZIndex returns where the index that starts at pos ends.
func skipXZIndex(data []byte, pos int) (int, error) {
	start := pos
	pos++ // the indicator
	// A malformed count reads as no records, which the footer then fails.
	count, n := readVarint(data[pos:])
	pos += n
	for ; count > 0; count-- {
		for range 2 { // unpadded and uncompressed size
			_, n := readVarint(data[pos:])
			if n == 0 {
				return 0, errors.New("bad index record")
			}
			pos += n
		}
	}
	// Padding to four bytes, then the CRC32.
	return pos + (4-(pos-start)%4)%4 + 4, nil
}

// checkXZBlockHeader reads the filters of a block header.
func checkXZBlockHeader(block []byte, maxDict uint64) error {
	// Without the size byte and the CRC32; a header is at least 8 bytes.
	header := block[1 : len(block)-4]
	flags := header[0]
	pos := 1
	for _, present := range []byte{0x40, 0x80} { // compressed and uncompressed sizes
		if flags&present != 0 {
			_, n := readVarint(header[pos:])
			if n == 0 {
				return errors.New("bad block header")
			}
			pos += n
		}
	}
	last := uint64(0)
	for range int(flags&0x03) + 1 {
		id, n := readVarint(header[pos:])
		if n == 0 {
			return errors.New("bad filter flags")
		}
		pos += n
		propsSize, n := readVarint(header[pos:])
		if n == 0 || uint64(len(header)-pos-n) < propsSize {
			return errors.New("bad filter flags")
		}
		pos += n
		props := header[pos : pos+int(propsSize)]
		pos += int(propsSize)
		last = id
		if id != lzma2Filter {
			continue
		}
		if len(props) != 1 || props[0] > 40 {
			return errors.New("bad LZMA2 properties")
		}
		if dict := lzma2DictSize(props[0]); dict > maxDict {
			return fmt.Errorf("LZMA2 dictionary of %d bytes is larger than %d", dict, maxDict)
		}
	}
	if last != lzma2Filter {
		return errors.New("last filter is not LZMA2")
	}
	return nil
}

// lzma2DictSize decodes the LZMA2 dictionary size property, 0 to 40.
func lzma2DictSize(bits byte) uint64 {
	if bits == 40 {
		return 1<<32 - 1
	}
	return uint64(2|bits&1) << (bits/2 + 11)
}

// readVarint reads an xz multibyte integer, returning 0 bytes read when it
// is malformed or truncated.
func readVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < maxVarint; i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}
