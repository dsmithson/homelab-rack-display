#!/usr/bin/env bash
# Render every screen to renders/<name>.png at exactly 1440x240.
#
# NOTE: `chromium --headless=new --window-size=1440,240` gives a ~153 px tall
# viewport (the new headless mode still reserves window-chrome height). The
# old-headless binary `chrome-headless-shell` honours the window size exactly,
# so it is the default here. Override with CHROME=/path/to/binary.
set -euo pipefail
cd "$(dirname "$0")"
CHROME="${CHROME:-$(ls -d ~/.cache/ms-playwright/chromium_headless_shell-*/chrome-linux/headless_shell 2>/dev/null | tail -1)}"
CHROME="${CHROME:-chrome-headless-shell}"
mkdir -p renders
for f in screens/*.html; do
  n=$(basename "$f" .html)
  "$CHROME" --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
    --window-size=1440,240 --screenshot="$PWD/renders/$n.png" "file://$PWD/$f" >/dev/null 2>&1
  printf '%-28s %s\n' "$n" "$(file -b renders/$n.png | cut -d, -f2)"
done
