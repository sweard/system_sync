#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/helper-common.sh"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off
export GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT="" CGO_ENABLED=1
export GOCACHE="$HELPER_ROOT/.system-sync/go/build" GOMODCACHE="$HELPER_ROOT/.system-sync/go/mod"
cd -- "$HELPER_ROOT"
exec go test -mod=vendor -race ./...
