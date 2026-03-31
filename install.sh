#!/usr/bin/env bash
set -e

BINARY_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.config/steamdiscordrpc"
CONFIG_FILE="$CONFIG_DIR/config.json"
BINARY="$BINARY_DIR/steamdiscordrpc"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Building steamdiscordrpc..."
cd "$SCRIPT_DIR"
go build -o steamdiscordrpc .

echo "==> Installing binary to $BINARY..."
mkdir -p "$BINARY_DIR"
cp steamdiscordrpc "$BINARY"
chmod +x "$BINARY"

echo "==> Setting up config..."
mkdir -p "$CONFIG_DIR"
if [ -f "$CONFIG_FILE" ]; then
    echo "    Config already exists at $CONFIG_FILE, skipping."
else
    cp "$SCRIPT_DIR/config.example.json" "$CONFIG_FILE"
    echo "    Config copied to $CONFIG_FILE"
    echo "    Edit it and fill in your Discord app ID and SteamGridDB API key."
fi

# Check if ~/.local/bin is in PATH
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$BINARY_DIR"; then
    echo ""
    echo "  WARNING: $BINARY_DIR is not in your PATH."
    echo "  Add this to your shell profile (~/.bashrc, ~/.zshrc, ~/.config/fish/config.fish, etc.):"
    echo ""
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo "  (fish: set -Ux fish_user_paths \$HOME/.local/bin \$fish_user_paths)"
    echo ""
    echo "  Then restart your shell or source your profile before using the launch option below."
fi

echo ""
echo "Done!"
