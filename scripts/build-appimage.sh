#!/usr/bin/env bash
# 构建单个 Linux AppImage(桌面形态)。
#
#   scripts/build-appimage.sh [amd64|arm64]
#
# 前置:
#   - 需要 cgo 构建(托盘走 GTK/AppIndicator),arm64 交叉请提供:
#       CC=aarch64-linux-gnu-gcc
#       PKG_CONFIG=aarch64-linux-gnu-pkg-config
#       PKG_CONFIG_LIBDIR=/usr/lib/aarch64-linux-gnu/pkgconfig
#   - appimagetool 在 PATH,或用 APPIMAGETOOL 指向其路径;
#     工具本身是 AppImage 时用 --appimage-extract-and-run(免 FUSE)。
# 产出:build/gossh-linux-<arch>.AppImage
set -euo pipefail

ARCH="${1:-amd64}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/build"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
LDFLAGS="-X github.com/gausszhou/gossh/cmd.Version=$VERSION -X github.com/gausszhou/gossh/cmd.CommitID=$COMMIT -s -w"

echo "== [$ARCH] building gossh (cgo) =="
mkdir -p "$STAGE/usr/bin"
(cd "$ROOT" && CGO_ENABLED=1 GOOS=linux GOARCH="$ARCH" go build -trimpath \
  -ldflags "$LDFLAGS" -o "$STAGE/usr/bin/gossh" .)

echo "== [$ARCH] assembling AppDir =="
mkdir -p "$STAGE/usr/share/applications" "$STAGE/usr/share/icons/hicolor/256x256/apps"
cp "$ROOT/packaging/linux/AppRun" "$STAGE/AppRun"
chmod +x "$STAGE/AppRun"
cp "$ROOT/packaging/linux/gossh.desktop" "$STAGE/gossh.desktop"
cp "$ROOT/packaging/linux/gossh.desktop" "$STAGE/usr/share/applications/gossh.desktop"
cp "$ROOT/assets/icon.png" "$STAGE/gossh.png"
cp "$ROOT/assets/icon.png" "$STAGE/usr/share/icons/hicolor/256x256/apps/gossh.png"

TOOL="${APPIMAGETOOL:-appimagetool}"
echo "== [$ARCH] appimagetool: $TOOL =="
"$TOOL" --appimage-extract-and-run "$STAGE" "$OUT/gossh-linux-$ARCH.AppImage"

echo "== [$ARCH] done: $OUT/gossh-linux-$ARCH.AppImage =="
ls -la "$OUT/gossh-linux-$ARCH.AppImage"