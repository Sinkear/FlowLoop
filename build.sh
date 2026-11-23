#!/bin/sh
# Builds the ouroflow CLI and daemon with one shared version stamp.
#
#   OUROFLOW_COMPILE_VERSION=0.0.1-beta.2 ./build.sh
#
# The env var overrides the default (0.0.1-beta.1). Both binaries MUST be
# built with the same stamp: the register-time version check between the
# CLI and the daemon warns on a mismatch, and mixed stamps are the symptom
# of a mixed deploy.
set -e
cd "$(dirname "$0")"

VERSION="${OUROFLOW_COMPILE_VERSION:-0.0.1-beta.1}"
LDFLAGS="-X main.cliVersion=$VERSION \
-X github.com/Sinkear/ouroflow/internal/daemon.DaemonVersion=$VERSION"

mkdir -p build
go build -ldflags "$LDFLAGS" -o build/ouroflow ./cmd/ouroflow-cli
go build -ldflags "$LDFLAGS" -o build/ouroflow-daemon ./cmd/ouroflow-daemon
echo "built build/ouroflow (CLI) + build/ouroflow-daemon v$VERSION"
