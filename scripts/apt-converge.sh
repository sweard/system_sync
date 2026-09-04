#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
export SYSTEM_SYNC_REASON_MANAGER="apt"
exec "$SCRIPT_DIR/linux-reason-converge.sh" "$@"
