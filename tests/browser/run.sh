#!/bin/sh
set -eu
Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp &
display_pid=$!
trap 'kill "$display_pid" 2>/dev/null || true' EXIT
attempt=0
until xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; do
  kill -0 "$display_pid"
  attempt=$((attempt + 1))
  test "$attempt" -lt 50
  sleep 0.1
done
node scripts/profile-compat.mjs
PROFILE_ID=chrome_157_cft_PSK node scripts/profile-compat.mjs
