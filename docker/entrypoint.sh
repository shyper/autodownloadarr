#!/bin/sh
# Runs AutoDownloadarr as PUID:PGID so that files in /config and the media
# folders belong to the host user, as with other *arr containers.
set -eu

config_dir="${AUTODOWNLOADARR_CONFIG_DIR:-/config}"

if [ "$(id -u)" != "0" ]; then
  # Started with --user: nothing to set up.
  exec /app/autodownloadarr "$@"
fi

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

group="$(getent group "$PGID" | cut -d: -f1 || true)"
if [ -z "$group" ]; then
  group=autodownloadarr
  addgroup -g "$PGID" "$group"
fi
if ! getent passwd "$PUID" >/dev/null; then
  adduser -D -H -u "$PUID" -G "$group" -s /sbin/nologin autodownloadarr
fi

mkdir -p "$config_dir"
chown -R "$PUID:$PGID" "$config_dir"

exec su-exec "$PUID:$PGID" /app/autodownloadarr "$@"
