#!/bin/sh
#
# gossh 本地一键构建 + 安装(Git Bash / Linux / macOS),对标 scripts/build-local.ps1。
#
#   scripts/build-local.sh
#   scripts/build-local.sh --no-tray --prefix ~/.local
#
# 流程(与 build-local.ps1 逐步对应):
#   1. 校验环境(node / pnpm;Go 缺失时自动下载安装)
#   2. 构建前端(apps/web → apps/web/dist)
#   3. 同步前端产物到 internal/api/static(go:embed 的输入)
#   4. 编译 Go 二进制(版本号取 git describe --tags --always --dirty)
#   5. 结束正在运行的 gossh(否则无法覆盖已安装的二进制)
#   6. 安装到 <prefix>/bin/gossh[.exe] 并校验
# 脚本幂等:重复执行只会重新构建/覆盖安装,不会重复安装工具链。
#
# options:
#   --no-tray          不结束正在运行的 gossh(跳过第 5 步);目标被占用会直接失败
#   --no-install       只构建出 build/gossh[.exe],不安装到 <prefix>/bin
#   --prefix <dir>     安装前缀,默认 $HOME/.local(bin 目录为 <prefix>/bin)
#   --go-version <v>   自动下载 Go 时指定版本(如 go1.27.1);默认取官方最新稳定版
#   -h, --help         显示本帮助
#
# 环境变量:GOSSH_PREFIX / GOSSH_GO_VERSION 对应上面两个选项;
# GOSSH_GO_ROOT 指定自动安装 Go 的目录(默认 $HOME/.local/go)。
#
# 与 .ps1 的两点差异:二进制后缀按当前平台决定(Unix 为 gossh,Windows 为
# gossh.exe);PATH 注册写到 ~/.bashrc(与 scripts/install.sh 一致),要让
# cmd / PowerShell 也能直接调用 gossh 时请改用 scripts/install.sh 或 .ps1。
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WEB_DIR="$REPO_ROOT/apps/web"
DIST_DIR="$WEB_DIR/dist"
STATIC_DIR="$REPO_ROOT/internal/api/static"
BUILD_DIR="$REPO_ROOT/build"

NO_TRAY=false
NO_INSTALL=false
PREFIX="${GOSSH_PREFIX:-}"
GO_VERSION="${GOSSH_GO_VERSION:-}"

# ── 输出 ──
if [ -t 1 ]; then
    CYAN="$(printf '\033[36m')"
    GREEN="$(printf '\033[32m')"
    YELLOW="$(printf '\033[33m')"
    RED="$(printf '\033[31m')"
    RESET="$(printf '\033[0m')"
else
    CYAN='' GREEN='' YELLOW='' RED='' RESET=''
fi

_step=0
step() {
    _step=$((_step + 1))
    printf '\n%s[%s] %s%s\n' "$CYAN" "$_step" "$1" "$RESET"
}
ok() { printf '    %sok  %s%s\n' "$GREEN" "$1" "$RESET"; }
warn() { printf '    %s!   %s%s\n' "$YELLOW" "$1" "$RESET" >&2; }
die() {
    printf '    %sx   %s%s\n' "$RED" "$1" "$RESET" >&2
    exit 1
}

usage() {
    # 头部注释块即帮助正文(单处维护);末尾那行 `set -eu` 是脚本本体,去掉。
    sed -n '2,/^set -eu$/p' "$0" | sed -e 's/^# //' -e 's/^#$//' -e '/^set -eu$/d'
    echo
    echo "also honors: GOSSH_PREFIX / GOSSH_GO_VERSION / GOSSH_GO_ROOT"
}

while [ $# -gt 0 ]; do
    case "$1" in
        --no-tray) NO_TRAY=true; shift ;;
        --no-install) NO_INSTALL=true; shift ;;
        --prefix)
            PREFIX="${2:-}"
            [ -n "$PREFIX" ] || die "--prefix 需要一个目录参数"
            shift 2
            ;;
        --go-version)
            GO_VERSION="${2:-}"
            [ -n "$GO_VERSION" ] || die "--go-version 需要一个版本参数(如 go1.27.1)"
            shift 2
            ;;
        -h|--help) usage; exit 0 ;;
        *) die "未知选项: $1(用 --help 看用法)" ;;
    esac
done

[ -n "$PREFIX" ] || PREFIX="$HOME/.local"
BIN_DIR="$PREFIX/bin"

# ── 平台 ──
UNAME_S="$(uname -s 2>/dev/null || echo unknown)"
case "$UNAME_S" in
    MINGW*|MSYS*|CYGWIN*) HOST_OS=windows ;;
    *) HOST_OS=unix ;;
esac

# ── 小工具 ──

# native_path <path> —— 原生 Windows 程序(node、go)看不懂 MSYS 的 /d/... 形式,
# 必须换成 D:\...;别指望 MSYS 自动转换:MSYS2_ARG_CONV_EXCL 一经设置(沙箱与
# 部分发行版默认就设)就会整体关掉参数转换,而 node 把 /d/x 当成「当前盘下的
# \d\x」,报出来的错误完全指不到问题。非 Windows 平台原样返回。
native_path() {
    if [ "$HOST_OS" = windows ] && command -v cygpath >/dev/null 2>&1; then
        cygpath -w "$1"
    else
        printf '%s\n' "$1"
    fi
}

# run_in_repo 对应 .ps1 的 Invoke-Tool:统一在仓库根执行,退出码交给调用方。
run_in_repo() {
    (cd "$REPO_ROOT" && "$@")
}

# fetch <url> / download <url> <out> —— curl 优先,离线镜像或精简系统上常只有 wget。
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$1"
    else
        die "需要 curl 或 wget 才能下载 $1"
    fi
}
download() {
    if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 3 --retry-delay 1 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --tries=3 -O "$2" "$1"
    else
        die "需要 curl 或 wget 才能下载 $1"
    fi
}

# sha256_of <file> —— 校验器按可用性降级(Windows 的 Git Bash 没有 openssl,
# macOS 没有 sha256sum),都不可用时返回空串,调用方跳过校验。
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$1" | sed 's/.*= *//'
    else
        printf ''
    fi
}

# env_path <VAR> <component>… —— 把 <VAR> 指向的目录与各段拼成一个路径,变量
# 未设时不输出任何东西(空 base 会拼出相对路径,那种路径会被误当成真实路径)。
# 这里走 `env` 而不是 ${VAR}:ProgramFiles(x86) 这类名字含括号的变量用 ${...}
# 展开会直接语法错误。反斜杠统一成斜杠,MSYS 下两种分隔符都能用。
env_path() {
    _base="$(env | sed -n "s/^$1=//p" | head -1)"
    [ -n "$_base" ] || return 0
    shift
    _rel="$(printf '%s/' "$@" | sed 's|/$||')"
    _base="$(printf '%s' "$_base" | tr '\\' '/')"
    printf '%s/%s\n' "${_base%/}" "$_rel"
}

# version_ge <a> <b> —— 点分数字比较,不足位补 0。纯 shell 实现:Git Bash 的
# sort 未必有 -V,而这里只需要比 go.mod 里那一个下限。
version_ge() {
    _a="$1"
    _b="$2"
    while [ -n "$_a" ] || [ -n "$_b" ]; do
        _ah="${_a%%.*}"
        case "$_a" in *.*) _a="${_a#*.}" ;; *) _a='' ;; esac
        _bh="${_b%%.*}"
        case "$_b" in *.*) _b="${_b#*.}" ;; *) _b='' ;; esac
        _ah="$(printf '%s' "$_ah" | sed 's/[^0-9].*$//')"
        _bh="$(printf '%s' "$_bh" | sed 's/[^0-9].*$//')"
        [ -n "$_ah" ] || _ah=0
        [ -n "$_bh" ] || _bh=0
        if [ "$_ah" -gt "$_bh" ]; then
            return 0
        fi
        if [ "$_ah" -lt "$_bh" ]; then
            return 1
        fi
    done
    return 0
}

# ── 工具链解析(对应 .ps1 的 Resolve-Node / Resolve-Pnpm / Resolve-Go) ──

resolve_node() {
    command -v node 2>/dev/null || true
}

resolve_pnpm() {
    # pnpm 在 PATH 上不等于能用:corepack 的 shim 若指向一个已被清理的
    # package-manager-store,`pnpm` 会以「wrapper is missing」告败 —— 那种
    # 情况下不如直接走本地 vite,所以这里顺手验一次版本。
    _p="$(command -v pnpm 2>/dev/null || true)"
    if [ -n "$_p" ] && "$_p" -v >/dev/null 2>&1; then
        printf '%s\n' "$_p"
        return 0
    fi
    if [ -n "$_p" ]; then
        warn "pnpm 存在但不可用($_p -v 失败),忽略"
    fi
    if command -v corepack >/dev/null 2>&1; then
        warn "pnpm 不可用,尝试 corepack 启用"
        corepack enable pnpm >/dev/null 2>&1 || true
        _p="$(command -v pnpm 2>/dev/null || true)"
        if [ -n "$_p" ] && "$_p" -v >/dev/null 2>&1; then
            printf '%s\n' "$_p"
            return 0
        fi
    fi
    return 0
}

resolve_go() {
    if command -v go >/dev/null 2>&1; then
        command -v go
        return 0
    fi
    for p in \
        "$HOME/.local/go/bin/go" \
        "/usr/local/go/bin/go" \
        "/usr/lib/go/bin/go" \
        "/opt/go/bin/go" \
        "$HOME/go/bin/go" \
        "$HOME/scoop/apps/go/current/bin/go" \
        "$(env_path ProgramFiles Go bin go.exe)" \
        "$(env_path 'ProgramFiles(x86)' Go bin go.exe)" \
        "$(env_path LOCALAPPDATA Programs Go bin go.exe)" \
        "C:/Go/bin/go.exe"; do
        if [ -n "$p" ] && [ -f "$p" ]; then
            printf '%s\n' "$p"
            return 0
        fi
    done
    return 0
}

# assert_go_version <go> —— Go 版本必须 >= go.mod 声明的下限(硬性要求)。
assert_go_version() {
    _ver_out="$("$1" version 2>/dev/null || true)"
    _cur="$(printf '%s' "$_ver_out" | sed -n 's/^go version go\([0-9][^ ]*\).*/\1/p')"
    [ -n "$_cur" ] || die "无法解析 Go 版本:$1"
    _cur_num="$(printf '%s' "$_cur" | sed 's/[^0-9.].*$//')"
    _req="$(sed -n 's/^go[[:space:]]\{1,\}\([0-9][0-9.]*\).*/\1/p' "$REPO_ROOT/go.mod" | head -1)"
    [ -n "$_req" ] || _req=0
    if ! version_ge "$_cur_num" "$_req"; then
        die "Go 版本过低: 当前 $_cur,go.mod 要求 >= $_req;请升级 Go 后重跑"
    fi
    printf '%s\n' "$_cur (>= $_req)"
}

go_arch() {
    case "$(uname -m 2>/dev/null || echo unknown)" in
        x86_64|amd64) printf 'amd64\n' ;;
        aarch64|arm64) printf 'arm64\n' ;;
        i686|i386|x86) printf '386\n' ;;
        *) return 1 ;;
    esac
}

go_os() {
    case "$UNAME_S" in
        MINGW*|MSYS*|CYGWIN*) printf 'windows\n' ;;
        Darwin) printf 'darwin\n' ;;
        Linux) printf 'linux\n' ;;
        *) return 1 ;;
    esac
}

# extract_go_archive <archive> <dest> —— Windows 的官方包是 zip,Git Bash
# 默认不带 unzip,退回 Windows 自己的 Expand-Archive。
extract_go_archive() {
    case "$1" in
        *.zip)
            if command -v unzip >/dev/null 2>&1; then
                unzip -q "$1" -d "$2"
            elif command -v powershell.exe >/dev/null 2>&1 && command -v cygpath >/dev/null 2>&1; then
                powershell.exe -NoProfile -Command \
                    "Expand-Archive -LiteralPath '$(cygpath -w "$1")' -DestinationPath '$(cygpath -w "$2")' -Force" >/dev/null
            else
                return 1
            fi
            ;;
        *) tar -xzf "$1" -C "$2" ;;
    esac
}

# install_go 下载官方工具链到 GOSSH_GO_ROOT(默认 ~/.local/go,用户目录,
# 不需要 sudo),并把它加进本次执行的 PATH。对应 .ps1 的 Install-Go。
install_go() {
    _arch="$(go_arch)" || die "不支持的架构: $(uname -m 2>/dev/null)"
    _os="$(go_os)" || die "不支持的系统: $UNAME_S"

    _ver="$GO_VERSION"
    if [ -z "$_ver" ]; then
        warn "未在任何常规位置找到 Go,查询官方最新稳定版 …"
        _json="$(fetch 'https://go.dev/dl/?mode=json' || true)"
        # 官方 JSON 数组的首项即最新稳定版(元素形如
        # {"version":"go1.27.1","stable":true,"files":[…]})。
        _ver="$(printf '%s' "$_json" | sed -n 's/^\[{"version":"\(go[0-9][^"]*\)".*/\1/p')"
        [ -n "$_ver" ] || die "无法从 go.dev 解析 Go 版本,可用 --go-version 手动指定"
    fi

    case "$_os" in
        windows) _ext=zip ;;
        *) _ext=tar.gz ;;
    esac
    _name="$_ver.$_os-$_arch.$_ext"
    _url="https://go.dev/dl/$_name"
    _tmp="${TMPDIR:-/tmp}/$_name"

    warn "下载 $_name(约 80 MB)…"
    download "$_url" "$_tmp" || die "下载失败: $_url"
    _size="$(wc -c <"$_tmp" | tr -d ' ')"
    [ "$_size" -gt 10485760 ] || die "Go 压缩包不完整: $_url($_size 字节)"

    _go_root="${GOSSH_GO_ROOT:-$HOME/.local/go}"
    _staging="$(mktemp -d "${TMPDIR:-/tmp}/gossh-go.XXXXXX")"
    trap 'rm -rf "$_staging"' EXIT INT TERM
    warn "解压到 $_go_root …"
    extract_go_archive "$_tmp" "$_staging" ||
        die "解压失败: 需要 unzip 或 powershell(Windows)/ tar"
    rm -f "$_tmp"

    _go_bin="$_staging/go/bin/go"
    [ -f "$_go_bin" ] || _go_bin="$_staging/go/bin/go.exe"
    [ -f "$_go_bin" ] || die "Go 压缩包结构异常: 缺少 go/bin/go"

    if [ -d "$_go_root" ]; then
        # 不删用户的目录:旧的那份挪到旁边,由用户自己决定何时清理。
        _bak="$_go_root.bak.$$"
        warn "$_go_root 已存在,挪到 $_bak"
        mv "$_go_root" "$_bak"
    fi
    mkdir -p "$(dirname "$_go_root")"
    mv "$_staging/go" "$_go_root"
    rm -rf "$_staging"

    PATH="$_go_root/bin:$PATH"
    export PATH
    ok "Go 已安装:$_go_root"
    printf '%s\n' "$_go_root/bin/go"
}

printf '%s\n' "gossh 本地构建 + 安装"
printf '%s\n' "仓库: $REPO_ROOT"

# ── 1. 工具链 ──
step "检查工具链"
NODE="$(resolve_node)"
[ -n "$NODE" ] || die "未找到 node(前端构建需要 Node 18+),请先安装 Node.js"
PNPM="$(resolve_pnpm)"
GO_EXE="$(resolve_go)"
if [ -z "$GO_EXE" ]; then
    GO_EXE="$(install_go)"
fi
[ -n "$GO_EXE" ] || die "Go 安装完成但未能定位 go 可执行文件"
GO_VERSION_TEXT="$(assert_go_version "$GO_EXE")"
GOEXE="$("$GO_EXE" env GOEXE 2>/dev/null || printf '')"
BUILD_BIN="$BUILD_DIR/gossh$GOEXE"
ok "node   $("$NODE" -v)"
if [ -n "$PNPM" ]; then
    ok "pnpm   $("$PNPM" -v)"
else
    warn "pnpm   未找到(将只用 apps/web/node_modules 里的 vite 构建)"
fi
ok "go     $GO_VERSION_TEXT  ($GO_EXE)"

# ── 2. 前端构建 ──
step "构建前端(apps/web)"
if [ ! -d "$WEB_DIR/node_modules" ]; then
    warn "缺少前端依赖,执行 pnpm install …"
    [ -n "$PNPM" ] || die "缺少 apps/web/node_modules 且未找到 pnpm,请先执行: npm i -g pnpm"
    run_in_repo "$PNPM" install || die "pnpm install 失败"
fi

# pnpm 11 在非 TTY 环境里若判定 node_modules 需要重建会直接中止
# (ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY),CI=1 让它自动重建;
# 若 pnpm 这条路走不通,退回直接调用本地 vite(构建只依赖 apps/web/node_modules)。
CI=1
export CI
DIST_MAIN="$DIST_DIR/main.js"
rm -f "$DIST_MAIN"

PNPM_OK=false
if [ -n "$PNPM" ]; then
    # 注意:--config.* 必须放在 pnpm 自己的参数位,不能跟在 build 之后
    # (否则会被透传给 vite build,变成它的位置参数而报错)
    if run_in_repo "$PNPM" --config.confirmModulesPurge=false --filter gotty-frontend build; then
        if [ -f "$DIST_MAIN" ]; then
            PNPM_OK=true
        fi
    else
        warn "pnpm 构建未成功,继续尝试本地 vite"
    fi
fi

if [ "$PNPM_OK" != true ]; then
    warn "改用本地 vite 直接构建(apps/web/node_modules)…"
    VITE_JS="$WEB_DIR/node_modules/vite/bin/vite.js"
    [ -f "$VITE_JS" ] || die "未找到 $VITE_JS,请先在仓库根执行 pnpm install"
    (cd "$WEB_DIR" && "$NODE" "$(native_path "$VITE_JS")" build) || die "vite 构建失败"
fi

if [ ! -f "$DIST_MAIN" ]; then
    die "前端产物缺失: $DIST_MAIN"
fi
ok "dist/main.js  $(wc -c <"$DIST_MAIN" | tr -d ' ') 字节"

# ── 3. 同步嵌入产物 ──
step "同步前端产物到 internal/api/static(go:embed 输入)"
mkdir -p "$STATIC_DIR"
for f in index.html main.js favicon.png; do
    [ -f "$DIST_DIR/$f" ] || die "前端产物缺失: $DIST_DIR/$f"
    cp "$DIST_DIR/$f" "$STATIC_DIR/$f"
done
EMBED_HASH="$(sha256_of "$STATIC_DIR/main.js")"
ok "index.html / main.js / favicon.png 已同步(sha256 $(printf '%.12s' "$EMBED_HASH")…)"

# ── 4. 编译 Go 二进制 ──
step "编译 gossh(版本号取自 git)"
GIT_VERSION="$(git describe --tags --always --dirty 2>/dev/null || true)"
GIT_COMMIT="$(git rev-parse HEAD 2>/dev/null | cut -c1-7 || true)"
[ -n "$GIT_VERSION" ] || GIT_VERSION=dev
[ -n "$GIT_COMMIT" ] || GIT_COMMIT=unknown
LDFLAGS="-s -w -X github.com/gausszhou/gossh/cmd.Version=$GIT_VERSION -X github.com/gausszhou/gossh/cmd.CommitID=$GIT_COMMIT"
ok "版本 $GIT_VERSION(commit $GIT_COMMIT)"
mkdir -p "$BUILD_DIR"
(cd "$REPO_ROOT" && CGO_ENABLED=0 "$GO_EXE" build -trimpath -ldflags "$LDFLAGS" -o "$BUILD_BIN" .) ||
    die "编译失败"
[ -f "$BUILD_BIN" ] || die "编译失败: 未生成 $BUILD_BIN"
ok "build/gossh$GOEXE  $(wc -c <"$BUILD_BIN" | tr -d ' ') 字节"

if [ "$NO_INSTALL" = true ]; then
    printf '\n%s已按要求跳过安装。二进制: %s%s\n' "$GREEN" "$BUILD_BIN" "$RESET"
    exit 0
fi

# ── 5. 结束正在运行的 gossh ──
step "结束正在运行的 gossh"
INSTALL_BIN="$BIN_DIR/gossh$GOEXE"
if [ "$NO_TRAY" = true ]; then
    warn "已指定 --no-tray,跳过;若覆盖安装失败请手动退出托盘里的 gossh"
elif [ "$HOST_OS" = windows ]; then
    # Git Bash 没有 pkill;借 Windows 自己的 taskkill(`//` 抑制 MSYS 把
    # /F 当路径改写)。没有进程时 taskkill 返回非 0,忽略即可。
    if taskkill //F //IM "gossh$GOEXE" >/dev/null 2>&1; then
        ok "已结束正在运行的 gossh"
    else
        ok "没有正在运行的实例"
    fi
elif command -v pkill >/dev/null 2>&1; then
    if pkill -x gossh 2>/dev/null; then
        sleep 0.8
        pkill -9 -x gossh 2>/dev/null || true
        ok "已结束正在运行的 gossh"
    else
        ok "没有正在运行的实例"
    fi
else
    warn "未找到 pkill;若覆盖安装失败请手动结束正在运行的 gossh"
fi

# ── 6. 安装 ──
step "安装到 $INSTALL_BIN"
mkdir -p "$BIN_DIR"
if ! cp "$BUILD_BIN" "$INSTALL_BIN" 2>/dev/null; then
    die "覆盖安装失败(文件可能仍被占用):$INSTALL_BIN
    请退出托盘里的 gossh 后重跑本脚本。"
fi
ok "已安装  $(wc -c <"$INSTALL_BIN" | tr -d ' ') 字节"

# PATH 注册(幂等;与 scripts/install.sh 一致,只写 shell 的 rc 文件)
BASHRC="$HOME/.bashrc"
PATH_LINE="export PATH=\"$BIN_DIR:\$PATH\""
case ":$PATH:" in
    *":$BIN_DIR:"*) ok "$BIN_DIR 已在 PATH 中" ;;
    *)
        if grep -qsF -- "$PATH_LINE" "$BASHRC" 2>/dev/null; then
            ok "$BIN_DIR 已注册到 $BASHRC"
        elif printf '\n# gossh: 安装目录加入 PATH\n%s\n' "$PATH_LINE" >>"$BASHRC" 2>/dev/null; then
            warn "已把 $BIN_DIR 写入 $BASHRC(新开的终端生效)"
        else
            warn "无法写入 $BASHRC,请手动添加:$PATH_LINE"
        fi
        ;;
esac

# login shell 也要读到 ~/.bashrc:Git Bash 等以 login shell 启动,若用户已有
# ~/.profile 而没有 ~/.bash_profile,登录时只读 ~/.profile(与 install.sh 同)。
PROFILE="$HOME/.profile"
if [ -f "$PROFILE" ] && ! grep -qs 'bashrc' "$PROFILE"; then
    if printf '\n# gossh: login shell 加载 ~/.bashrc\n. "$HOME/.bashrc"\n' >>"$PROFILE" 2>/dev/null; then
        warn "已在 $PROFILE 补一行 . ~/.bashrc"
    fi
fi

# ── 7. 校验 ──
step "校验安装结果"
VER_OUT="$("$INSTALL_BIN" version 2>&1 || true)"
case "$VER_OUT" in
    *"$GIT_VERSION"*) ok "版本自检:$VER_OUT" ;;
    *) warn "版本自检异常:$VER_OUT" ;;
esac

# 装上去的是不是本次构建的那一个:直接比字节摘要(比 .ps1 的产物片段搜索更硬)
BUILD_HASH="$(sha256_of "$BUILD_BIN")"
INSTALL_HASH="$(sha256_of "$INSTALL_BIN")"
if [ -n "$BUILD_HASH" ] && [ "$BUILD_HASH" = "$INSTALL_HASH" ]; then
    ok "已安装的二进制与本次构建一致(sha256 $(printf '%.12s' "$INSTALL_HASH")…)"
else
    warn "无法确认已安装二进制与本次构建一致,请以实际界面为准"
fi

printf '\n%s完成。启动方式:%s\n' "$GREEN" "$RESET"
printf '%s\n' "    gossh app                 # 桌面形态:托盘常驻 + 自动打开浏览器"
printf '%s\n' "    gossh serve -p 8040       # 仅服务端(浏览器访问打印出的带 token URL)"
printf '\n'
