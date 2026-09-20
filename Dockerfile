# ── Stage 1: Build ────────────────────────────────────────────────────────
# go 1.25+ required: circl v1.6.5 (FIPS-204 ML-DSA-65) sets go 1.25.0 in its module.
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy all source
COPY . .

# Fetch the committed dependency set without changing it during image builds.
RUN go mod download

# Build identity, stamped into the binary so a running node can say what it is.
# Without these the node reports commit "unknown" — deliberately, because a
# guessed commit is worse than an absent one.
ARG SQC_COMMIT=unknown
ARG SQC_IMAGE_TAG=unknown

RUN mkdir -p /out
RUN CGO_ENABLED=1 GOOS=linux go build -mod=readonly \
      -ldflags="-s -w \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.Commit=${SQC_COMMIT} \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.ImageTag=${SQC_IMAGE_TAG}" \
      -o /out/sqc-node ./cmd/node
RUN CGO_ENABLED=1 GOOS=linux go build -mod=readonly \
      -ldflags="-s -w \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.Commit=${SQC_COMMIT} \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.ImageTag=${SQC_IMAGE_TAG}" \
      -o /out/sqc-wallet ./cmd/wallet

# Operators register a new validator with a signed module call after it has
# synced. Keep the existing module-call driver alongside the wallet so that
# registration can use the host's wallet without exporting its private key.
RUN CGO_ENABLED=1 GOOS=linux go build -mod=readonly \
      -ldflags="-s -w" -o /out/sqc-modcall ./cmd/sqc-modcall

# ── Stage 2: Runtime ──────────────────────────────────────────────────────
FROM alpine:3.22

RUN apk add --no-cache ca-certificates

WORKDIR /sqc

COPY --from=builder /out/sqc-node /usr/local/bin/sqc-node
COPY --from=builder /out/sqc-wallet /usr/local/bin/sqc-wallet
COPY --from=builder /out/sqc-modcall /usr/local/bin/sqc-modcall

# The node never binds a privileged port (defaults: RPC :8080, metrics on
# 127.0.0.1) and never needs to touch device files, so drop to an
# unprivileged user: a compromised node process then cannot escape to root
# in the container. Named volumes inherit the image's ownership on first
# use; a pre-existing bind mount owned by root must be chowned by the
# operator before this image runs against it.
RUN addgroup -S sqc && adduser -S -G sqc sqc \
    && mkdir -p /sqc/data \
    && chown -R sqc:sqc /sqc
USER sqc

EXPOSE 8080
VOLUME ["/sqc/data"]

ENTRYPOINT ["sqc-node"]
CMD ["--data-dir", "/sqc/data", "--rpc-addr", ":8080", "--log-level", "info"]
