package deb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/git-pkgs/registries/client"
)

var testKeyConfig = &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA}

func newKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity("Archive Signing Key", "", "archive@example.org", testKeyConfig)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// armoredPublicKey is e's public key as an armored keyring.
func armoredPublicKey(t *testing.T, e *openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Serialize(w); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func binaryPublicKey(t *testing.T, e *openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := e.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func clearsigned(t *testing.T, e *openpgp.Entity, text []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, e.PrivateKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(text); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func detachSigned(t *testing.T, e *openpgp.Entity, text []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, e, bytes.NewReader(text), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// signedArchive is one suite whose Release text is unsigned for the test
// to sign as it needs. It returns the Release text and the archive root.
func signedArchive(t *testing.T) (*archive, []byte, string) {
	a := newArchive(t)
	a.index("bookworm", "main/binary-amd64", "Packages.xz", bookwormAMD64)
	a.release("bookworm", "bookworm", false)
	text := a.served["dists/bookworm/Release"]
	delete(a.served, "dists/bookworm/Release")
	return a, text, a.server().URL + "/debian"
}

func keyringRegistry(base string, keyring []byte) *Registry {
	return New(base+"?suites=bookworm&architectures=amd64", client.NewClient(client.WithDebianKeyring(base, keyring)))
}

func fetchCurl(r *Registry) error {
	_, err := r.FetchVersions(context.Background(), "curl")
	return err
}

func TestSignedInRelease(t *testing.T) {
	key := newKey(t)
	for name, keyring := range map[string][]byte{"armored": armoredPublicKey(t, key), "binary": binaryPublicKey(t, key)} {
		t.Run(name, func(t *testing.T) {
			a, text, base := signedArchive(t)
			a.served["dists/bookworm/InRelease"] = clearsigned(t, key, text)
			if err := fetchCurl(keyringRegistry(base, keyring)); err != nil {
				t.Fatalf("FetchVersions: %v", err)
			}
		})
	}
}

func TestInReleaseSignedByAnotherKey(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, newKey(t), text)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestTamperedInRelease(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	signed := clearsigned(t, key, text)
	a.served["dists/bookworm/InRelease"] = bytes.Replace(signed, []byte("Codename: bookworm"), []byte("Codename: trixie"), 1)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key)))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// The text before a clearsigned message isn't covered by its signature,
// so a Release with any is refused rather than parsed without it.
func TestInReleaseWithLeadingText(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = append([]byte("Codename: trixie\n\n"), clearsigned(t, key, text)...)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key)))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

// Only the signed text is read: a second Release after the signature
// can't add or replace entries.
func TestInReleaseReadsOnlySignedText(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	forged := strings.Replace(string(text), "Codename: bookworm", "Codename: trixie", 1)
	a.served["dists/bookworm/InRelease"] = append(clearsigned(t, key, text), []byte("\n"+forged)...)
	r := keyringRegistry(base, armoredPublicKey(t, key))
	versions, err := r.FetchVersions(context.Background(), "curl")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if d := versions[0].Artifacts[0].Metadata["distro"]; d != "bookworm" {
		t.Errorf("distro = %v, want bookworm from the signed text", d)
	}
}

func TestUnsignedInReleaseRefused(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = text
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

func TestReleaseWithDetachedSignature(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = detachSigned(t, key, text)
	if err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key))); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestReleaseWithBinaryDetachedSignature(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	var sig bytes.Buffer
	if err := openpgp.DetachSign(&sig, key, bytes.NewReader(text), nil); err != nil {
		t.Fatal(err)
	}
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = sig.Bytes()
	if err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key))); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestReleaseDetachedSignatureByAnotherKey(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = detachSigned(t, newKey(t), text)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestTamperedReleaseWithDetachedSignature(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release.gpg"] = detachSigned(t, key, text)
	a.served["dists/bookworm/Release"] = bytes.Replace(text, []byte("Codename: bookworm"), []byte("Codename: trixie"), 1)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key)))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestReleaseWithoutSignatureRefused(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

func TestSuiteWithoutReleaseRefusedWithKeyring(t *testing.T) {
	_, _, base := signedArchive(t)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

// A keyring applies to its own archive only, matched on the root without
// a trailing slash or query.
func TestKeyringForAnotherArchive(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	c := client.NewClient(client.WithDebianKeyring(base+"-security", armoredPublicKey(t, newKey(t))))
	if err := fetchCurl(New(base+"?suites=bookworm&architectures=amd64", c)); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestKeyringArchiveURLNormalised(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	c := client.NewClient(client.WithDebianKeyring(base+"/?suites=other", armoredPublicKey(t, newKey(t))))
	err := fetchCurl(New(base+"/?suites=bookworm&architectures=amd64", c))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

func TestInvalidKeyring(t *testing.T) {
	for name, keyring := range map[string][]byte{"garbage": []byte("not a key"), "empty": nil} {
		t.Run(name, func(t *testing.T) {
			_, _, base := signedArchive(t)
			err := fetchCurl(keyringRegistry(base, keyring))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

// A signed Release without SHA256 entries would leave the indexes
// unchecked.
func TestSignedReleaseWithoutSHA256(t *testing.T) {
	key := newKey(t)
	a, _, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, key, []byte("Origin: Debian\nCodename: bookworm\n"))
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, key)))
	if !errors.Is(err, ErrMalformedIndex) {
		t.Fatalf("err = %v, want ErrMalformedIndex", err)
	}
}

func TestClearsignedInReleaseWithoutSignature(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = append([]byte(clearsignedBegin+"\nHash: SHA256\n\n"), text...)
	err := fetchCurl(keyringRegistry(base, armoredPublicKey(t, newKey(t))))
	if !errors.Is(err, ErrUnsigned) {
		t.Fatalf("err = %v, want ErrUnsigned", err)
	}
}

// Keyrings given for the same archive under URLs that differ only in a
// trailing slash are both used.
func TestKeyringsForOneArchiveCombined(t *testing.T) {
	first, second := newKey(t), newKey(t)
	for _, key := range []*openpgp.Entity{first, second} {
		a, text, base := signedArchive(t)
		a.served["dists/bookworm/InRelease"] = clearsigned(t, key, text)
		c := client.NewClient(
			client.WithDebianKeyring(base, armoredPublicKey(t, first)),
			client.WithDebianKeyring(base+"/", armoredPublicKey(t, second)),
		)
		if err := fetchCurl(New(base+"?suites=bookworm&architectures=amd64", c)); err != nil {
			t.Fatalf("FetchVersions: %v", err)
		}
	}
}
