#!/bin/sh
# Installs the Lotse agent as a system service on Linux (systemd) or macOS (launchd).
# Usage: curl -fsSL HUB/install.sh | sudo sh -s -- --hub HUB --key 'ssh-ed25519 ...' --token TOKEN [--allow-shell] [--no-updates]
set -eu

NAME=lotse-agent
HUB=""
KEY=""
TOKEN=""
ALLOW_SHELL=false
NO_UPDATES=false

while [ $# -gt 0 ]; do
	case "$1" in
	--hub) HUB="$2"; shift 2 ;;
	--key) KEY="$2"; shift 2 ;;
	--token) TOKEN="$2"; shift 2 ;;
	--allow-shell) ALLOW_SHELL=true; shift ;;
	--no-updates) NO_UPDATES=true; shift ;;
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

URL="$HUB/download/$NAME-$OS-$ARCH"

download() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --compressed -o "$1" "$URL"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$1" "$URL"
	else
		echo "curl or wget is required" >&2
		return 1
	fi
}

# Systems with a read-only /usr (ZimaOS, Fedora CoreOS, ...) get the agent in /opt or
# /var/lib instead. A location must be writable and allow running programs (not noexec).
echo "Downloading $NAME for $OS/$ARCH from $HUB ..."
BIN=""
for dir in /usr/local/bin "/opt/$NAME" "/var/lib/$NAME"; do
	{ mkdir -p "$dir" && [ -w "$dir" ]; } 2>/dev/null || continue
	# Download next to the target and rename, so a running binary is replaced atomically.
	TMP="$(mktemp "$dir/.$NAME.XXXXXX" 2>/dev/null)" || continue
	trap 'rm -f "$TMP"' EXIT
	download "$TMP"
	chmod 755 "$TMP"
	if "$TMP" version >/dev/null 2>&1; then
		mv -f "$TMP" "$dir/$NAME"
		trap - EXIT
		BIN="$dir/$NAME"
		break
	fi
	rm -f "$TMP"
	trap - EXIT
done
if [ -z "$BIN" ]; then
	echo "found no writable location that allows running programs (tried /usr/local/bin, /opt/$NAME, /var/lib/$NAME)" >&2
	exit 1
fi
echo "Installed $BIN"

"$BIN" install --hub="$HUB" --key="$KEY" --token="$TOKEN" --allow-shell="$ALLOW_SHELL" --no-updates="$NO_UPDATES"
