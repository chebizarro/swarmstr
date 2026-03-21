#!/bin/bash
# Sandbox entrypoint: starts Xvfb (virtual display), optionally noVNC, then bridge server.
set -e

DISPLAY_NUM=${DISPLAY_NUM:-99}
export DISPLAY=":${DISPLAY_NUM}"

# Start virtual display
Xvfb "${DISPLAY}" -screen 0 1280x800x24 -ac &
XVFB_PID=$!
sleep 0.5

# Optional noVNC remote desktop
if [[ "${NOVNC_ENABLED}" == "1" ]]; then
  echo "Starting x11vnc on display ${DISPLAY}..."
  x11vnc -display "${DISPLAY}" -forever -nopw -quiet -rfbport 5900 &

  echo "Starting websockify on port ${NOVNC_PORT}..."
  websockify --web /usr/share/novnc "${NOVNC_PORT}" localhost:5900 &
fi

cleanup() {
  echo "Shutting down sandbox..."
  kill "${XVFB_PID}" 2>/dev/null || true
  wait
}
trap cleanup EXIT TERM INT

echo "Starting metiq browser bridge on ${SANDBOX_LISTEN}..."
exec node /app/server.js
