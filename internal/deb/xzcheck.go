package deb

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// maxXZDict bounds the LZMA2 dictionary an index may ask for. The decoder
// allocates the dictionary a block header names, up to 4 GiB, and its own
// DictCap does not lower that (ulikunitz/xz#84). Debian uses 8 MiB.
const maxXZDict = 64 << 20

const (
	xzHeaderSize = 12
	xzFooterSize = 12
	lzma2Filter  = 0x21
	maxVarint    = 9
)

var (
	xzHeaderMagic = []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	xzFooterMagic = []byte{'Y', 'Z'}
)

// checkXZ checks every block of every stream in an .xz file for an LZMA2
// dictionary larger than maxDict, before the file is decoded. Streams are
// found from the end, through each stream's footer and index, so blocks
// need not be decoded to be found.
func checkXZ(data []byte, maxDict uint64) error {
	end := len(data)
	streams := 0
	for {
		// Stream padding is a multiple of four zero bytes.
		for end >= 4 && bytes.Equal(data[end-4:end], []byte{0, 0, 0, 0}) {
			end -= 4
		}
		if end == 0 {
			break
		}
		start, err := checkXZStream(data[:end], maxDict)
		if err != nil {
			return fmt.Errorf("%w: xz: %w", ErrMalformedIndex, err)
		}
		end = start
		streams++
	}
	if streams == 0 {
		return fmt.Errorf("%w: xz: no stream", ErrMalformedIndex)
	}
	return nil
}

// checkXZStream checks the stream that ends data and returns its offset.
func checkXZStream(data []byte, maxDict uint64) (int, error) {
	if len(data) < xzHeaderSize+xzFooterSize {
		return 0, fmt.Errorf("stream too short")
	}
	footer := data[len(data)-xzFooterSize:]
	if !bytes.Equal(footer[10:], xzFooterMagic) {
		return 0, fmt.Errorf("no stream footer")
	}
	indexSize := (int(binary.LittleEndian.Uint32(footer[4:8])) + 1) * 4
	indexStart := len(data) - xzFooterSize - indexSize
	if indexStart < 0 {
		return 0, fmt.Errorf("index outside the stream")
	}
	blocks, err := xzIndexBlocks(data[indexStart:len(data)-xzFooterSize], indexStart)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, size := range blocks {
		total += size
	}
	start := indexStart - total - xzHeaderSize
	if start < 0 || !bytes.Equal(data[start:start+len(xzHeaderMagic)], xzHeaderMagic) {
		return 0, fmt.Errorf("no stream header")
	}
	offset := start + xzHeaderSize
	for _, size := range blocks {
		if err := checkXZBlockHeader(data[offset:offset+size], maxDict); err != nil {
			return 0, err
		}
		offset += size
	}
	return start, nil
}

// xzIndexBlocks reads an xz index and returns the space each block takes,
// its unpadded size rounded up to four bytes. No block is larger than
// limit, the bytes before the index.
func xzIndexBlocks(index []byte, limit int) ([]int, error) {
	if len(index) == 0 || index[0] != 0 {
		return nil, fmt.Errorf("no index")
	}
	pos := 1
	count, n := readVarint(index[pos:])
	if n == 0 {
		return nil, fmt.Errorf("bad index")
	}
	pos += n
	// Each record is at least two bytes, which bounds count.
	if count > uint64(len(index)) {
		return nil, fmt.Errorf("bad index")
	}
	blocks := make([]int, 0, count)
	for range count {
		unpadded, n := readVarint(index[pos:])
		// Bounded so the sizes below cannot overflow int.
		if n == 0 || unpadded > uint64(limit) {
			return nil, fmt.Errorf("bad index record")
		}
		pos += n
		if _, n = readVarint(index[pos:]); n == 0 {
			return nil, fmt.Errorf("bad index record")
		}
		pos += n
		blocks = append(blocks, int((unpadded+3)&^3))
	}
	return blocks, nil
}

// checkXZBlockHeader reads the filters of the block that starts block.
func checkXZBlockHeader(block []byte, maxDict uint64) error {
	if len(block) == 0 || block[0] == 0 {
		return fmt.Errorf("no block header")
	}
	size := (int(block[0]) + 1) * 4
	if size > len(block) {
		return fmt.Errorf("block header past the block")
	}
	header := block[1 : size-4] // without the size byte and the CRC32
	if len(header) == 0 {
		return fmt.Errorf("short block header")
	}
	flags := header[0]
	pos := 1
	for _, present := range []byte{0x40, 0x80} { // compressed and uncompressed sizes
		if flags&present != 0 {
			_, n := readVarint(header[pos:])
			if n == 0 {
				return fmt.Errorf("bad block header")
			}
			pos += n
		}
	}
	for range int(flags&0x03) + 1 {
		id, n := readVarint(header[pos:])
		if n == 0 {
			return fmt.Errorf("bad filter flags")
		}
		pos += n
		propsSize, n := readVarint(header[pos:])
		if n == 0 || uint64(len(header)-pos-n) < propsSize {
			return fmt.Errorf("bad filter flags")
		}
		pos += n
		props := header[pos : pos+int(propsSize)]
		pos += int(propsSize)
		if id != lzma2Filter {
			continue
		}
		if len(props) != 1 || props[0] > 40 {
			return fmt.Errorf("bad LZMA2 properties")
		}
		if dict := lzma2DictSize(props[0]); dict > maxDict {
			return fmt.Errorf("LZMA2 dictionary of %d bytes is larger than %d", dict, maxDict)
		}
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
