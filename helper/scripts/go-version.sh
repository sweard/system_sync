#!/usr/bin/env bash
# Read the pinned build toolchain without Go, Python, mise or network access.
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
[[ $# -eq 0 ]] || helper_error "用法：go-version.sh"
helper_go_version
