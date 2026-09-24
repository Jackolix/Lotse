#!/bin/sh
# Installs the Lotse agent as a system service on Linux (systemd) or macOS (launchd).
# Usage: curl -fsSL HUB/install.sh | sudo sh -s -- --hub HUB --key 'ssh-ed25519 ...' --token TOKEN
set -eu

NAME=lotse-agent
HUB=""
KEY=""
TOKEN=""

while [ $# -gt 0 ]; do
	case "$1" in
	--hub) HUB="$2"; shift 2 ;;
	--key) KEY="$2"; shift 2 ;;
	--token) TOKEN="$2"; shift 2 ;;
	*) echo "unknown option: $1" >&2; exit 1 ;;
	esac
done

if [ -z "$HUB" ] || [ -z "$KEY" ]; then
	echo "usage: install.sh --hub URL --key 'ssh-ed25519 ...' [--token TOKEN]" >&2
	exit 1
fi
if [ "$(id -u)" -ne 0 ]; then
	echo "please run as root (sudo)" >&2
	exit 1
fi

case "$(uname -s)" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BIN=/usr/local/bin/$NAME
URL="$HUB/download/$NAME-$OS-$ARCH"
mkdir -p "$(dirname "$BIN")"
# Download next to the target and rename, so a running binary is replaced atomically.
TMP="$(mktemp "$BIN.XXXXXX")"
trap 'rm -f "$TMP"' EXIT

echo "Downloading $NAME for $OS/$ARCH from $HUB ..."
if command -v curl >/dev/null 2>&1; then
	curl -fsSL --compressed -o "$TMP" "$URL"
elif command -v wget >/dev/null 2>&1; then
	wget -qO "$TMP" "$URL"
else
	echo "curl or wget is required" >&2
	exit 1
fi
chmod 755 "$TMP"
mv -f "$TMP" "$BIN"
trap - EXIT

"$BIN" install --hub="$HUB" --key="$KEY" --token="$TOKEN"
