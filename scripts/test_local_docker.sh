#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
IMAGE="arm64v8/ubuntu:18.04"

echo "[1/3] Checking QEMU binfmt registration for ARM64 emulation..."
HOST_ARCH="$(uname -m)"
if [[ "${HOST_ARCH}" != "aarch64" && "${HOST_ARCH}" != "arm64" ]]; then
    docker run --rm --privileged multiarch/qemu-user-static --reset -p yes >/dev/null
fi

echo "[2/3] Launching ${IMAGE} (glibc 2.27 target environment)..."
docker run --rm --platform linux/arm64 \
    -v "${REPO_ROOT}:/workspace" \
    -w /workspace \
    "${IMAGE}" \
    /bin/bash -c '
        set -euo pipefail
        export DEBIAN_FRONTEND=noninteractive

        echo "--- Verifying Target Architecture & glibc 2.27 ---"
        ARCH="$(uname -m)"
        echo "Container architecture: ${ARCH}"
        if [[ "${ARCH}" != "aarch64" ]]; then
            echo "ERROR: Expected aarch64 architecture, got ${ARCH}" >&2
            exit 1
        fi

        LDD_OUT="$(ldd --version | head -n 1)"
        echo "Container libc: ${LDD_OUT}"
        if [[ "${LDD_OUT}" != *"2.27"* ]]; then
            echo "ERROR: Expected glibc 2.27 in Ubuntu 18.04 container, got: ${LDD_OUT}" >&2
            exit 1
        fi

        echo "--- Installing Go Toolchain (official arm64 tarball; bionic has no recent golang package) ---"
        apt-get update -qq
        apt-get install -y -qq --no-install-recommends ca-certificates wget binutils >/dev/null
        wget -q https://go.dev/dl/go1.22.5.linux-arm64.tar.gz -O /tmp/go.tgz
        tar -C /usr/local -xzf /tmp/go.tgz
        export PATH="/usr/local/go/bin:${PATH}"
        go version

        echo "--- Compiling Static Test Binary (CGO_ENABLED=0) ---"
        CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -ldflags="-s -w" -o /tmp/protocol.test ./common/protocol
        CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /tmp/anytable-server ./server/src

        echo "--- Checking Binary Linkage (Zero Dynamic glibc Dependencies) ---"
        for bin in /tmp/protocol.test /tmp/anytable-server; do
            if ldd "${bin}" 2>&1 | grep -E "not a dynamic executable|statically linked"; then
                echo "PASS: ${bin} is statically linked with zero dynamic glibc dependencies."
            else
                echo "ERROR: ${bin} has unexpected dynamic dependencies:" >&2
                ldd "${bin}" >&2 || true
                exit 1
            fi
        done

        echo "--- Executing Protocol Unit Tests on ARM64 Ubuntu 18.04 ---"
        /tmp/protocol.test -test.v
    '

echo "[3/3] Verification succeeded on ${IMAGE} (glibc 2.27)."
