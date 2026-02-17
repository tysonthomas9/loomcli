#!/usr/bin/env bash
set -euo pipefail

# Loom CLI installer
# Usage: curl -fsSL https://raw.githubusercontent.com/tysonthomas9/loomcli/main/scripts/install.sh | bash
#
# Environment variables:
#   INSTALL_DIR  - Where to install the binary (default: ~/.local/bin on macOS, /usr/local/bin on Linux)
#   VERSION      - Specific version to install (default: latest)

REPO="tysonthomas9/loomcli"
BINARY="loom"
DEFAULT_INSTALL_DIR="$HOME/.local/bin"
if [ "$(uname -s)" = "Linux" ]; then
    DEFAULT_INSTALL_DIR="/usr/local/bin"
fi
INSTALL_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"

info() { printf "\033[1;34m==>\033[0m %s\n" "$1"; }
error() { printf "\033[1;31mError:\033[0m %s\n" "$1" >&2; exit 1; }

detect_os() {
    case "$(uname -s)" in
        Darwin*) echo "darwin" ;;
        Linux*)  echo "linux" ;;
        *)       error "Unsupported OS: $(uname -s). Only macOS and Linux are supported." ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)             error "Unsupported architecture: $(uname -m). Only amd64 and arm64 are supported." ;;
    esac
}

get_latest_version() {
    local url="https://api.github.com/repos/${REPO}/releases/latest"
    local tag
    tag=$(curl -fsSL "$url" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
    [ -z "$tag" ] && error "Failed to fetch latest release tag from GitHub"
    echo "$tag"
}

TMPDIR_CLEANUP=""
cleanup() { [ -n "$TMPDIR_CLEANUP" ] && rm -rf "$TMPDIR_CLEANUP"; }
trap cleanup EXIT

main() {
    local os arch tag version archive checksum_file

    os=$(detect_os)
    arch=$(detect_arch)

    if [ -n "${VERSION:-}" ]; then
        tag="v${VERSION#v}"
    else
        info "Fetching latest release..."
        tag=$(get_latest_version)
    fi

    # Strip v prefix for archive name (tags are v0.1.0, archives are loomcli_0.1.0_...)
    version="${tag#v}"
    archive="loomcli_${version}_${os}_${arch}.tar.gz"
    checksum_file="checksums.txt"

    info "Installing loom ${tag} (${os}/${arch})..."

    local tmpdir
    tmpdir=$(mktemp -d)
    TMPDIR_CLEANUP="$tmpdir"

    local base_url="https://github.com/${REPO}/releases/download/${tag}"

    info "Downloading ${archive}..."
    curl -fsSL -o "${tmpdir}/${archive}" "${base_url}/${archive}" ||
        error "Failed to download ${archive}. Check that the release exists for your platform."

    info "Downloading checksums..."
    curl -fsSL -o "${tmpdir}/${checksum_file}" "${base_url}/${checksum_file}" ||
        error "Failed to download checksums."

    info "Verifying checksum..."
    local expected_checksum actual_checksum
    expected_checksum=$(grep "${archive}" "${tmpdir}/${checksum_file}" | awk '{print $1}')
    [ -z "$expected_checksum" ] && error "Checksum not found for ${archive}"

    if command -v sha256sum &>/dev/null; then
        actual_checksum=$(sha256sum "${tmpdir}/${archive}" | awk '{print $1}')
    elif command -v shasum &>/dev/null; then
        actual_checksum=$(shasum -a 256 "${tmpdir}/${archive}" | awk '{print $1}')
    else
        error "No sha256sum or shasum found. Cannot verify checksum."
    fi

    if [ "$expected_checksum" != "$actual_checksum" ]; then
        error "Checksum mismatch!\n  Expected: ${expected_checksum}\n  Got:      ${actual_checksum}"
    fi

    info "Extracting..."
    tar -xzf "${tmpdir}/${archive}" -C "${tmpdir}"

    info "Installing to ${INSTALL_DIR}..."
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR" 2>/dev/null || sudo mkdir -p "$INSTALL_DIR"
    fi
    if [ -w "$INSTALL_DIR" ]; then
        cp "${tmpdir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
        chmod +x "${INSTALL_DIR}/${BINARY}"
    else
        info "Elevated permissions required to install to ${INSTALL_DIR}"
        sudo cp "${tmpdir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY}"
    fi

    info "Verifying installation..."
    if "${INSTALL_DIR}/${BINARY}" --version &>/dev/null; then
        printf "\033[1;32mSuccess!\033[0m loom %s installed to %s\n" "$tag" "${INSTALL_DIR}/${BINARY}"
    else
        printf "\033[1;33mWarning:\033[0m Binary installed but 'loom --version' returned non-zero.\n"
        printf "  Installed to: %s\n" "${INSTALL_DIR}/${BINARY}"
    fi

    # Remind user to add install dir to PATH if needed
    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        printf "\n\033[1;33mNote:\033[0m %s is not in your PATH. Add it with:\n" "$INSTALL_DIR"
        printf "  export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
    fi
}

main
