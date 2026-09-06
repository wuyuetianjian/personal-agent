#!/usr/bin/env sh
set -eu

PREFIX="${PACHAT_PREFIX:-$HOME/.local}"
CONFIG="${PACHAT_CONFIG:-$HOME/.config/pachat/config.yaml}"
DATA_DIR="${PACHAT_DATA_DIR:-$HOME/.local/share/pachat}"
NEW_BIN="${1:?path to new pachat binary is required}"
BIN="$PREFIX/bin/pachat"
BACKUP="$DATA_DIR/backups/pre-upgrade-$(date -u +%Y%m%dT%H%M%SZ).zip"

mkdir -p "$DATA_DIR/backups"

if [ -x "$BIN" ]; then
  "$BIN" backup create --config "$CONFIG" --output "$BACKUP"
  "$BIN" config validate --config "$CONFIG"
fi

install -m 0755 "$NEW_BIN" "$BIN"
"$BIN" config validate --config "$CONFIG"
"$BIN" storage migrate --config "$CONFIG"

echo "upgraded=$BIN"
echo "backup=$BACKUP"
