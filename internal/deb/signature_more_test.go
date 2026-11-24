package deb

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/git-pkgs/registries/client"
)

// expiredKey is a key that was valid for an hour a year ago, and a config
// that signs at that time.
func expiredKey(t *testing.T) (*openpgp.Entity, *packet.Config) {
	t.Helper()
	then := time.Now().AddDate(-1, 0, 0)
	config := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, KeyLifetimeSecs: 3600, Time: func() time.Time { return then }}
	e, err := openpgp.NewEntity("Old Archive Signing Key", "", "old@example.org", config)
	if err != nil {
		t.Fatal(err)
	}
	return e, config
}

// clearsignedBy is text clearsigned by each key in turn, signing with the
// key's config.
func clearsignedBy(t *testing.T, text []byte, keys []*openpgp.Entity, configs []*packet.Config) []byte {
	t.Helper()
	var buf bytes.Buffer
	privateKeys := make([]*packet.PrivateKey, len(keys))
	for i, k := range keys {
		privateKeys[i] = k.PrivateKey
	}
	// EncodeMulti takes one config; the signing time of the first key
	// keeps the expired key's signature inside its lifetime.
	w, err := clearsign.EncodeMulti(&buf, privateKeys, configs[0])
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

func detachSignedBinary(t *testing.T, e *openpgp.Entity, text []byte, config *packet.Config) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, e, bytes.NewReader(text), config); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func armorSignature(t *testing.T, sig []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.SignatureType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(sig); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func keyringOf(t *testing.T, keys ...*openpgp.Entity) []byte {
	t.Helper()
	var keyring []byte
	for _, k := range keys {
		keyring = append(keyring, binaryPublicKey(t, k)...)
	}
	return keyring
}

// Debian's InRelease carries several signatures, and one by an expired
// key doesn't keep a good one after it from counting.
func TestInReleaseWithAnExpiredAndACurrentSignature(t *testing.T) {
	old, oldConfig := expiredKey(t)
	current := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsignedBy(t, text, []*openpgp.Entity{old, current}, []*packet.Config{oldConfig})
	if err := fetchCurl(keyringRegistry(base, keyringOf(t, old, current))); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestInReleaseSignedOnlyByAnExpiredKey(t *testing.T) {
	old, oldConfig := expiredKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsignedBy(t, text, []*openpgp.Entity{old}, []*packet.Config{oldConfig})
	err := fetchCurl(keyringRegistry(base, keyringOf(t, old, newKey(t))))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestReleaseGPGWithAnExpiredAndACurrentSignature(t *testing.T) {
	old, oldConfig := expiredKey(t)
	current := newKey(t)
	for name, wrap := range map[string]func([]byte) []byte{
		"binary":  func(sig []byte) []byte { return sig },
		"armored": func(sig []byte) []byte { return armorSignature(t, sig) },
	} {
		t.Run(name, func(t *testing.T) {
			a, text, base := signedArchive(t)
			sigs := append(detachSignedBinary(t, old, text, oldConfig), detachSignedBinary(t, current, text, nil)...)
			a.served["dists/bookworm/Release"] = text
			a.served["dists/bookworm/Release.gpg"] = wrap(sigs)
			if err := fetchCurl(keyringRegistry(base, keyringOf(t, old, current))); err != nil {
				t.Fatalf("FetchVersions: %v", err)
			}
		})
	}
}

// A signature by a key outside the keyring ahead of a good one is skipped.
func TestReleaseGPGWithAForeignAndACurrentSignature(t *testing.T) {
	current := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = append(detachSignedBinary(t, newKey(t), text, nil), detachSignedBinary(t, current, text, nil)...)
	if err := fetchCurl(keyringRegistry(base, keyringOf(t, current))); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestReleaseGPGTruncated(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	sig := detachSignedBinary(t, key, text, nil)
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = sig[:len(sig)-3]
	err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// A keyring made by concatenating .asc files has every key in it.
func TestConcatenatedArmoredKeyrings(t *testing.T) {
	first, second := newKey(t), newKey(t)
	keyring := append(armoredPublicKey(t, first), armoredPublicKey(t, second)...)
	for _, key := range []*openpgp.Entity{first, second} {
		a, text, base := signedArchive(t)
		a.served["dists/bookworm/InRelease"] = clearsigned(t, key, text)
		if err := fetchCurl(keyringRegistry(base, keyring)); err != nil {
			t.Fatalf("FetchVersions: %v", err)
		}
	}
}

func TestConcatenatedKeyringWithABadBlock(t *testing.T) {
	_, _, base := signedArchive(t)
	keyring := append(armoredPublicKey(t, newKey(t)), []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nnot base64!\n-----END PGP PUBLIC KEY BLOCK-----\n")...)
	err := fetchCurl(keyringRegistry(base, keyring))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func withValidUntil(text []byte, until time.Time) []byte {
	return append([]byte("Valid-Until: "+until.UTC().Format(time.RFC1123)+"\n"), text...)
}

func TestExpiredReleaseRefused(t *testing.T) {
	key := newKey(t)
	t.Run("InRelease", func(t *testing.T) {
		a, text, base := signedArchive(t)
		a.served["dists/bookworm/InRelease"] = clearsigned(t, key, withValidUntil(text, time.Now().Add(-time.Hour)))
		err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
		if !errors.Is(err, ErrExpiredRelease) {
			t.Fatalf("err = %v, want ErrExpiredRelease", err)
		}
	})
	t.Run("Release", func(t *testing.T) {
		a, text, base := signedArchive(t)
		text = withValidUntil(text, time.Now().Add(-time.Hour))
		a.served["dists/bookworm/Release"] = text
		a.served["dists/bookworm/Release.gpg"] = detachSigned(t, key, text)
		err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
		if !errors.Is(err, ErrExpiredRelease) {
			t.Fatalf("err = %v, want ErrExpiredRelease", err)
		}
	})
}

func TestReleaseValidUntilInTheFuture(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, key, withValidUntil(text, time.Now().Add(time.Hour)))
	if err := fetchCurl(keyringRegistry(base, keyringOf(t, key))); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

// Valid-Until is compared with the clock, not only read.
func TestReleaseValidUntilUsesTheClock(t *testing.T) {
	key := newKey(t)
	until := time.Now().Add(time.Hour)
	defer func(saved func() time.Time) { now = saved }(now)
	now = func() time.Time { return until.Add(time.Minute) }
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, key, withValidUntil(text, until))
	err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
	if !errors.Is(err, ErrExpiredRelease) {
		t.Fatalf("err = %v, want ErrExpiredRelease", err)
	}
}

// apt writes the zone as UTC; some archives write a numeric offset.
func TestReleaseValidUntilNumericZone(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	until := time.Now().Add(-time.Hour).Format(time.RFC1123Z)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, key, append([]byte("Valid-Until: "+until+"\n"), text...))
	err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
	if !errors.Is(err, ErrExpiredRelease) {
		t.Fatalf("err = %v, want ErrExpiredRelease", err)
	}
}

func TestReleaseValidUntilMalformed(t *testing.T) {
	key := newKey(t)
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/InRelease"] = clearsigned(t, key, append([]byte("Valid-Until: next week\n"), text...))
	err := fetchCurl(keyringRegistry(base, keyringOf(t, key)))
	if !errors.Is(err, ErrMalformedIndex) {
		t.Fatalf("err = %v, want ErrMalformedIndex", err)
	}
}

// Without a keyring nothing is checked, Valid-Until included.
func TestExpiredReleaseWithoutKeyring(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = withValidUntil(text, time.Now().Add(-time.Hour))
	if err := fetchCurl(New(base+"?suites=bookworm&architectures=amd64", client.NewClient())); err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
}

func TestEmptyReleaseGPGRefused(t *testing.T) {
	a, text, base := signedArchive(t)
	a.served["dists/bookworm/Release"] = text
	a.served["dists/bookworm/Release.gpg"] = []byte{}
	err := fetchCurl(keyringRegistry(base, keyringOf(t, newKey(t))))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// RSA signatures from 2048-bit keys up use two-octet lengths, and other
// tools write the old packet format.
func TestPacketLength(t *testing.T) {
	body := func(n int) []byte { return make([]byte, n) }
	for name, tc := range map[string]struct {
		packet         []byte
		header, length int
	}{
		"new one-octet":     {append([]byte{0xC2, 191}, body(191)...), 2, 191},
		"new two-octet low": {append([]byte{0xC2, 192, 0}, body(192)...), 3, 192},
		"new two-octet":     {append([]byte{0xC2, 193, 0x40}, body(512)...), 3, 512},
		"new five-octet":    {append([]byte{0xC2, 255, 0, 0, 0x22, 0x40}, body(8768)...), 6, 8768},
		"old one-octet":     {append([]byte{0x88, 5}, body(5)...), 2, 5},
		"old two-octet":     {append([]byte{0x89, 0x01, 0x00}, body(256)...), 3, 256},
		"old four-octet":    {append([]byte{0x8A, 0, 0, 0x01, 0x00}, body(256)...), 5, 256},
		"old indeterminate": {append([]byte{0x8B}, body(7)...), 1, 7},
	} {
		t.Run(name, func(t *testing.T) {
			header, length, err := packetLength(tc.packet)
			if err != nil || header != tc.header || length != tc.length {
				t.Fatalf("packetLength = %d, %d, %v; want %d, %d", header, length, err, tc.header, tc.length)
			}
			packets, err := signaturePackets(append(append([]byte{}, tc.packet...), tc.packet...))
			if tc.packet[0] == 0x8B {
				// An indeterminate length runs to the end.
				if err != nil || len(packets) != 1 {
					t.Fatalf("signaturePackets = %d packets, %v; want 1", len(packets), err)
				}
				return
			}
			if err != nil || len(packets) != 2 || !bytes.Equal(packets[1], tc.packet) {
				t.Fatalf("signaturePackets = %d packets, %v; want 2", len(packets), err)
			}
		})
	}
	for name, packet := range map[string][]byte{
		"not a packet":    {0x42, 1, 0},
		"partial length":  {0xC2, 224, 0},
		"short header":    {0xC2},
		"short two-octet": {0xC2, 192},
		"short old":       {0x89, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := signaturePackets(packet); err == nil {
				t.Fatal("signaturePackets accepted it")
			}
		})
	}
}
