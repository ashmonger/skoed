---
x-tsid: TS-DocumentationSite
x-fsid-links:
  - FS-DocsSiteBuildsLocally
  - FS-DocsSiteSearchable
  - FS-DocsSiteCoversCoreFlow
  - FS-DocsSitePublishesViaPages
---

# TS-DocumentationSite — mdBook + Pagefind + GitHub Pages

## Tool

[mdBook](https://rust-lang.github.io/mdBook/) — single-binary,
Rust-based, zero JS framework, fast `cargo install mdbook` (or via
the `peaceiris/actions-mdbook` GitHub action). Chosen over Hugo /
VitePress because:

- No npm dependency in the docs build (CI already does npm for the
  SPA; keeping docs static-only avoids drift).
- Built-in left-nav + search (no template hunting).
- Operators reading dblock docs are already operating Linux daemons;
  a Rust-built static site is the right vibe.

[Pagefind](https://pagefind.app/) layered on top for full-text search
(mdBook's built-in search is OK, Pagefind is materially better and
adds ~70 KB of JS only on result pages).

## Layout

```
docs/
  book.toml                      # mdBook config
  src/
    SUMMARY.md                   # left-nav (the table of contents)
    introduction.md
    install/
      debian-ubuntu.md
      proxmox-lxc.md
      docker.md
      kubernetes.md
    first-run/
      auth-setup.md
      first-blocklist.md
    cluster/
      bootstrap.md
      add-nodes.md
      encrypted-mesh.md
    configuration/
      dns.md
      blocklists.md
      profiles.md
      schedules.md
      categories.md
      doh-dot.md
      api-https.md
      metrics.md
      audit-log.md
    operations/
      automated-refresh.md
      in-place-upgrade.md
      backup-restore.md
      troubleshooting.md
    reference/
      yaml-schema.md
      api-openapi.md             # link to /api/docs/ on a live node
      cli.md
```

## book.toml

```toml
[book]
title = "dblock"
authors = ["dblock maintainers"]
language = "en"
src = "src"

[output.html]
default-theme = "navy"
preferred-dark-theme = "navy"
git-repository-url = "https://github.com/dblock/dblock"
edit-url-template = "https://github.com/dblock/dblock/edit/main/docs/src/{path}"
no-section-label = true
```

## Build

```sh
# install mdbook (Linux)
curl -sSL https://github.com/rust-lang/mdBook/releases/latest/download/mdbook-v0.4.42-x86_64-unknown-linux-gnu.tar.gz \
  | tar -xz && sudo mv mdbook /usr/local/bin/

# build the site
cd docs && mdbook build
# → docs/book/index.html + assets

# add Pagefind search index
npx pagefind --site docs/book
```

## CI publish

`.github/workflows/docs.yml`:

```yaml
name: docs
on:
  push:
    branches: [main]
    paths: ['docs/**']
permissions:
  contents: read
  pages: write
  id-token: write
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: peaceiris/actions-mdbook@v2
        with: { mdbook-version: 'latest' }
      - run: cd docs && mdbook build
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - run: npx pagefind --site docs/book
      - uses: actions/upload-pages-artifact@v3
        with: { path: docs/book }
  deploy:
    needs: build
    runs-on: ubuntu-latest
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    steps:
      - id: deployment
        uses: actions/deploy-pages@v4
```

## M5.8 v1 scope

The full chapter tree above is the target; **v1 ships the scaffolding
+ five seed chapters** covering the operator's first-hour path:

- `introduction.md` — what dblock is + the three deployment shapes.
- `install/debian-ubuntu.md` — `apt install ./dblock_*.deb`.
- `install/proxmox-lxc.md` — `scripts/proxmox-create.sh`.
- `first-run/auth-setup.md` — POST `/api/v1/auth/setup`.
- `cluster/bootstrap.md` — token-based three-node join.

Remaining chapters land as we use them; the workflow is set up so a
new `.md` under `docs/src/` is a one-PR addition.
