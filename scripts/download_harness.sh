#!/usr/bin/env bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Downloads the localharness binary from the google-antigravity PyPI package.
# Usage: ./scripts/download_harness.sh [version]
#
# The binary is extracted from the platform-specific wheel and placed in bin/.
# No Python installation is required.

set -euo pipefail

PACKAGE_NAME="google-antigravity"
VERSION="${1:-}"  # Optional: pass a specific version, otherwise uses latest
BIN_DIR="$(cd "$(dirname "$0")/.." && pwd)/bin"

# --- Detect platform ---
OS="$(uname -s)"
ARCH="$(uname -m)"

case "${OS}" in
  Darwin)
    case "${ARCH}" in
      arm64)  PLATFORM_TAG="macosx_11_0_arm64" ;;
      x86_64) PLATFORM_TAG="macosx_10_9_x86_64" ;;
      *)      echo "Error: Unsupported macOS architecture: ${ARCH}" >&2; exit 1 ;;
    esac
    BINARY_NAME="localharness"
    ;;
  Linux)
    case "${ARCH}" in
      x86_64)  PLATFORM_TAG="manylinux_2_17_x86_64.manylinux2014_x86_64" ;;
      aarch64) PLATFORM_TAG="manylinux_2_17_aarch64.manylinux2014_aarch64" ;;
      *)       echo "Error: Unsupported Linux architecture: ${ARCH}" >&2; exit 1 ;;
    esac
    BINARY_NAME="localharness"
    ;;
  MINGW*|MSYS*|CYGWIN*|Windows_NT)
    case "${ARCH}" in
      x86_64|AMD64)  PLATFORM_TAG="win_amd64" ;;
      aarch64|ARM64) PLATFORM_TAG="win_arm64" ;;
      *)             echo "Error: Unsupported Windows architecture: ${ARCH}" >&2; exit 1 ;;
    esac
    BINARY_NAME="localharness.exe"
    ;;
  *)
    echo "Error: Unsupported OS: ${OS}" >&2
    exit 1
    ;;
esac

echo "==> Detected platform: ${OS}/${ARCH} (wheel tag: ${PLATFORM_TAG})"

# --- Query PyPI for download URL ---
echo "==> Querying PyPI for ${PACKAGE_NAME}..."
PYPI_JSON=$(curl -sSfL "https://pypi.org/pypi/${PACKAGE_NAME}/json")

if [ -z "${VERSION}" ]; then
  VERSION=$(echo "${PYPI_JSON}" | grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4)
  echo "==> Latest version: ${VERSION}"
fi

# Find the matching wheel URL from the release files
WHEEL_URL=$(echo "${PYPI_JSON}" | \
  grep -o "\"url\":\"[^\"]*${PLATFORM_TAG}[^\"]*\.whl\"" | \
  head -1 | \
  cut -d'"' -f4)

if [ -z "${WHEEL_URL}" ]; then
  echo "Error: Could not find a wheel for platform '${PLATFORM_TAG}' in ${PACKAGE_NAME} v${VERSION}" >&2
  echo "" >&2
  echo "Available wheels:" >&2
  echo "${PYPI_JSON}" | grep -o '"url":"[^"]*\.whl"' | cut -d'"' -f4 | sed 's/^/  /' >&2
  exit 1
fi

echo "==> Downloading wheel: $(basename "${WHEEL_URL}")"

# --- Download and extract ---
TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

WHEEL_PATH="${TMPDIR}/package.whl"
curl -sSfL -o "${WHEEL_PATH}" "${WHEEL_URL}"

echo "==> Extracting ${BINARY_NAME}..."

# Wheels are zip files — extract the binary
BINARY_PATH_IN_WHEEL="google/antigravity/bin/${BINARY_NAME}"
unzip -q -o "${WHEEL_PATH}" "${BINARY_PATH_IN_WHEEL}" -d "${TMPDIR}" 2>/dev/null || {
  echo "Error: Binary '${BINARY_PATH_IN_WHEEL}' not found in wheel." >&2
  echo "Contents of wheel:" >&2
  unzip -l "${WHEEL_PATH}" | grep -i "localharness" | sed 's/^/  /' >&2
  exit 1
}

# --- Install to bin/ ---
mkdir -p "${BIN_DIR}"
cp "${TMPDIR}/${BINARY_PATH_IN_WHEEL}" "${BIN_DIR}/${BINARY_NAME}"
chmod +x "${BIN_DIR}/${BINARY_NAME}"

echo "==> Installed: ${BIN_DIR}/${BINARY_NAME}"
echo ""
echo "To use it, either:"
echo "  export ANTIGRAVITY_HARNESS_PATH=\"${BIN_DIR}/${BINARY_NAME}\""
echo "  or add ${BIN_DIR} to your PATH"
echo ""
"${BIN_DIR}/${BINARY_NAME}" --version 2>/dev/null && echo "" || true
echo "Done!"
