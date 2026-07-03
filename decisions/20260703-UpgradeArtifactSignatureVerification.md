# Upgrade Artifact Integrity Verification

- Date: 2026-07-03
- Status: Accepted (revised — OpenPGP signing was prototyped then dropped; see "Decision")
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

**Mandatory SHA-256 integrity verification, with authenticity resting on the
GitHub-hosted build + HTTPS transport. No cryptographic signing.**

`upgrade.Swap` refuses to install any artifact without a matching SHA-256, and
the checksum is sourced from the goreleaser `checksums.txt` on the GitHub release
(or inline in a custom feed). The chain is:

  checksums.txt (GitHub release, HTTPS) → SHA-256 of artifact → swap

Option 3 (GPG-signed `checksums.txt` with `x/crypto/openpgp` verification) was
implemented first, but was **dropped** on maintainer decision: for a self-hosted
appliance whose releases are built and published by GitHub Actions from tagged
source, the GitHub-hosted build provenance plus HTTPS delivery is judged
sufficient authenticity, and it avoids the operational burden of managing a
signing key in CI and keeping an embedded public key in sync with subkey
rotations. The deprecated `x/crypto/openpgp` dependency and the embedded
`release_pubkey.asc` were removed with it.

## Consequences

- No CI signing secrets required; `release.yml` has no GPG step and goreleaser
  emits `checksums.txt` (no `.sig`).
- SHA-256 verification still protects integrity: a corrupted/MITM'd download of a
  known release is rejected. `TestUpgradeRejectsChecksumMismatch` covers this.
- The direct-`url` and cluster rolling-upgrade paths still require a `sha256` in
  the request.
- **Accepted residual (audit C-2 tail):** a caller who can both reach the upgrade
  API and control the checksum source is not stopped by SHA alone. This is
  accepted given the trust model above; re-introduce signing if releases ever
  move off GitHub-hosted builds or the API's trust boundary widens.

## Related hypotheses

- If signing is ever reinstated, prefer a dedicated release identity + minisign
  (stdlib Ed25519 verify) over reusing the maintainer's GPG primary.

## Affected features

- In-place upgrade (M5.6 / M16), rolling cluster upgrade (M18), release pipeline.
