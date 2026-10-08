#!/bin/sh

if [ -n "$CONFIG_URL" ]; then
    case "$CONFIG_URL" in
        http://*|https://*) ;;
        *) echo "[ERROR] CONFIG_URL must be an HTTP(S) URL"; exit 1 ;;
    esac
    echo "[INFO] Loading remote configuration"
    # The Go loader validates the response without overwriting local or mounted files.
    exec /app/saveany-bot --config "$CONFIG_URL"
fi

exec /app/saveany-bot
