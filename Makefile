# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: The Pumi Authors

# The commands that developers, agents and CI run, as in the core repository
# (decision 0088).

.DEFAULT_GOAL := build

# go test picks a new random seed when SHUFFLE is "on" and prints it as
# "-test.shuffle <seed>" when a test fails. Run "make test SHUFFLE=<seed>" to
# repeat that order.
SHUFFLE ?= on
TEST_FLAGS := -race -shuffle=$(SHUFFLE) -count=1

# IMAGE is the repository in the GitHub container registry that holds the
# devcontainer images (decision 0113).
IMAGE ?= ghcr.io/getpumi/adapter-codex-devcontainer

# RANGE is the range of commits that make dco checks. CI sets it to the base
# and head commits of the pull request.
RANGE ?= origin/main..HEAD

# BASE is the commit against which make spdx checks only the files added or
# changed up to HEAD. When it is empty, make spdx checks every file of the
# tree. CI sets it to the base commit of the pull request.
BASE ?=

.PHONY: build test lint dco spdx image

# build compiles every package and writes the adapter's binary to bin/.
build:
	go build ./...
	go build -o bin/ ./cmd/...

# test runs the tests and writes a coverage profile to coverage/.
test:
	mkdir -p coverage
	go test $(TEST_FLAGS) -coverprofile=coverage/adapter.out ./...

# lint runs the nolint check on the whole repository (decision 0110), first
# because it is fast, and then golangci-lint with .golangci.yml (decision
# 0109).
lint:
	go run ./internal/checks/cmd/nolint
	golangci-lint run ./...

# dco runs the DCO check (decisions 0070 and 0081) on the commits in RANGE,
# merge commits included.
dco:
	go run ./internal/checks/cmd/dco "$(RANGE)"

# spdx runs the SPDX header check (decision 0068) on every file of the tree,
# or, when BASE is set, on the files added or changed between BASE and HEAD.
spdx:
	go run ./internal/checks/cmd/spdx $(if $(BASE),"$(BASE)")

# image prints the reference by digest of the devcontainer image for the
# committed content of .devcontainer/, which it tags with the git tree hash of
# that directory (decision 0113). It builds and pushes the image first when the
# registry reports that the tag is not found; any other registry error stops
# it, so an existing tag is never rebuilt. Everything except the reference goes
# to stderr. It needs a docker login to ghcr.io.
image:
	@test -z "$$(git status --porcelain -- .devcontainer)" || { echo "make image: .devcontainer/ has uncommitted changes" >&2; exit 1; }
	@ref=$(IMAGE):$$(git rev-parse HEAD:.devcontainer) && \
	if err=$$(docker buildx imagetools inspect "$$ref" 2>&1 >/dev/null); then \
		echo "make image: found $$ref" >&2; \
	elif echo "$$err" | grep -q ': not found$$'; then \
		echo "make image: building $$ref" >&2 && \
		docker build --tag "$$ref" .devcontainer >&2 && \
		docker push "$$ref" >&2; \
	else \
		echo "$$err" >&2; exit 1; \
	fi && \
	digest=$$(docker buildx imagetools inspect "$$ref" --format '{{.Manifest.Digest}}') && \
	echo "$$digest" | grep -Eqx 'sha256:[0-9a-f]{64}' && \
	echo "$(IMAGE)@$$digest"
