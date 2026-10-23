#!/usr/bin/env bash
# Read the stored tools.go using mise's TOML parser; no Go, Python or network needed.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
[[ $# -eq 0 ]] || helper_error "用法：go-version.sh"
helper_go_version
