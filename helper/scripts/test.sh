#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off
export GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT="" CGO_ENABLED=1
export GOCACHE="$REPO_ROOT/.system-sync/go/build" GOMODCACHE="$REPO_ROOT/.system-sync/go/mod"
REQUIRED_GO_VERSION="$(helper_go_version)"
GO_RECOVERY="请先运行 mise install go@${REQUIRED_GO_VERSION}，再运行 mise run --tool go@${REQUIRED_GO_VERSION} check"
command -v go >/dev/null 2>&1 || helper_error "测试需要 Go ${REQUIRED_GO_VERSION}，当前 PATH 中没有 go；$GO_RECOVERY"
ACTUAL_GO_VERSION="$(go env GOVERSION)" || helper_error "无法读取当前 Go 版本；$GO_RECOVERY"
[[ "$ACTUAL_GO_VERSION" == "go$REQUIRED_GO_VERSION" ]] || helper_error "固定测试工具链为 Go ${REQUIRED_GO_VERSION}，当前为 ${ACTUAL_GO_VERSION:-未知}；$GO_RECOVERY"
cd -- "$HELPER_DIR"
system_sync_step "运行 Go race 测试（包含编译准备）" go test -mod=vendor -race -v ./...
