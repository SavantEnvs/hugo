# syntax=docker/dockerfile:1.7
#
# Commit-image Dockerfile for hugo (Go, native libFuzzer). Builds on the org base
# (clang for the libFuzzer link), adds a pinned Go under /opt/toolchains/go with
# GOPATH/GOMODCACHE/GOCACHE at fixed, $HOME-independent paths so the module cache the
# first (online) build fills is found again when build.sh re-runs OFFLINE.
#
#   DOCKER_BUILDKIT=1 docker build -f mayhem/Dockerfile \
#       --build-arg COMMIT=$(git rev-parse HEAD) -t hugo-commit:test .

FROM ghcr.io/savantenvs/base:20261023

ARG COMMIT
ARG REPO_URL
ARG SOURCE_DATE_EPOCH
ARG SANITIZER_FLAGS

# go.mod requires go >= 1.27.0.
ARG GO_VERSION=1.27.4
ARG GO_SHA256=7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840

ENV GOROOT=/opt/toolchains/go \
    GOPATH=/opt/toolchains/go-path \
    GOMODCACHE=/opt/toolchains/go-path/pkg/mod \
    GOCACHE=/opt/toolchains/go-path/build-cache \
    GOTOOLCHAIN=local \
    GOFLAGS=-mod=mod \
    PATH=/opt/toolchains/go/bin:/opt/toolchains/go-path/bin:${PATH}

USER root
RUN install -d /opt/toolchains/go-path \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz \
 && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
 && tar -C /opt/toolchains -xzf /tmp/go.tgz && rm -f /tmp/go.tgz \
 && chown -R 2000:2000 /opt/toolchains/go-path

USER mayhem
WORKDIR /mayhem
COPY --chown=2000:2000 . /mayhem

# Populates $GOMODCACHE (online, this first build) and builds the libFuzzer targets.
RUN bash mayhem/build.sh

RUN bash mayhem/test.sh

LABEL org.opencontainers.image.source="${REPO_URL}" \
      org.opencontainers.image.revision="${COMMIT}" \
      rlenv.tier="commit" \
      rlenv.source_date_epoch="${SOURCE_DATE_EPOCH}" \
      rlenv.user="mayhem"

USER mayhem
WORKDIR /mayhem
