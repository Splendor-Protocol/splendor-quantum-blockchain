# ── Stage 1: Build ────────────────────────────────────────────────────────
# go 1.25+ required: circl v1.6.5 (FIPS-204 ML-DSA-65) sets go 1.25.0 in its module.
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy all source
COPY . .

# Generate go.sum by fetching all dependencies, then build
RUN go get ./... && go mod tidy

# Build identity, stamped into the binary so a running node can say what it is.
# Without these the node reports commit "unknown" — deliberately, because a
# guessed commit is worse than an absent one.
ARG SQC_COMMIT=unknown
ARG SQC_IMAGE_TAG=unknown

RUN mkdir -p /out
RUN CGO_ENABLED=1 GOOS=linux go build -mod=mod \
      -ldflags="-s -w \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.Commit=${SQC_COMMIT} \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.ImageTag=${SQC_IMAGE_TAG}" \
      -o /out/sqc-node ./cmd/node
RUN CGO_ENABLED=1 GOOS=linux go build -mod=mod \
      -ldflags="-s -w \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.Commit=${SQC_COMMIT} \
        -X github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo.ImageTag=${SQC_IMAGE_TAG}" \
      -o /out/sqc-wallet ./cmd/wallet

# ── Stage 2: Runtime ──────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /sqc

COPY --from=builder /out/sqc-node /usr/local/bin/sqc-node
COPY --from=builder /out/sqc-wallet /usr/local/bin/sqc-wallet

RUN mkdir -p /sqc/data

EXPOSE 8080
VOLUME ["/sqc/data"]

ENTRYPOINT ["sqc-node"]
CMD ["--data-dir", "/sqc/data", "--rpc-addr", ":8080", "--log-level", "info"]
