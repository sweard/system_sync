#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off
export GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT="" CGO_ENABLED=1
export GOCACHE="$REPO_ROOT/.system-sync/go/build" GOMODCACHE="$REPO_ROOT/.system-sync/go/mod"
cd -- "$HELPER_DIR"
exec go test -mod=vendor -race ./...
