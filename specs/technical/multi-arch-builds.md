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
GOOS/GOARCH (CGO_ENABLED=0 since dblock has no C deps). nfpm config
re-used from M5.5.

```yaml
version: 2

before:
  hooks:
    - make build-ui      # bundle the SPA before binary builds
    - make openapi-sync  # stage the OpenAPI spec for the binary

builds:
  - id: dblock
    main: ./apps/dblock/cmd/dblock
    binary: dblock
    env:
      - CGO_ENABLED=0
    goos:   [linux]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.ShortCommit}}

archives:
  - id: dblock
    name_template: "dblock_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format: tar.gz
    files: [LICENSE*, README*, packaging/config.example.yaml]

nfpms:
  - id: dblock
    package_name: dblock
    file_name_template: "dblock_{{ .Version }}_{{ .Arch }}"
    homepage: https://github.com/dblock/dblock
    description: Self-hosted DNS filtering with multi-node sync.
    maintainer: dblock maintainers <maintainers@dblock.io>
    license: MIT
    formats: [deb]
    bindir: /usr/bin
    contents:
      - src: ./packaging/dblock.service
        dst: /lib/systemd/system/dblock.service
      - src: ./packaging/config.example.yaml
        dst: /etc/dblock/config.yaml
        type: config|noreplace
      - dst: /var/lib/dblock
        type: dir
        file_info: { mode: 0700, owner: dblock, group: dblock }
      - dst: /var/log/dblock
        type: dir
        file_info: { mode: 0750, owner: dblock, group: dblock }
    scripts:
      preinstall:  ./packaging/scripts/preinst.sh
      postinstall: ./packaging/scripts/postinst.sh
      preremove:   ./packaging/scripts/prerm.sh
      postremove:  ./packaging/scripts/postrm.sh
    dependencies: [adduser]

dockers:
  - id: dblock-amd64
    image_templates:
      - "ghcr.io/dblock/dblock:{{ .Version }}-amd64"
      - "ghcr.io/dblock/dblock:latest-amd64"
    dockerfile: Dockerfile
    use: buildx
    build_flag_templates:
      - "--platform=linux/amd64"
    goarch: amd64

  - id: dblock-arm64
    image_templates:
      - "ghcr.io/dblock/dblock:{{ .Version }}-arm64"
      - "ghcr.io/dblock/dblock:latest-arm64"
    dockerfile: Dockerfile
    use: buildx
    build_flag_templates:
      - "--platform=linux/arm64"
    goarch: arm64

docker_manifests:
  - name_template: "ghcr.io/dblock/dblock:{{ .Version }}"
    image_templates:
      - "ghcr.io/dblock/dblock:{{ .Version }}-amd64"
      - "ghcr.io/dblock/dblock:{{ .Version }}-arm64"
  - name_template: "ghcr.io/dblock/dblock:latest"
    image_templates:
      - "ghcr.io/dblock/dblock:latest-amd64"
      - "ghcr.io/dblock/dblock:latest-arm64"

checksum:
  name_template: "checksums.txt"

release:
  github:
    owner: dblock
    name: dblock
```

## Dockerfile

Single-stage `FROM gcr.io/distroless/static-debian12` (≤ 30 MB),
copies the pre-built `dblock` binary in. Image size assertion: any
arch under 100 MB (M1 risk row).

```dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
COPY dblock /usr/bin/dblock
EXPOSE 53/udp 53/tcp 8080/tcp
ENTRYPOINT ["/usr/bin/dblock"]
CMD ["--config", "/etc/dblock/config.yaml"]
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
- `file dist/dblock_*_linux_amd64/dblock` → `ELF 64-bit LSB executable, x86-64`.
- `file dist/dblock_*_linux_arm64/dblock` → `ELF 64-bit LSB executable, ARM aarch64`.
- `packaging/test-deb.sh dist/dblock_*_amd64.deb` smoke-test.
