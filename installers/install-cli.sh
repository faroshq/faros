#!/bin/bash
set -e

# --- helper functions for logs ---
info()
{
    echo '[INFO] ' "$@"
}
fatal()
{
    echo '[ERROR] ' "$@" >&2
    exit 1
}

# --- define needed environment variables ---
setup_env() {
    # --- use sudo if we are not already root ---
    SUDO=sudo
    if [ $(id -u) -eq 0 ]; then
        SUDO=
    fi

    # --- use binary install directory if defined or create default ---
    if [ -n "${INSTALL_BIN_DIR}" ]; then
        BIN_DIR=${INSTALL_BIN_DIR}
    else
        BIN_DIR=/usr/local/bin
    fi

    # --- synpse CLI version ---
    if [ -z "${VERSION}" ]; then
        VERSION=latest
    fi
}

# --- set arch and suffix, fatal if architecture not supported ---
setup_verify_arch() {
    if [ -z "$ARCH" ]; then
        ARCH=$(uname -m)
    fi
    case $ARCH in
        amd64)
            ARCH=amd64
            ;;
        x86_64)
            ARCH=amd64
            ;;
        arm64)
            ARCH=arm64
            ;;
        aarch64)
            ARCH=arm64
            ;;
        arm*)
            ARCH=arm
            ;;
        *)
            fatal "Unsupported architecture $ARCH"
    esac
}

setup_verify_os() {
    OS=$(uname | tr '[:upper:]' '[:lower:]')
    OS_CYGWIN=0
    case "$OS" in
        darwin) OS='darwin';;
        linux) OS='linux';;
        # freebsd) OS='freebsd';;
        mingw*) OS='windows';;
        msys*) OS='windows';;
	cygwin*)
	    OS='windows'
	    OS_CYGWIN=1
	    ;;
        *) echo "OS ${OS} is not supported by this installation script"; exit 1;;
    esac

    BINARY="synpse"

    # add .exe if on windows
    if [ "$OS" = "windows" ]; then
        BINARY="$BINARY.exe"
    fi

    info "OS = $OS"
}

# --- verify existence of network downloader executable ---
verify_downloader() {
    # Return failure if it doesn't exist or is no executable
    [ -x "$(which $1 2>/dev/null)" ] || return 1

    # Set verified executable as our downloader program and return success
    DOWNLOADER=$1
    return 0
}

# --- create tempory directory and cleanup when done ---
setup_tmp() {
    TMP_DIR=$(mktemp -d -t synpse-cli.XXXXXXXXXX)
    TMP_BIN=${TMP_DIR}/synpse-cli
    cleanup() {
        code=$?
        set +e
        trap - EXIT
        rm -rf ${TMP_DIR}
        exit $code
    }
    trap cleanup INT EXIT
}

# --- download from github url ---
download() {
    [ $# -eq 2 ] || fatal 'download needs exactly 2 arguments'

    case $DOWNLOADER in
        curl)
            curl -o $1 -sfL $2
            ;;
        wget)
            wget -qO $1 $2
            ;;
        *)
            fatal "Incorrect executable '$DOWNLOADER'"
            ;;
    esac

    # Abort if download command failed
    [ $? -eq 0 ] || fatal 'Download failed'
}

# --- download binary from github url ---
download_binary() {
    BIN_URL=https://downloads.faros.sh/cli/${OS}-${ARCH}
    info "Downloading binary ${BIN_URL}"
    download ${TMP_BIN} ${BIN_URL}
}

# --- setup permissions and move binary to system directory ---
setup_binary() {
    chmod 755 ${TMP_BIN}
    info "Installing faros CLI to ${BIN_DIR}/faros"
     # TODO: root:root does not work on darwin
    if [  $DISTRO="darwin" ]; then
       $SUDO chown root ${TMP_BIN}
    else
       $SUDO chown root:root ${TMP_BIN}
    fi
    $SUDO mv -f ${TMP_BIN} ${BIN_DIR}/faros
    $SUDO cp ${BIN_DIR}/faros ${BIN_DIR}/kubectl faros
}

# --- download and verify ---
download_and_verify() {
    setup_verify_os
    setup_verify_arch
    verify_downloader curl || verify_downloader wget || fatal 'Can not find curl or wget for downloading files'
    setup_tmp
    download_binary
    setup_binary
}

# --- run the install process --
{
    setup_env
    download_and_verify
}
