FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /skoed ./cmd/skoed/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /skoed /usr/local/bin/skoed
EXPOSE 53/udp 53/tcp 8080
ENTRYPOINT ["skoed"]
