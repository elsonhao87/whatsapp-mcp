#!/bin/sh
# macOS: build the bridge and (re)install it as a login item that launchd keeps running.
#   ./install.sh            build + install + start
#   ./install.sh uninstall  stop + remove the login item
# Windows: use install.ps1.
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
LABEL=com.elson.whatsapp-bridge
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

if [ "$1" = uninstall ]; then
	launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	rm -f "$PLIST"
	echo "removed $LABEL"
	exit 0
fi

if [ ! -f "$DIR/config.json" ]; then
	sed "s|\"claude_bin\": \"\"|\"claude_bin\": \"$(command -v claude)\"|" "$DIR/config.example.json" > "$DIR/config.json"
	echo "created config.json - set triage_dir (the repo Claude should investigate in) before relying on replies"
fi

# build first: a compile error must not leave the running bridge stopped
(cd "$DIR/whatsapp-bridge" && CGO_ENABLED=0 go build -o whatsapp-bridge .)
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
# bootout returns before the old process is gone; bootstrap fails until it is
while launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; do sleep 1; done
# PATH is passed on so the claude CLI can start its MCP servers (node, uv, ...)
sed -e "s|__DIR__|$DIR|g" -e "s|__PATH__|$PATH|g" "$DIR/launchd/$LABEL.plist" > "$PLIST"
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo "started $LABEL - admin page: http://127.0.0.1:8765 (log: $DIR/whatsapp-bridge/store/bridge.log)"
