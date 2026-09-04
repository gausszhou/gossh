#!/bin/sh
#
# gossh 一键安装脚本
#
#   curl -fsSL https://raw.githubusercontent.com/gausszhou/gossh/main/scripts/install.sh | sh
#   # 指定版本 / 安装前缀 / 镜像仓库:
#   #   sh install.sh --version v0.0.2 --prefix ~/.local --repo owner/gossh
#
# 覆盖平台:Linux / macOS / Windows(**Git Bash**、MSYS2、Cygwin 的 sh 环境)。
# 流程:探测平台架构 → 取发布信息(latest 或 --version)→ 下载二进制与
# sha256sums.txt → 校验(校验器不可用时降级跳过并告警)→ 安装到 --prefix
# (默认 $HOME/.local/bin,用户目录,不请求 sudo)。
#
# 私有部署:GOSSH_UPDATE_URL 指向一个「GitHub release 同形状」的 JSON 索引
# (静态站点可托管),脚本改从该地址取版本与资产 URL。
set -eu

VERSION="${GOSSH_VERSION:-}"
PREFIX="${GOSSH_PREFIX:-$HOME/.local}"
REPO="${GOSSH_REPO:-gausszhou/gossh}"

usage() {
    sed -n '2,14p' "$0"
    echo
    echo "options:"
    echo "  --version <tag>    Target version tag, e.g. v0.0.2 (default: latest release)"
    echo "  --prefix <dir>     Install prefix (default: \$HOME/.local, binary at <prefix>/bin/gossh)"
    echo "  --repo <owner/name> GitHub repository to fetch from (default: gausszhou/gossh)"
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version) VERSION="$2"; shift 2 ;;
        --prefix) PREFIX="$2"; shift 2 ;;
        --repo) REPO="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage; exit 1 ;;
    esac
done

# --- 平台/架构探测(与 Makefile 矩阵命名对齐 gossh-{os}-{arch}[.exe]) ---
# Git Bash / MSYS2 / Cygwin 的 uname -s 返回 MINGW64_NT-... / MSYS_NT-... / CYGWIN_NT-...
OS="$(uname -s 2>/dev/null || true)"
case "$OS" in
    Linux) GOOS=linux ;;
    Darwin) GOOS=darwin ;;
    MINGW*|MSYS*|CYGWIN*) GOOS=windows ;;
    *) echo "unsupported OS: $OS (install.sh covers Linux/macOS, and Windows via Git Bash/MSYS2)" >&2; exit 1 ;;
esac
MACH="$(uname -m 2>/dev/null || true)"
case "$MACH" in
    x86_64|amd64) GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    *) echo "unsupported architecture: $MACH (releases cover amd64/arm64)" >&2; exit 1 ;;
esac
ASSET="gossh-$GOOS-$GOARCH"
# Windows 资产带 .exe 后缀;安装目标名同样带 .exe(否则 Windows 无法执行)
if [ "$GOOS" = windows ]; then
    ASSET="$ASSET.exe"
    BIN="gossh.exe"
else
    BIN="gossh"
fi

# --- 取发布信息 ----------------------------------------------------------
BASE_URL="https://github.com/$REPO/releases"
if [ -n "${GOSSH_UPDATE_URL:-}" ]; then
    # 私有镜像:JSON 形如 { "tag_name": "v0.0.2", "assets": [ { "name": "gossh-windows-amd64.exe", "browser_download_url": "..." } ] }
    RELEASE_JSON="$(curl -fsSL "$GOSSH_UPDATE_URL")"
elif [ -n "$VERSION" ]; then
    RELEASE_JSON="$(curl -fsSL "$BASE_URL/download/$VERSION/release.json" 2>/dev/null || true)"
    [ -n "$RELEASE_JSON" ] || RELEASE_JSON="{\"tag_name\":\"$VERSION\"}"
else
    RELEASE_JSON="$(curl -fsSL "$BASE_URL/latest/download/release.json" 2>/dev/null || true)"
    if [ -z "$RELEASE_JSON" ]; then
        # release 资产未挂 release.json 时,退化为最新 tag API
        RELEASE_JSON="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest")"
    fi
fi

TAG="$(echo "$RELEASE_JSON" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
if [ -z "$TAG" ]; then
    if [ -n "$VERSION" ]; then TAG="$VERSION"; else
        echo "failed to resolve the latest release tag for $REPO" >&2
        exit 1
    fi
fi
echo "installing gossh $TAG ($GOOS/$GOARCH)"

# 从 release.json 解析资产 URL(带 @name 锚点避免误配);没有则回退到标准下载路径
ASSET_URL="$(echo "$RELEASE_JSON" | tr '\n' ' ' | sed -n "s/.*\"name\":\"$ASSET\"[^}]*\"browser_download_url\":\"\([^\"]*\)\".*/\1/p" | head -1)"
if [ -z "$ASSET_URL" ]; then
    ASSET_URL="$BASE_URL/download/$TAG/$ASSET"
    CHECKSUM_URL="$BASE_URL/download/$TAG/sha256sums.txt"
else
    CHECKSUM_URL="$(echo "$RELEASE_JSON" | tr '\n' ' ' | sed -n 's/.*"name":"sha256sums.txt"[^}]*"browser_download_url":"\([^"]*\)".*/\1/p' | head -1)"
    [ -n "$CHECKSUM_URL" ] || CHECKSUM_URL="$BASE_URL/download/$TAG/sha256sums.txt"
fi

# --- 下载与校验 ----------------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "downloading $ASSET ..."
curl -fL --retry 3 --retry-delay 1 -o "$TMP/$ASSET" "$ASSET_URL"
curl -fsSL -o "$TMP/sha256sums.txt" "$CHECKSUM_URL" || true

VERIFIED=false
if [ -s "$TMP/sha256sums.txt" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
        EXPECTED="$(awk -v asset="$ASSET" '$2 == asset {print $1} $1 == asset && NF == 2 {print $2}' "$TMP/sha256sums.txt" | head -1)"
        if [ -z "$EXPECTED" ]; then
            echo "checksum file exists but has no entry for $ASSET; aborting (do not install unverified binaries)" >&2
            exit 1
        fi
        ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
        if [ "$EXPECTED" != "$ACTUAL" ]; then
            echo "checksum mismatch for $ASSET:" >&2
            echo "  expected: $EXPECTED" >&2
            echo "  actual:   $ACTUAL" >&2
            exit 1
        fi
        VERIFIED=true
        echo "checksum verified"
    else
        echo "WARNING: sha256sum not available in this shell (Git Bash?), skipping verification" >&2
    fi
else
    echo "WARNING: sha256sums.txt unavailable (offline mirror?); skipping verification" >&2
fi
if [ "$VERIFIED" = false ]; then
    echo "WARNING: installing without checksum verification. Confirm the release URL is trustworthy." >&2
fi

# --- 安装 ----------------------------------------------------------------
BINDIR="$PREFIX/bin"
mkdir -p "$BINDIR"
chmod +x "$TMP/$ASSET" || true   # Git Bash 下对 .exe 执行 chmod 可能是 no-op
mv -f "$TMP/$ASSET" "$BINDIR/$BIN"

"$BINDIR/$BIN" version

cat <<EOF

installed: $BINDIR/$BIN ($TAG)

使用:
  gossh serve            # 启动并打印带令牌的 URL,浏览器打开即用
  gossh hosts add ...    # 添加主机(或浏览器里填表)
  gossh run <host> 'cmd' # 无浏览器执行单命令

若 $BINDIR 不在 PATH,可加:
  export PATH="$BINDIR:\$PATH"          # bash / Git Bash
EOF