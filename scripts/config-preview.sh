#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# Compatibility entry; machine-init and everyday tasks share the Go module.
exec "$SCRIPT_DIR/../helper/scripts/run.sh" config diff "$@"
