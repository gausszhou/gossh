#Requires -Version 5.1
<#
.SYNOPSIS
    gossh Windows installer (PowerShell).
    Mirrors the 5 steps of scripts/install.sh:
      1. detect OS / architecture
      2. download the release archive (tar.gz)
      3. extract the binary to ~\.local\bin
      4. register the bin dir in the PowerShell profile (idempotent)
      5. prompt to reload the session (analog of "source ~/.bashrc")

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File install.ps1
    powershell -ExecutionPolicy Bypass -File install.ps1 -Version v0.0.4
    powershell -ExecutionPolicy Bypass -File install.ps1 -Prefix D:\\tools\\gossh
#>
param(
    [string]$Version = "",          # target tag, e.g. v0.0.4 (default: latest release)
    [string]$Prefix = "",           # install prefix (default: $HOME\.local, bin at <prefix>\bin)
    [string]$Repo = "gausszhou/gossh"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"    # speed up Invoke-WebRequest

# --- 1. detect OS / architecture ------------------------------------------
$os = "windows"
$procArch = [Environment]::GetEnvironmentVariable("PROCESSOR_ARCHITECTURE")
switch -Regex ($procArch) {
    "^AMD64$" { $arch = "amd64" }
    "^ARM64$" { $arch = "arm64" }
    default { throw "unsupported architecture: $procArch (releases cover amd64/arm64)" }
}
# Windows 二进制与压缩包资产均带 .exe 后缀(与 Makefile 矩阵命名一致)
$platform = "gossh-$os-$arch.exe"
$asset = "$platform.tar.gz"         # Windows 平台与其它平台一样是 tar.gz,不是 zip

if ([string]::IsNullOrWhiteSpace($Prefix)) { $Prefix = Join-Path $HOME ".local" }
$Bindir = Join-Path $Prefix "bin"
$BinPath = Join-Path $Bindir "gossh.exe"

# --- 2. resolve version and download the archive ---------------------------
$base = "https://github.com/$Repo/releases"
if ([string]::IsNullOrWhiteSpace($Version)) {
    Write-Host "resolving latest release ..."
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" `
        -Headers @{ "User-Agent" = "gossh-install" }
    $tag = $rel.tag_name
} else {
    $tag = $Version
}
Write-Host "installing gossh $tag ($os/$arch)"

$tmp = Join-Path $env:TEMP ("gossh-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $tgz = Join-Path $tmp $asset
    Write-Host "downloading $asset ..."
    Invoke-WebRequest -Uri "$base/download/$tag/$asset" -OutFile $tgz
    if (-not (Test-Path $tgz)) { throw "download failed: $asset" }

    # checksum: sha256sums.txt 可用时校验,不一致即中止(与 install.sh 同语义)
    try {
        $sums = (Invoke-WebRequest -Uri "$base/download/$tag/sha256sums.txt").Content
        $expected = $null
        foreach ($ln in ($sums -split "`r?`n")) {
            if ($ln -match '^([0-9a-fA-F]{64})\s+(.+)$' -and $Matches[2].Trim() -eq $asset) {
                $expected = $Matches[1]
                break
            }
        }
        if ($expected) {
            $actual = (Get-FileHash -Algorithm SHA256 -Path $tgz).Hash.ToLowerInvariant()
            if ($actual -ne $expected.ToLowerInvariant()) {
                throw "checksum mismatch for $asset`n  expected: $expected`n  actual:   $actual"
            }
            Write-Host "checksum verified"
        } else {
            Write-Warning "sha256sums.txt 中无 $asset 条目;跳过校验"
        }
    } catch {
        Write-Warning "sha256sums.txt 不可用;跳过校验($($_.Exception.Message))"
    }

    # --- 3. extract: tar.exe 内置于 Windows 10 1803+ / 11,支持 gzip ---------
    Write-Host "extracting $asset ..."
    tar -xzf $tgz -C $tmp
    if ($LASTEXITCODE -ne 0) { throw "tar extraction failed(exit $LASTEXITCODE);需 Windows 10 1803+ 或安装 tar" }
    $extracted = Get-ChildItem -Path $tmp -Filter $platform -File | Select-Object -First 1
    if (-not $extracted) { throw "archive contains no $platform binary" }

    New-Item -ItemType Directory -Path $Bindir -Force | Out-Null
    Copy-Item -Path $extracted.FullName -Destination $BinPath -Force
    Write-Host "installed: $BinPath"

    # --- 4. register PATH in the PowerShell profile (idempotent) ------------
    # 类比 install.sh 的 ~/.bashrc:仅当 $Bindir 未被引用过才追加
    $profileDir = Split-Path -Parent $PROFILE
    if ($profileDir) { New-Item -ItemType Directory -Path $profileDir -Force | Out-Null }
    $line = 'if (($env:Path -split ";") -notcontains "' + $Bindir + '") { $env:Path += ";' + $Bindir + '" }'
    $profileText = if (Test-Path $PROFILE) { Get-Content -Path $PROFILE -Raw } else { "" }
    if ($profileText -match [regex]::Escape($Bindir)) {
        Write-Host "$Bindir 已在 $PROFILE 中(跳过 PATH 注册)"
    } else {
        Add-Content -Path $PROFILE -Value ("`n# gossh: add install dir to PATH`n" + $line)
        Write-Host "已将 $Bindir 注册到 PowerShell 配置: $PROFILE"
    }
    # 当前会话立即生效
    if (($env:Path -split ";") -notcontains $Bindir) { $env:Path += ";" + $Bindir }

    # --- 5. prompt ----------------------------------------------------------
    & $BinPath version
    Write-Host ""
    Write-Host "installed: $BinPath ($tag)"
    Write-Host "PATH 已注册;新开终端即生效。当前会话可用: . `$PROFILE"
} finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}