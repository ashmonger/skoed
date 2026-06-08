---
x-tsid: TS-MultiArchBuilds
x-fsid-links:
  - FS-MultiArchGoreleaserBuilds
  - FS-MultiArchDockerManifest
  - FS-MultiArchImageSize
  - FS-MultiArchChecksums
---

# TS-MultiArchBuilds — goreleaser + buildx

## Tool

[`goreleaser`](https://goreleaser.com/) drives the full release
pipeline: build (cross-compile for amd64 + arm64), archive (tar.gz),
package (.deb via nfpm), Docker (buildx multi-arch manifest),
checksums, GitHub Release upload.

## Layout

`.goreleaser.yaml` at the repo root. Cross-compile via Go's native
GOOS/GOARCH (CGO_ENABLED=0 since skoed has no C deps). nfpm config
re-used from M5.5.

```yaml
version: 2

before:
  hooks:
    - make build-ui      # bundle the SPA before binary builds
    - make openapi-sync  # stage the OpenAPI spec for the binary

builds:
  - id: skoed
    main: ./apps/skoed/cmd/skoed
    binary: skoed
    env:
      - CGO_ENABLED=0
    goos:   [linux]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.ShortCommit}}

archives:
  - id: skoed
    name_template: "skoed_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format: tar.gz
    files: [LICENSE*, README*, packaging/config.example.yaml]

nfpms:
  - id: skoed
    package_name: skoed
    file_name_template: "skoed_{{ .Version }}_{{ .Arch }}"
    homepage: https://github.com/skoed/skoed
    description: Self-hosted DNS filtering with multi-node sync.
    maintainer: skoed maintainers <maintainers@skoed.io>
    license: MIT
    formats: [deb]
    bindir: /usr/bin
    contents:
      - src: ./packaging/skoed.service
        dst: /lib/systemd/system/skoed.service
      - src: ./packaging/config.example.yaml
        dst: /etc/skoed/config.yaml
        type: config|noreplace
      - dst: /var/lib/skoed
        type: dir
        file_info: { mode: 0700, owner: skoed, group: skoed }
      - dst: /var/log/skoed
        type: dir
        file_info: { mode: 0750, owner: skoed, group: skoed }
    scripts:
      preinstall:  ./packaging/scripts/preinst.sh
      postinstall: ./packaging/scripts/postinst.sh
      preremove:   ./packaging/scripts/prerm.sh
      postremove:  ./packaging/scripts/postrm.sh
    dependencies: [adduser]

dockers:
  - id: skoed-amd64
    image_templates:
      - "ghcr.io/skoed/skoed:{{ .Version }}-amd64"
      - "ghcr.io/skoed/skoed:latest-amd64"
    dockerfile: Dockerfile
    use: buildx
    build_flag_templates:
      - "--platform=linux/amd64"
    goarch: amd64

  - id: skoed-arm64
    image_templates:
      - "ghcr.io/skoed/skoed:{{ .Version }}-arm64"
      - "ghcr.io/skoed/skoed:latest-arm64"
    dockerfile: Dockerfile
    use: buildx
    build_flag_templates:
      - "--platform=linux/arm64"
    goarch: arm64

docker_manifests:
  - name_template: "ghcr.io/skoed/skoed:{{ .Version }}"
    image_templates:
      - "ghcr.io/skoed/skoed:{{ .Version }}-amd64"
      - "ghcr.io/skoed/skoed:{{ .Version }}-arm64"
  - name_template: "ghcr.io/skoed/skoed:latest"
    image_templates:
      - "ghcr.io/skoed/skoed:latest-amd64"
      - "ghcr.io/skoed/skoed:latest-arm64"

checksum:
  name_template: "checksums.txt"

release:
  github:
    owner: skoed
    name: skoed
```

## Dockerfile

Single-stage `FROM gcr.io/distroless/static-debian12` (≤ 30 MB),
copies the pre-built `skoed` binary in. Image size assertion: any
arch under 100 MB (M1 risk row).

```dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
COPY skoed /usr/bin/skoed
EXPOSE 53/udp 53/tcp 8080/tcp
ENTRYPOINT ["/usr/bin/skoed"]
CMD ["--config", "/etc/skoed/config.yaml"]
```

## CI workflow

`.github/workflows/release.yml`:

```yaml
name: release
on:
  push:
    tags: ['v*']
jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      packages: write
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.24' }
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## Local validation

`goreleaser release --snapshot --clean` produces a `dist/` tree
without publishing. Operators (and CI) verify by:

- `ls dist/` — both arches per artefact.
- `file dist/skoed_*_linux_amd64/skoed` → `ELF 64-bit LSB executable, x86-64`.
- `file dist/skoed_*_linux_arm64/skoed` → `ELF 64-bit LSB executable, ARM aarch64`.
- `packaging/test-deb.sh dist/skoed_*_amd64.deb` smoke-test.
