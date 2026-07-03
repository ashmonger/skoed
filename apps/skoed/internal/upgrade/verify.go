package upgrade

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"

	"golang.org/x/crypto/openpgp" //nolint:staticcheck // deprecated but adequate for detached-signature verify; avoids a new module dependency (x/crypto is already a direct dep)
)

// embeddedReleaseKey is the ASCII-armored OpenPGP public key (primary
// 762F13A88A0D63D5 plus its signing subkey) used to authenticate release
// artifacts. It is embedded at BUILD time so a compromised feed or mirror
// cannot substitute its own key — the trust anchor ships inside the binary.
//
// Regenerate after adding/rotating the release signing subkey:
//
//	gpg --armor --export 762F13A88A0D63D5 > internal/upgrade/release_pubkey.asc
//
//go:embed release_pubkey.asc
var embeddedReleaseKey []byte

// releaseKeyring returns the keyring used to verify release signatures. In
// tests SKOED_TEST_UPGRADE_PUBKEY points at an ephemeral armored public key so
// the suite can sign fixtures without the production private key; it is never
// set in production.
func releaseKeyring() (openpgp.EntityList, error) {
	armored := embeddedReleaseKey
	if p := os.Getenv("SKOED_TEST_UPGRADE_PUBKEY"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read test upgrade pubkey: %w", err)
		}
		armored = b
	}
	kr, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("parse release public key: %w", err)
	}
	if len(kr) == 0 {
		return nil, fmt.Errorf("release keyring is empty")
	}
	return kr, nil
}

// verifyChecksumsSignature checks that sig is a valid OpenPGP detached
// signature over checksums, made by the embedded release key (or a subkey it
// certifies). It accepts both binary (.sig) and ASCII-armored (.asc)
// signatures. A non-nil error means the checksums must NOT be trusted.
func verifyChecksumsSignature(checksums, sig []byte) error {
	kr, err := releaseKeyring()
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(sig)
	if bytes.HasPrefix(trimmed, []byte("-----BEGIN PGP")) {
		_, err = openpgp.CheckArmoredDetachedSignature(kr, bytes.NewReader(checksums), bytes.NewReader(sig))
	} else {
		_, err = openpgp.CheckDetachedSignature(kr, bytes.NewReader(checksums), bytes.NewReader(sig))
	}
	if err != nil {
		return fmt.Errorf("release signature verification failed: %w", err)
	}
	return nil
}
