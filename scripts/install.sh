#!/usr/bin/env sh
set -eu

PREFIX="${PACHAT_PREFIX:-$HOME/.local}"
CONFIG_DIR="${PACHAT_CONFIG_DIR:-$HOME/.config/pachat}"
DATA_DIR="${PACHAT_DATA_DIR:-$HOME/.local/share/pachat}"
BIN_DIR="$PREFIX/bin"
SOURCE_BIN="${1:-./pachat}"

mkdir -p "$BIN_DIR" "$CONFIG_DIR" "$DATA_DIR"
install -m 0755 "$SOURCE_BIN" "$BIN_DIR/pachat"

if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
  "$BIN_DIR/pachat" init --config "$CONFIG_DIR/config.yaml" --data-dir "$DATA_DIR" --db-path "$DATA_DIR/personal-agent.db"
fi

echo "installed=$BIN_DIR/pachat"
echo "config=$CONFIG_DIR/config.yaml"
echo "data=$DATA_DIR"
