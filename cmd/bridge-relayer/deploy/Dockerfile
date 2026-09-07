# Bridge relayer — reproducible container image.
#
# Build from the REPO ROOT (it imports core/*, vm/*):
#   docker build -f cmd/bridge-relayer/deploy/Dockerfile -t splendor/bridge-relayer:testnet .
#
# The image ships only the static binary on distroless — no shell, no package
# manager, nothing to pivot to. The operator .sqwallet and SQC_WALLET_PASS are
# injected at RUNTIME from AWS Secrets Manager (see AWS.md), never baked in.

FROM golang:1.25 AS build
WORKDIR /src
COPY . .
# Static, stripped, reproducible. CGO off so it runs on distroless/static.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bridge-relayer ./cmd/bridge-relayer

FROM gcr.io/distroless/static-debian12:nonroot
# State dir is a mounted volume (EFS/EBS); the wallet arrives via tmpfs at runtime.
VOLUME ["/var/sqc"]
COPY --from=build /out/bridge-relayer /bridge-relayer
USER nonroot:nonroot
ENTRYPOINT ["/bridge-relayer"]
