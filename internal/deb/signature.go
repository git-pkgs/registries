package deb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
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
)

const clearsignedBegin = "-----BEGIN PGP SIGNED MESSAGE-----"

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

func readKeyring(data []byte) (openpgp.EntityList, error) {
	if bytes.Contains(data, []byte("-----BEGIN PGP")) {
		return openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	}
	return openpgp.ReadKeyRing(bytes.NewReader(data))
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
	if _, err := block.VerifySignature(r.keyring, nil); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	return block.Plaintext, nil
}

// verifyDetached checks a Release.gpg, armored or binary, over text.
func (r *Registry) verifyDetached(text, signature []byte) error {
	var err error
	if bytes.HasPrefix(bytes.TrimSpace(signature), []byte("-----BEGIN PGP SIGNATURE-----")) {
		_, err = openpgp.CheckArmoredDetachedSignature(r.keyring, bytes.NewReader(text), bytes.NewReader(signature), nil)
	} else {
		_, err = openpgp.CheckDetachedSignature(r.keyring, bytes.NewReader(text), bytes.NewReader(signature), nil)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	return nil
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
	return rel, nil
}
