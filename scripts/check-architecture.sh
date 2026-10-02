#!/usr/bin/env bash
set -euo pipefail

# Only the coordinator may issue rover movement commands.
if rg -n '\.Move\(' internal --glob '*.go' --glob '!internal/control/**' --glob '!**/*_test.go'; then
  echo 'Motor commands must be issued by internal/control only.' >&2
  exit 1
fi

if rg -n 'ROVER_API_TOKEN|homePassword|password' internal/store --glob '*.go' --glob '!**/*_test.go'; then
  echo 'Secrets must not be written by the storage package.' >&2
  exit 1
fi
