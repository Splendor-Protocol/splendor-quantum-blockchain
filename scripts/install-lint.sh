#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
version=$(cat .golangci-version)
binary="$PWD/.tools/bin/golangci-lint"
if [ -x "$binary" ] && [ "$("$binary" version --short)" = "${version#v}" ]; then
  exit 0
fi

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "Use Linux, macOS, or WSL for make lint." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

archive="golangci-lint-${version#v}-$os-$arch.tar.gz"
url="https://github.com/golangci/golangci-lint/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl --fail --silent --show-error --location --retry 3 "$url/$archive" -o "$tmp/$archive"
curl --fail --silent --show-error --location --retry 3 "$url/golangci-lint-${version#v}-checksums.txt" -o "$tmp/checksums.txt"
expected=$(awk -v file="$archive" '$2 == file {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "Checksum verification failed for $archive" >&2
  exit 1
fi
tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p .tools/bin
cp "$tmp/golangci-lint-${version#v}-$os-$arch/golangci-lint" "$binary"
"$binary" version
