# Upgrade Artifact Signature Verification

- Date: 2026-07-03
- Status: Accepted
- Deciders: Repository Maintainer Council (UoR), driven by security audit follow-up

## Context

The security audit (`SECURITY_AUDIT.md`) rated the in-place binary upgrade a
**Critical** finding (C-2): `upgrade.Swap` downloaded a tar.gz and replaced the
running binary with **no integrity or authenticity check**. The first fix added
mandatory SHA-256 verification against the goreleaser `checksums.txt`. That
closes MITM / compromised-mirror tampering, but **not** a malicious authenticated
user (or anyone who can reach the upgrade API) who supplies a binary *and its
matching checksum* — SHA alone proves nothing about origin.

## Problem

skoed must refuse to install any upgrade artifact that was not produced by the
project's release process, even against a caller who controls the URL, the
binary, and the checksum table.

## Options considered

1. **SHA-256 only** (already shipped). Integrity, not authenticity. Rejected as
   insufficient for C-2's residual.
2. **minisign / Ed25519.** Verification is pure stdlib `crypto/ed25519`, no new
   module, goreleaser-native. Downside: a brand-new key not tied to any existing
   identity; another secret to manage.
3. **GPG/OpenPGP with the maintainer's existing key `762F13A88A0D63D5`**, using a
   **dedicated release-only signing subkey** so the long-lived personal key is
   never copied into CI. Verification via `golang.org/x/crypto/openpgp`.
4. **cosign / sigstore.** Keyless-OIDC or key-based; heavier infra than warranted
   for a self-hosted appliance.

## Decision

Option 3. Sign the goreleaser `checksums.txt` at release time with a dedicated
GPG signing subkey under the maintainer's primary key `762F13A88A0D63D5`. skoed
**embeds the release public key at build time** (`internal/upgrade/release_pubkey.asc`,
`//go:embed`) and verifies the detached signature over `checksums.txt` before any
checksum — and therefore any artifact — is trusted. The chain is:

  embedded release pubkey → verify checksums.txt signature → SHA-256 of artifact → swap

Rationale for reusing the maintainer's GPG identity: it is already published and
trusted (`github.com/ashmonger.gpg`), so downstream users can independently
verify releases against a pre-existing, out-of-band trust anchor rather than a
key that first appears alongside the software it signs.

### Dependency exception

Verification uses `golang.org/x/crypto/openpgp`. This package is **deprecated**
(frozen) upstream, but:
- `golang.org/x/crypto` is **already a direct dependency**, so this adds **no new
  module** and no new supply-chain surface — materially better than pulling in
  `github.com/ProtonMail/go-crypto` (AGENTS.md Rule 11: standard/common libraries).
- Detached-signature verification of an RSA key is stable, well-exercised
  functionality unaffected by the package's frozen status.

This is the written exception permitting the deprecated import; revisit if the
package is ever removed from `x/crypto` (migrate to ProtonMail's fork at that
point).

## Consequences

- The release process (CI) must import the release signing subkey's secret and
  set `GPG_FINGERPRINT`; goreleaser's `signs` block emits `checksums.txt.sig`.
- After the release subkey is created/rotated, `release_pubkey.asc` MUST be
  re-exported (`gpg --armor --export 762F13A88A0D63D5`) and committed, or
  upgrades will fail to verify.
- The direct-`url` and cluster rolling-upgrade paths already require a `sha256`;
  those SHAs are only trusted when they originate from a signature-verified
  `checksums.txt` on the feed/GitHub path.
- Custom feeds must reference `checksums_url` + `checksums_sig_url`; inline,
  unsigned `checksums` in a feed are no longer trusted.
- Tests inject an ephemeral signing key via `SKOED_TEST_UPGRADE_PUBKEY`
  (never set in production).

## Related hypotheses

- A future move to a dedicated CI/release identity (rather than the maintainer's
  personal primary) would further isolate blast radius; deferred.

## Affected features

- In-place upgrade (M5.6 / M16), rolling cluster upgrade (M18), release pipeline.
