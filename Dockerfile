# skoed Docker image — distroless, multi-arch (amd64 + arm64).
# Built by goreleaser; the skoed binary is pre-compiled for the
# target arch and copied in.

FROM gcr.io/distroless/static-debian12:nonroot

COPY skoed /usr/bin/skoed

# DNS (plain), DoH/DoT (when enabled), management API.
EXPOSE 53/udp 53/tcp 853/tcp 8080/tcp

# /etc/skoed/config.yaml is operator-supplied via volume mount or
# Helm chart. Image ships no config — the operator picks the shape.
ENTRYPOINT ["/usr/bin/skoed"]
CMD ["--config", "/etc/skoed/config.yaml"]
