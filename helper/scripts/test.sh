#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off
export GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT="" CGO_ENABLED=1
export GOCACHE="$REPO_ROOT/.system-sync/go/build" GOMODCACHE="$REPO_ROOT/.system-sync/go/mod"
cd -- "$HELPER_DIR"
system_sync_step "运行 Go race 测试（包含编译准备）" go test -mod=vendor -race -v ./...
