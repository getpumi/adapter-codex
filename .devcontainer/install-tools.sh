#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: The Pumi Authors

# Downloads the release binaries that are pinned by version and checksum
# (decision 0086) and installs them under /usr/local. A download whose
# checksum does not match stops the script, and so the image build.
#
# The checksums are copied from what each project publishes with its release:
# golangci-lint's checksum file, which has SHA-256 checksums, and the
# integrity of the Codex CLI's npm packages in the npm registry, which is a
# SHA-512 checksum in base64, here in hex.

set -eu

GOLANGCI_LINT_VERSION=2.15.0
GOLANGCI_LINT_SHA256_AMD64=9b7fe6a6bf6334b98decf8d86b11415ea7889afdabdd1959fdce23af2a9ebcea
GOLANGCI_LINT_SHA256_ARM64=b4d8f17068b35ee6060cc2f69a16fb48740d706fbba81661c318649c114a6998

# The Codex CLI, which the adapter's conformance tests run (decision 0244).
# The npm package @openai/codex at the version <version>-linux-<arch> holds
# the native codex for Linux, built with musl, in package/vendor/<target>,
# with the programs and resources it runs beside it. The script installs
# that directory as /usr/local/lib/codex and links codex into
# /usr/local/bin.
CODEX_VERSION=0.172.0
CODEX_SHA512_X64=609aba5b3dcf1575792d9d0e41cdffa3e829337a4b990ffc9fcb8cdb317f9b43bff33103fae48f4fc2e7d4a191aea47c43a7a89fd61d5a586e780ba755367e7a
CODEX_SHA512_ARM64=a8b1f955d79390f9649d680a914d7522518ccb2c4a5e9f6f723eb99626d3dd9a783f72adee011b2b86035e50c1a96b40d3df9f1a7214a56111b3a05a368f3a0c

# download downloads the archive at $1 to the directory $2 and checks it
# against the checksum in $3 with the program in $4, sha256sum or sha512sum.
# It prints the archive's path.
download() {
	archive=$2/$(basename "$1")
	curl --fail --silent --show-error --location --output "$archive" "$1"
	echo "$3  $archive" | "$4" --check --strict >&2
	echo "$archive"
}

case "$(dpkg --print-architecture)" in
amd64)
	golangci_lint_arch=amd64
	golangci_lint_sha256=$GOLANGCI_LINT_SHA256_AMD64
	codex_arch=x64
	codex_target=x86_64-unknown-linux-musl
	codex_sha512=$CODEX_SHA512_X64
	;;
arm64)
	golangci_lint_arch=arm64
	golangci_lint_sha256=$GOLANGCI_LINT_SHA256_ARM64
	codex_arch=arm64
	codex_target=aarch64-unknown-linux-musl
	codex_sha512=$CODEX_SHA512_ARM64
	;;
*)
	echo "install-tools.sh: unsupported architecture $(dpkg --print-architecture)" >&2
	exit 1
	;;
esac

dir=$(mktemp -d)

golangci_lint=golangci-lint-$GOLANGCI_LINT_VERSION-linux-$golangci_lint_arch
archive=$(download \
	"https://github.com/golangci/golangci-lint/releases/download/v$GOLANGCI_LINT_VERSION/$golangci_lint.tar.gz" \
	"$dir" "$golangci_lint_sha256" sha256sum)
tar --extract --gzip --file "$archive" --directory "$dir" "$golangci_lint/golangci-lint"
install -m 0755 "$dir/$golangci_lint/golangci-lint" /usr/local/bin/

archive=$(download \
	"https://registry.npmjs.org/@openai/codex/-/codex-$CODEX_VERSION-linux-$codex_arch.tgz" \
	"$dir" "$codex_sha512" sha512sum)
tar --extract --gzip --file "$archive" --directory "$dir" "package/vendor/$codex_target"
mv "$dir/package/vendor/$codex_target" /usr/local/lib/codex
ln -s /usr/local/lib/codex/bin/codex /usr/local/bin/codex

rm -rf "$dir"
