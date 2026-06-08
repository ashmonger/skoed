# DEMO NOTE — M5.8 Documentation Site (mdBook scaffold)

## Scope

A static documentation site rendered by [mdBook](https://rust-lang.github.io/mdBook/),
search-indexed by [Pagefind](https://pagefind.app/), and published
to GitHub Pages by a workflow that fires on any change under
`docs/`.

### Implemented

- **`docs/book.toml`** — mdBook config with the dark "navy" theme,
  fold-by-section navigation, and `edit-url-template` pointing back
  to `github.com/dblock/dblock/edit/main/docs/src/{path}` so any
  page has a one-click "edit on GitHub" link.
- **`docs/src/SUMMARY.md`** — the table of contents. 5 sections:
  Install / First run / Cluster / Configuration / Operations /
  Reference.
- **5 seed chapters covering the operator's first-hour path**:
  - `introduction.md` — what dblock is + deployment shapes + non-goals.
  - `install/debian-ubuntu.md` — `.deb` install via apt.
  - `install/proxmox-lxc.md` — `scripts/proxmox-create.sh` walkthrough.
  - `first-run/auth-setup.md` — first POST `/api/v1/auth/setup`.
  - `first-run/first-blocklist.md` — Hagezi Pro example via
    `POST /api/v1/blocklists` with `refresh_interval_seconds`.
  - `cluster/bootstrap.md` — 3-node cluster from scratch, join
    tokens, verification.
- **19 placeholder chapters** for everything else (categories,
  profiles, DoH/DoT, audit log, troubleshooting, etc.) — auto-generated
  stubs so SUMMARY links don't 404. Each says "this chapter is a
  placeholder; PRs welcome" + points at the spec.
- **`.github/workflows/docs.yml`** — on push to `master`/`main`
  paths `docs/**`:
  - `peaceiris/actions-mdbook@v2` builds the book.
  - `npx pagefind --site docs/book` adds the search index.
  - `actions/upload-pages-artifact` + `actions/deploy-pages@v4`
    publishes to GitHub Pages.

### Local build (operator side)

```sh
# Install mdbook one-shot.
curl -sSL https://github.com/rust-lang/mdBook/releases/latest/download/mdbook-v0.4.42-x86_64-unknown-linux-gnu.tar.gz \
  | tar -xz && sudo install mdbook /usr/local/bin/

cd docs && mdbook build
# → docs/book/index.html + assets

# Optional: full-text search.
npx --yes pagefind --site docs/book

# Live preview while writing:
mdbook serve  # http://127.0.0.1:3000
```

### Acceptance / FSIDs

4 FSIDs in `specs/functional/documentation-site.feature`. No Go
acceptance tests — the validation is `mdbook build` exits 0 + the
Pages workflow publishes. Both are exercised by the docs CI job on
every push touching `docs/`.

### Not implemented (deferred / non-goals)

- **Versioned docs** across previous N releases — single "latest"
  for v1. mdBook can do versioned via subdirectories; revisit when
  there's a v1.0.
- **Translated docs** — English only.
- **API reference auto-gen** — M4.5 Swagger UI already serves
  `/api/docs/` on every live node; the docs site links to it.
- **Comment threads / forums** — explicit non-goal.
- **Custom domain `docs.dblock.io`** — operator/owner DNS work,
  outside the repo.

### Files added

```
docs/
  book.toml
  src/
    SUMMARY.md
    introduction.md
    install/
      debian-ubuntu.md
      proxmox-lxc.md
      docker.md          (stub)
      kubernetes.md      (stub)
    first-run/
      auth-setup.md
      first-blocklist.md
    cluster/
      bootstrap.md
      add-nodes.md       (stub)
      encrypted-mesh.md  (stub)
    configuration/
      ...                (8 stubs)
    operations/
      ...                (4 stubs)
    reference/
      ...                (3 stubs)
.github/workflows/docs.yml
```

## Next

End of the M5.x umbrella. M6 ("Closing the DoH Gap") and beyond
already have entries in ROADMAP.md.
