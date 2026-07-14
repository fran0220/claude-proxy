#!/bin/bash
set -euo pipefail

APP_NAME="ClaudeProxy"
BUNDLE_ID="dev.claudeproxy.desktop"
VERSION="${VERSION:-dev}"
BINARY="claude-proxy"
APP_DIR="${APP_NAME}.app"

echo "=== Building ${APP_NAME} macOS status bar app ==="

echo "→ Compiling arm64 binary..."
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.version=${VERSION}" -o "${BINARY}" .

echo "→ Creating app bundle..."
rm -rf "${APP_DIR}"
mkdir -p "${APP_DIR}/Contents/MacOS"
mkdir -p "${APP_DIR}/Contents/Resources"
mv "${BINARY}" "${APP_DIR}/Contents/MacOS/${BINARY}"

cat > "${APP_DIR}/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleExecutable</key>
    <string>${BINARY}</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>NSSupportsAutomaticGraphicsSwitching</key>
    <true/>
</dict>
</plist>
EOF

echo "→ Generating app icon..."
python3 - <<'PY'
import struct, zlib

def chunk(ctype, data):
    c = ctype + data
    return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)

def png(w, h, r, g, b):
    rows = []
    cx, cy, rad = w / 2, h / 2, w / 2 - 8
    for y in range(h):
        row = b'\x00'
        for x in range(w):
            dx, dy = x - cx, y - cy
            dist = (dx*dx + dy*dy) ** 0.5
            if dist < rad - 2:
                row += bytes([r, g, b, 255])
            elif dist < rad:
                row += bytes([r, g, b, max(0, min(255, int(255 * (rad - dist) / 2)))])
            else:
                row += bytes([0, 0, 0, 0])
        rows.append(row)
    raw = b''.join(rows)
    return (b'\x89PNG\r\n\x1a\n' +
            chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0)) +
            chunk(b'IDAT', zlib.compress(raw)) +
            chunk(b'IEND', b''))

with open('/tmp/claude-proxy-icon-512.png', 'wb') as f:
    f.write(png(512, 512, 0xD9, 0x77, 0x57))
PY

mkdir -p /tmp/ClaudeProxy.iconset
sips -z 16 16     /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_16x16.png      >/dev/null
sips -z 32 32     /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_16x16@2x.png   >/dev/null
sips -z 32 32     /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_32x32.png      >/dev/null
sips -z 64 64     /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_32x32@2x.png   >/dev/null
sips -z 128 128   /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_128x128.png    >/dev/null
sips -z 256 256   /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_128x128@2x.png >/dev/null
sips -z 256 256   /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_256x256.png    >/dev/null
sips -z 512 512   /tmp/claude-proxy-icon-512.png --out /tmp/ClaudeProxy.iconset/icon_256x256@2x.png >/dev/null
cp /tmp/claude-proxy-icon-512.png /tmp/ClaudeProxy.iconset/icon_512x512.png
iconutil -c icns /tmp/ClaudeProxy.iconset -o "${APP_DIR}/Contents/Resources/AppIcon.icns"
rm -rf /tmp/ClaudeProxy.iconset /tmp/claude-proxy-icon-512.png

echo "→ Done"
echo ""
echo "  ${APP_DIR}  ($(du -sh "${APP_DIR}" | cut -f1))"
echo ""
echo "  Run:      open '${APP_DIR}'"
echo "  Install:  cp -R '${APP_DIR}' /Applications/"
