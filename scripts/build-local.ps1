#Requires -Version 5.1
# 注意:本文件必须保存为 UTF-8 with BOM —— Windows PowerShell 5.1 在无 BOM 时
# 会按系统 ANSI 代码页(简体中文为 GBK)解析 .ps1,中文注释与文案会直接导致语法错误。
<#
.SYNOPSIS
    gossh 本地一键构建 + 安装(Windows)。

.DESCRIPTION
    零依赖地把当前工作副本编译成本机可用的 gossh:
      1. 校验环境(node / pnpm;Go 缺失时自动下载安装)
      2. 构建前端(apps/web → apps/web/dist)
      3. 同步前端产物到 internal/api/static(go:embed 的输入)
      4. 编译 Go 二进制(版本号取 git describe --tags --always --dirty)
      5. 停掉正在运行的 gossh(否则无法覆盖已安装的 exe)
      6. 安装到 ~/.local/bin/gossh.exe 并校验内嵌产物是否为最新
    脚本幂等:重复执行只会重新构建/覆盖安装,不会重复安装工具链。

.PARAMETER NoTray
    不结束正在运行的 gossh 进程(跳过步骤 5)。若目标 exe 被占用会直接失败。
.PARAMETER NoInstall
    只构建出 build/gossh.exe,不安装到 ~/.local/bin。
.PARAMETER Prefix
    安装前缀,默认 $HOME\.local(bin 目录为 <Prefix>\bin)。

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts\build-local.ps1
    powershell -ExecutionPolicy Bypass -File scripts\build-local.ps1 -NoTray
#>
param(
    [switch]$NoTray,
    [switch]$NoInstall,
    [string]$Prefix = "",
    [string]$GoVersion = ""      # 指定 Go 版本(如 go1.27.1);默认取官方最新稳定版
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"   # 加速 Invoke-WebRequest

# ── 路径 ──
$RepoRoot = Split-Path -Parent $PSScriptRoot          # scripts/.. = 仓库根
$WebDir = Join-Path $RepoRoot "apps\web"
$DistDir = Join-Path $WebDir "dist"
$StaticDir = Join-Path $RepoRoot "internal\api\static"
$BuildDir = Join-Path $RepoRoot "build"
$BuildExe = Join-Path $BuildDir "gossh.exe"

if ([string]::IsNullOrWhiteSpace($Prefix)) { $Prefix = Join-Path $HOME ".local" }
$BinDir = Join-Path $Prefix "bin"
$InstallExe = Join-Path $BinDir "gossh.exe"

$step = 0
function Step([string]$Title) {
    $script:step++
    Write-Host ""
    Write-Host ("[{0}] {1}" -f $script:step, $Title) -ForegroundColor Cyan
}
function Ok([string]$Message) { Write-Host "    ok  $Message" -ForegroundColor Green }
function Warn2([string]$Message) { Write-Host "    !   $Message" -ForegroundColor Yellow }
function Die([string]$Message) { Write-Host "    x   $Message" -ForegroundColor Red; exit 1 }

# 运行外部命令:返回退出码(非 0 时按 $Fatal 决定是终止脚本还是抛出可捕获的异常)。
# 注意:不能用 Die() 做"可恢复失败"——exit 会直接结束整个 shell,连 try/catch 都拦不住。
function Invoke-Tool {
    param(
        [Parameter(Mandatory = $true)][string]$File,
        [string[]]$Arguments = @(),
        [string]$WorkDir = $RepoRoot,
        [switch]$Fatal,
        [string]$What = ""
    )
    Push-Location $WorkDir
    try {
        # 子命令的 stderr 不并入 PowerShell 错误流:5.1 下 NativeCommandError
        # 叠加 ErrorActionPreference=Stop 会把正常的构建警告当成致命错误
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        try {
            & $File @Arguments
            $code = $LASTEXITCODE
        } finally { $ErrorActionPreference = $prevEap }
    } finally { Pop-Location }

    if ($code -ne 0) {
        $what = if ($What) { $What } else { "$File $($Arguments -join ' ')" }
        $msg = "$what 失败(exit $code)"
        if ($Fatal) { Die $msg }
        throw $msg
    }
    return 0
}

# 致命调用:失败即终止
function Invoke-Checked([string]$File, [string[]]$Arguments, [string]$WorkDir = $RepoRoot) {
    Invoke-Tool -File $File -Arguments $Arguments -WorkDir $WorkDir -Fatal | Out-Null
}

# ── 加载本机 PATH(刚装完工具链时当前会话可能还没刷新) ──
function Sync-Path {
    $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:PATH = (@($machine, $user) | Where-Object { $_ }) -join ";"
}

# ── 工具链解析 ──
function Resolve-Node {
    $cmd = Get-Command node -ErrorAction SilentlyContinue
    if (-not $cmd) { Die("未找到 node(前端构建需要 Node 18+),请先安装 Node.js") }
    return $cmd.Source
}

function Resolve-Pnpm {
    $cmd = Get-Command pnpm -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $corepack = Get-Command corepack -ErrorAction SilentlyContinue
    if ($corepack) { Ok "pnpm 缺失,尝试 corepack 启用"; & $corepack.Source enable pnpm | Out-Null; Sync-Path }
    $cmd = Get-Command pnpm -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    Die("未找到 pnpm(前端构建需要),请执行: npm i -g pnpm")
}

function Resolve-Go {
    # 1) 先看 PATH 上的 go(最权威:用户自己装的位置)
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    # 2) 常规安装位置(逐个人工拼接而不是 Join-Path,避免不存在的盘符抛错)
    $candidates = @(
        "$env:ProgramFiles\Go\bin\go.exe",
        "${env:ProgramFiles(x86)}\Go\bin\go.exe",
        "$env:LOCALAPPDATA\Programs\Go\bin\go.exe",
        "$HOME\go\bin\go.exe",
        "$HOME\scoop\apps\go\current\bin\go.exe",
        "C:\Go\bin\go.exe"
    )
    foreach ($c in $candidates) { if ($c -and (Test-Path -LiteralPath $c)) { return $c } }

    # 3) 兜底:官方安装器把 Go 装在 <盘>:\Program Files\Go,%ProgramFiles% 也
    #    未必指向真实安装盘(本机就是 D:\Program Files\Go),所以遍历本机所有盘符
    $roots = @()
    foreach ($d in (Get-PSDrive -PSProvider FileSystem -ErrorAction SilentlyContinue)) {
        if ($d.Root) { $roots += ($d.Root.TrimEnd('\') + "\Program Files") }
    }
    $roots += @($env:ProgramFiles, ${env:ProgramFiles(x86)}) | Where-Object { $_ }
    foreach ($root in ($roots | Select-Object -Unique)) {
        $p = "$root\Go\bin\go.exe"
        if (Test-Path -LiteralPath $p) { return $p }
    }

    # 4) 兜底:注册表卸载项(Windows 官方 MSI 会登记 Go Programming Language)
    foreach ($k in @("HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*",
                     "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*")) {
        $items = Get-ItemProperty $k -ErrorAction SilentlyContinue |
            Where-Object { $_.DisplayName -match '^Go($| )' -and $_.InstallLocation }
        foreach ($it in $items) {
            $p = "$($it.InstallLocation.TrimEnd('\'))\bin\go.exe"
            if (Test-Path -LiteralPath $p) { return $p }
        }
    }

    # 5) 兜底:按 PATH 里所有已注册目录再扫一遍(当前会话 PATH 未刷新时仍能命中)
    foreach ($dir in ((@($env:Path) -split ';') | Where-Object { $_ })) {
        $p = Join-Path $dir.Trim('"') "go.exe"
        if (Test-Path -LiteralPath $p) { return $p }
    }
    return $null
}

# Go 版本必须 >= go.mod 声明的下限(go.mod 的 go 指令是硬性要求)
function Assert-GoVersion([string]$GoExe) {
    $current = (& $GoExe version) -replace '^go version go', '' -replace '\s.*$', ''   # 1.26.6
    $gomod = Join-Path $RepoRoot "go.mod"
    $required = "0"
    if (Test-Path $gomod) {
        $m = Select-String -Path $gomod -Pattern '^go\s+([0-9]+\.[0-9]+(\.[0-9]+)?)' | Select-Object -First 1
        if ($m) { $required = $m.Matches[0].Groups[1].Value }
    }
    $cur = [version]($current -replace 'rc.*$|beta.*$', '')
    $req = [version]$required
    if ($cur -lt $req) { Die("Go 版本过低: 当前 $current,go.mod 要求 >= $required;请升级 Go 后重跑") }
    return "$current (>= $required)"
}

# 下载并解压官方 Go(zip,安装到 %LOCALAPPDATA%\Programs\Go,无需管理员权限)
function Install-Go {
    $arch = switch -Regex ($env:PROCESSOR_ARCHITECTURE) {
        "^AMD64$" { "amd64" }
        "^ARM64$" { "arm64" }
        "^x86$" { "386" }
        default { Die("不支持的架构: $($env:PROCESSOR_ARCHITECTURE)") }
    }

    $version = $GoVersion
    if ([string]::IsNullOrWhiteSpace($version)) {
        Warn2 "未在任何常规位置找到 Go,查询官方最新稳定版 …"
        $rel = Invoke-RestMethod "https://go.dev/dl/?mode=json" -TimeoutSec 30
        $version = ($rel | Where-Object { $_.stable } | Select-Object -First 1).version
        if (-not $version) { Die("无法从 go.dev 解析 Go 版本,可用 -GoVersion 手动指定") }
    }

    $zipName = "$version.windows-$arch.zip"
    $url = "https://go.dev/dl/$zipName"
    $tmp = Join-Path $env:TEMP $zipName
    Warn2 "下载 $zipName(约 80 MB)…"
    Invoke-WebRequest -Uri $url -OutFile $tmp -TimeoutSec 1800
    if (-not (Test-Path $tmp) -or (Get-Item $tmp).Length -lt 10MB) { Die("Go 压缩包下载不完整: $url") }

    $goRoot = Join-Path $env:LOCALAPPDATA "Programs\Go"
    Warn2 "解压到 $goRoot …"
    $staging = Join-Path $env:TEMP ("gossh-go-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $staging -Force | Out-Null
    Expand-Archive -Path $tmp -DestinationPath $staging -Force
    Remove-Item $tmp -Force -ErrorAction SilentlyContinue

    $extracted = Join-Path $staging "go"
    if (-not (Test-Path (Join-Path $extracted "bin\go.exe"))) { Die("Go 压缩包结构异常: 缺少 bin\go.exe") }
    if (Test-Path $goRoot) { Remove-Item $goRoot -Recurse -Force -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Path (Split-Path $goRoot -Parent) -Force | Out-Null
    Move-Item $extracted $goRoot
    Remove-Item $staging -Recurse -Force -ErrorAction SilentlyContinue

    # 注册用户级 PATH(幂等),并让当前会话立即可用
    $goBin = Join-Path $goRoot "bin"
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$goBin*") {
        [Environment]::SetEnvironmentVariable("Path", ($userPath.TrimEnd(';') + ";" + $goBin), "User")
    }
    $env:PATH = $env:PATH + ";" + $goBin

    $go = Resolve-Go
    if (-not $go) { Die("Go 安装完成但未能定位 go.exe") }
    Ok "Go 已安装:$goRoot"
    return $go
}

Write-Host "gossh 本地构建 + 安装" -ForegroundColor White
Write-Host "仓库: $RepoRoot"

# ── 1. 工具链 ──
Step "检查工具链"
$node = Resolve-Node
$pnpm = Resolve-Pnpm
# 先按已装位置找(含 D:\Program Files\Go 这类非 C 盘安装),再考虑自动安装
$go = Resolve-Go
if (-not $go) { $go = Install-Go }
$goVersionText = Assert-GoVersion $go
Ok "node   $(& $node -v)"
Ok "pnpm   $(& $pnpm -v)"
Ok "go     $goVersionText  ($go)"

# ── 2. 前端构建 ──
Step "构建前端(apps/web)"
if (-not (Test-Path (Join-Path $WebDir "node_modules"))) {
    Warn2 "缺少前端依赖,执行 pnpm install …"
    Invoke-Checked -File $pnpm -Arguments @("install") -WorkDir $RepoRoot
}
# ── 前端构建 ──
# pnpm 11 在非 TTY 环境里若判定 node_modules 需要重建会直接中止
# (ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY),CI=1 让它自动重建;
# 若 pnpm 这条路走不通,退回直接调用本地 vite(构建只依赖 apps/web/node_modules)。
$env:CI = "1"
$distMain = Join-Path $DistDir "main.js"
if (Test-Path $distMain) { Remove-Item $distMain -Force }

$pnpmOk = $false
try {
    # 注意:--config.* 必须放在 pnpm 自己的参数位,不能跟在 build 之后(否则会被
    # 透传给 vite build,变成它的位置参数而报错)
    Invoke-Tool -File $pnpm -WorkDir $RepoRoot -What "pnpm build" -Arguments @(
        "--config.confirmModulesPurge=false", "--filter", "gotty-frontend", "build"
    ) | Out-Null
    $pnpmOk = Test-Path $distMain
} catch {
    Warn2 "pnpm 构建未成功:$($_.Exception.Message)"
}

if (-not $pnpmOk) {
    Warn2 "改用本地 vite 直接构建(apps/web/node_modules)…"
    $viteJs = Join-Path $WebDir "node_modules\vite\bin\vite.js"
    if (-not (Test-Path $viteJs)) { Die("未找到 $viteJs,请先在仓库根执行 pnpm install") }
    Invoke-Checked -File (Resolve-Node) -Arguments @($viteJs, "build") -WorkDir $WebDir
}

if (-not (Test-Path $distMain)) { Die("前端产物缺失: $distMain") }
Ok ("dist\main.js  {0:N0} 字节" -f (Get-Item $distMain).Length)

# ── 3. 同步嵌入产物 ──
Step "同步前端产物到 internal\api\static(go:embed 输入)"
New-Item -ItemType Directory -Path $StaticDir -Force | Out-Null
foreach ($f in @("index.html", "main.js", "favicon.png")) {
    $src = Join-Path $DistDir $f
    if (-not (Test-Path $src)) { Die("前端产物缺失: $src") }
    Copy-Item $src (Join-Path $StaticDir $f) -Force
}
$embedHash = (Get-FileHash (Join-Path $StaticDir "main.js") -Algorithm SHA256).Hash
Ok "index.html / main.js / favicon.png 已同步(sha256 $($embedHash.Substring(0,12))…)"

# ── 4. 编译 Go 二进制 ──
Step "编译 gossh(版本号取自 git)"
Push-Location $RepoRoot
try {
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = "Continue"   # git 在非仓库/无 tag 时的 stderr 不应中断脚本
    try {
        $gitVersion = (& git describe --tags --always --dirty 2>$null | Select-Object -First 1)
        $gitCommit = (& git rev-parse HEAD 2>$null | Select-Object -First 1)
    } finally { $ErrorActionPreference = $prevEap }
    if ([string]::IsNullOrWhiteSpace($gitVersion)) { $gitVersion = "dev" }
    if ([string]::IsNullOrWhiteSpace($gitCommit)) { $gitCommit = "unknown" }
    elseif ($gitCommit.Length -ge 7) { $gitCommit = $gitCommit.Substring(0, 7) }
} finally { Pop-Location }

New-Item -ItemType Directory -Path $BuildDir -Force | Out-Null
$ldflags = "-s -w -X github.com/gausszhou/gossh/cmd.Version=$gitVersion -X github.com/gausszhou/gossh/cmd.CommitID=$gitCommit"
Ok "版本 $gitVersion(commit $gitCommit)"
$env:CGO_ENABLED = "0"
Invoke-Checked -File $go -Arguments @("build", "-trimpath", "-ldflags", $ldflags, "-o", $BuildExe, ".")
if (-not (Test-Path $BuildExe)) { Die("编译失败: 未生成 $BuildExe") }
Ok ("build\gossh.exe  {0:N1} MB" -f ((Get-Item $BuildExe).Length / 1MB))

if ($NoInstall) {
    Write-Host ""
    Write-Host "已按要求跳过安装。二进制: $BuildExe" -ForegroundColor Green
    exit 0
}

# ── 5. 停掉正在运行的 gossh ──
Step "结束正在运行的 gossh"
$running = @(Get-Process -Name gossh -ErrorAction SilentlyContinue)
if ($running.Count -eq 0) {
    Ok "没有正在运行的实例"
} elseif ($NoTray) {
    Warn2 "检测到 $($running.Count) 个实例,但指定了 -NoTray;若覆盖安装失败请手动退出托盘程序"
} else {
    foreach ($proc in $running) {
        try { $null = $proc.CloseMainWindow() } catch { }
    }
    Start-Sleep -Milliseconds 800
    foreach ($proc in @(Get-Process -Name gossh -ErrorAction SilentlyContinue)) {
        try { Stop-Process -Id $proc.Id -Force -ErrorAction Stop; Ok "已结束 PID $($proc.Id)" }
        catch { Warn2 "无法结束 PID $($proc.Id):$($_.Exception.Message)" }
    }
}

# ── 6. 安装 ──
Step "安装到 $InstallExe"
New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
try {
    Copy-Item $BuildExe $InstallExe -Force -ErrorAction Stop
} catch {
    Die("覆盖安装失败(文件可能仍被占用):$($_.Exception.Message)`n    请退出托盘里的 gossh 后重跑本脚本。")
}
Ok ("已安装  {0:N1} MB" -f ((Get-Item $InstallExe).Length / 1MB))

# PATH 注册(用户级;幂等)
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$BinDir*") {
    [Environment]::SetEnvironmentVariable("Path", ($userPath.TrimEnd(';') + ";" + $BinDir), "User")
    Warn2 "已把 $BinDir 写入用户 PATH(新开的终端生效)"
} else {
    Ok "$BinDir 已在 PATH 中"
}

# ── 7. 校验 ──
Step "校验安装结果"
$verOut = (& $InstallExe version 2>&1) -join ' '
if ($LASTEXITCODE -ne 0) { Warn2 "gossh version 执行异常:$verOut" } else { Ok "版本自检:$verOut" }

# 内嵌产物是否为本次构建:比对安装后的 exe 与 static/main.js 的字节片段
$probe = [System.IO.File]::ReadAllBytes((Join-Path $StaticDir "main.js"))
$headLen = [Math]::Min(4096, $probe.Length)
$head = [System.Text.Encoding]::ASCII.GetString($probe, 0, $headLen)
$exeBytes = [System.IO.File]::ReadAllBytes($InstallExe)
$needle = [System.Text.Encoding]::ASCII.GetBytes($head.Substring($headLen - 64, 64))
$found = $false
for ($i = 0; $i -le $exeBytes.Length - $needle.Length; $i++) {
    if ($exeBytes[$i] -eq $needle[0]) {
        $match = $true
        for ($j = 1; $j -lt $needle.Length; $j++) { if ($exeBytes[$i + $j] -ne $needle[$j]) { $match = $false; break } }
        if ($match) { $found = $true; break }
    }
}
if ($found) { Ok "已安装的 exe 内嵌的是最新前端产物" } else { Warn2 "未能确认内嵌产物(可能被压缩/对齐),请以实际界面为准" }

Write-Host ""
Write-Host "完成。启动方式:" -ForegroundColor Green
Write-Host "    gossh app                 # 桌面形态:托盘常驻 + 自动打开浏览器"
Write-Host "    gossh serve -p 8040       # 仅服务端(浏览器访问打印出的带 token URL)"
Write-Host ""
