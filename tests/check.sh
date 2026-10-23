#!/usr/bin/env bash
# Development-only entry: pin Go before building the check runner. No Python.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../helper/scripts/common.sh"
required="$(helper_go_version)"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOTELEMETRY=off GOENV=off GOWORK=off GOFLAGS="" GOEXPERIMENT=""
actual="$(go env GOVERSION 2>/dev/null)" || actual=""
# `where` only selects an installed tool; unlike exec/use it cannot install one.
if [[ "$actual" != "go$required" ]]; then
  installed="$(MISE_AUTO_INSTALL=false MISE_TASK_RUN_AUTO_INSTALL=false MISE_OFFLINE=1 mise where "go@$required" 2>/dev/null)" || installed=""
  if [[ -n "$installed" && -x "$installed/bin/go" ]]; then
    export PATH="$installed/bin:$PATH"
    printf '[~] 选择已安装的固定 Go %s\n' "$required"
  fi
fi
command -v go >/dev/null 2>&1 || helper_error "检查需要 Go $required，当前 PATH 中没有 go；请先运行 mise install go@$required，再运行 mise run --tool go@$required check"
actual="$(go env GOVERSION)" || helper_error "无法读取当前 Go 版本；请先运行 mise install go@$required，再运行 mise run --tool go@$required check"
[[ "$actual" == "go$required" ]] || helper_error "固定检查工具链为 Go $required，当前为 $actual；请先运行 mise install go@$required，再运行 mise run --tool go@$required check"
export GOCACHE="$REPO_ROOT/.system-sync/go/build" GOMODCACHE="$REPO_ROOT/.system-sync/go/mod"
[[ ! -L "$REPO_ROOT/.system-sync" && ! -L "$REPO_ROOT/.system-sync/check" ]] || helper_error "检查结果目录不能是符号链接"
mkdir -p -- "$REPO_ROOT/.system-sync/check"
runner="$(mktemp "$REPO_ROOT/.system-sync/check/.runner-XXXXXX")"
trap 'rm -f -- "$runner"' EXIT
printf '[~] 准备 Go 检查运行器\n'
(cd -- "$HELPER_DIR"; system_sync_step "构建 Go 检查运行器" env CGO_ENABLED=0 go build -mod=vendor -buildvcs=false -o "$runner" ./dev/check)
"$runner" --repo "$REPO_ROOT" "$@"
