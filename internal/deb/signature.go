package deb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/git-pkgs/registries/internal/core"
)

var (
	// ErrUnsigned is returned, for an archive with a keyring, when a suite
	// has no signed InRelease and no Release with a Release.gpg.
	ErrUnsigned = errors.New("debian Release is not signed")
	// ErrBadSignature is returned, for an archive with a keyring, when a
	// Release signature isn't a valid one by a key in the keyring.
	ErrBadSignature = errors.New("debian Release signature does not verify")
	// ErrExpiredRelease is returned, for an archive with a keyring, when a
	// signed Release's Valid-Until has passed, so an old one can't be
	// replayed.
	ErrExpiredRelease = errors.New("debian Release has expired")
)

const (
	clearsignedBegin = "-----BEGIN PGP SIGNED MESSAGE-----"
	armorBegin       = "-----BEGIN PGP "
)

// now is the clock Valid-Until is checked against.
var now = time.Now

// archiveRoot is an archive URL without its query, fragment and trailing
// slash, as a Registry and its keyring are matched on.
func archiveRoot(u *url.URL) string {
	root := *u
	root.RawQuery, root.Fragment = "", ""
	return strings.TrimSuffix(root.String(), "/")
}

// keyringFor reads the keyrings the client has for archiveURL, nil if it
// has none.
func keyringFor(c *core.Client, archiveURL string) (openpgp.EntityList, error) {
	var keyring openpgp.EntityList
	found := false
	for root, data := range c.DebianKeyrings {
		u, err := url.Parse(root)
		if err != nil || archiveRoot(u) != archiveURL {
			continue
		}
		found = true
		keys, err := readKeyring(data)
		if err != nil {
			return nil, fmt.Errorf("%w: keyring for %s: %w", ErrInvalidConfig, archiveURL, err)
		}
		keyring = append(keyring, keys...)
	}
	if found && len(keyring) == 0 {
		return nil, fmt.Errorf("%w: keyring for %s has no keys", ErrInvalidConfig, archiveURL)
	}
	return keyring, nil
}

// readKeyring reads a binary keyring, or one or more armored blocks, as
// a keyring made by concatenating .asc files has.
func readKeyring(data []byte) (openpgp.EntityList, error) {
	if !bytes.Contains(data, []byte(armorBegin)) {
		return openpgp.ReadKeyRing(bytes.NewReader(data))
	}
	var keyring openpgp.EntityList
	for _, block := range armoredBlocks(data) {
		keys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(block))
		if err != nil {
			return nil, err
		}
		keyring = append(keyring, keys...)
	}
	return keyring, nil
}

// armoredBlocks splits data at each armor header line. armor.Decode may
// read past the end of its block, so each is decoded from its own slice.
func armoredBlocks(data []byte) [][]byte {
	var blocks [][]byte
	start := bytes.Index(data, []byte(armorBegin))
	for start >= 0 {
		next := bytes.Index(data[start+len(armorBegin):], []byte(armorBegin))
		if next < 0 {
			return append(blocks, data[start:])
		}
		end := start + len(armorBegin) + next
		blocks = append(blocks, data[start:end])
		start = end
	}
	return blocks
}

// fetchSignedRelease reads InRelease, which must be clearsigned by a key
// in the keyring, falling back to Release, which must have a Release.gpg
// by one. Only the signed text is read.
func (r *Registry) fetchSignedRelease(ctx context.Context, suite string) (*release, error) {
	dist := r.archiveURL + "/dists/" + suite + "/"
	body, err := r.client.GetBody(ctx, dist+"InRelease")
	switch {
	case err == nil:
		text, err := r.verifyInRelease(body)
		if err != nil {
			return nil, fmt.Errorf("%sInRelease: %w", dist, err)
		}
		return signedRelease(dist+"InRelease", text)
	case !isNotFound(err):
		return nil, err
	}
	body, err = r.client.GetBody(ctx, dist+"Release")
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %s has neither InRelease nor Release", ErrUnsigned, dist)
	}
	if err != nil {
		return nil, err
	}
	signature, err := r.client.GetBody(ctx, dist+"Release.gpg")
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %sRelease has no Release.gpg", ErrUnsigned, dist)
	}
	if err != nil {
		return nil, err
	}
	if err := r.verifyDetached(body, signature); err != nil {
		return nil, fmt.Errorf("%sRelease: %w", dist, err)
	}
	return signedRelease(dist+"Release", body)
}

// verifyInRelease returns the text of a clearsigned InRelease once its
// signature is checked. Text before the message isn't signed, so a
// message that doesn't start the file is refused.
func (r *Registry) verifyInRelease(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(clearsignedBegin)) {
		return nil, fmt.Errorf("%w: not a clearsigned message", ErrUnsigned)
	}
	block, _ := clearsign.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: malformed clearsigned message", ErrUnsigned)
	}
	signature, err := io.ReadAll(block.ArmoredSignature.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	if err := r.verifyAny(block.Bytes, signature); err != nil {
		return nil, err
	}
	return block.Plaintext, nil
}

// verifyDetached checks a Release.gpg, armored or binary, over text.
func (r *Registry) verifyDetached(text, signature []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(signature), []byte("-----BEGIN PGP SIGNATURE-----")) {
		block, err := armor.Decode(bytes.NewReader(signature))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBadSignature, err)
		}
		if signature, err = io.ReadAll(block.Body); err != nil {
			return fmt.Errorf("%w: %w", ErrBadSignature, err)
		}
	}
	return r.verifyAny(text, signature)
}

// verifyAny accepts text when any of the signature packets in signature
// verifies, as apt does. Debian signs each Release with several keys, and
// go-crypto checks only the first signature whose issuer is in the
// keyring, which may be one by an expired or revoked key.
func (r *Registry) verifyAny(text, signature []byte) error {
	packets, err := signaturePackets(signature)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	if len(packets) == 0 {
		return fmt.Errorf("%w: no signature", ErrBadSignature)
	}
	var errs []error
	for _, p := range packets {
		_, _, err := openpgp.VerifyDetachedSignature(r.keyring, bytes.NewReader(text), bytes.NewReader(p), nil)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("%w: %w", ErrBadSignature, errors.Join(errs...))
}

// signaturePackets splits a binary OpenPGP message into its packets,
// reading only their headers (RFC 9580 section 4.2). Partial body
// lengths, which signatures don't use, are refused.
func signaturePackets(data []byte) ([][]byte, error) {
	var packets [][]byte
	for len(data) > 0 {
		header, length, err := packetLength(data)
		if err != nil {
			return nil, err
		}
		if length < 0 || length > len(data)-header {
			return nil, errors.New("truncated signature packet")
		}
		packets = append(packets, data[:header+length])
		data = data[header+length:]
	}
	return packets, nil
}

// packetLength reads the header of the packet data starts with, returning
// the header's length and the body's.
func packetLength(data []byte) (header, length int, err error) {
	tag := data[0]
	if tag&0x80 == 0 {
		return 0, 0, errors.New("not an OpenPGP packet")
	}
	if tag&0x40 == 0 {
		// Old format: the low two bits give the length's size.
		size := []int{1, 2, 4}
		if tag&3 == 3 {
			return 1, len(data) - 1, nil
		}
		return bigEndian(data, 1, size[tag&3])
	}
	if len(data) < 2 {
		return 0, 0, errors.New("truncated packet header")
	}
	switch first := int(data[1]); {
	case first < 192:
		return 2, first, nil
	case first < 224:
		if len(data) < 3 {
			return 0, 0, errors.New("truncated packet header")
		}
		return 3, (first-192)<<8 + int(data[2]) + 192, nil
	case first == 255:
		return bigEndian(data, 2, 4)
	default:
		return 0, 0, errors.New("partial body length in a signature")
	}
}

// bigEndian reads a size-byte length at offset, returning the header's
// length up to and including it.
func bigEndian(data []byte, offset, size int) (header, length int, err error) {
	if len(data) < offset+size {
		return 0, 0, errors.New("truncated packet header")
	}
	for _, b := range data[offset : offset+size] {
		length = length<<8 | int(b)
	}
	return offset + size, length, nil
}

// signedRelease parses a signed Release text. One without SHA256 entries
// would leave its indexes unchecked, which a keyring is set to prevent.
func signedRelease(releaseURL string, text []byte) (*release, error) {
	rel, err := parseReleaseText(text)
	if err != nil {
		return nil, err
	}
	if len(rel.files) == 0 {
		return nil, fmt.Errorf("%w: %s lists no SHA256 sums", ErrMalformedIndex, releaseURL)
	}
	if rel.validUntil != "" {
		until, err := parseReleaseDate(rel.validUntil)
		if err != nil {
			return nil, fmt.Errorf("%w: %s Valid-Until %q", ErrMalformedIndex, releaseURL, rel.validUntil)
		}
		if now().After(until) {
			return nil, fmt.Errorf("%w: %s was valid until %s", ErrExpiredRelease, releaseURL, rel.validUntil)
		}
	}
	return rel, nil
}

// parseReleaseDate reads a Release date as apt writes it ("Sat, 30 Nov
// 2026 08:12:45 UTC"), or with a numeric zone.
func parseReleaseDate(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC1123, value)
	if err != nil {
		t, err = time.Parse(time.RFC1123Z, value)
	}
	return t, err
}
