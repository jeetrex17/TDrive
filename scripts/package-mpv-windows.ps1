param(
    [Parameter(Mandatory = $true)]
    [string]$AppExe
)

$ErrorActionPreference = "Stop"

function Fail($Message) {
    throw "package-mpv-windows: $Message"
}

function Find-MpvExecutable {
    if ($env:TDRIVE_MPV_BIN) {
        if (Test-Path -LiteralPath $env:TDRIVE_MPV_BIN -PathType Leaf) {
            return (Resolve-Path -LiteralPath $env:TDRIVE_MPV_BIN).Path
        }
        Fail "TDRIVE_MPV_BIN does not exist: $env:TDRIVE_MPV_BIN"
    }

    if ($env:TDRIVE_MPV_DIR) {
        $candidate = Join-Path $env:TDRIVE_MPV_DIR "mpv.exe"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
        Fail "TDRIVE_MPV_DIR does not contain mpv.exe: $env:TDRIVE_MPV_DIR"
    }

    $chocoRoot = if ($env:ChocolateyInstall) { $env:ChocolateyInstall } else { "C:\ProgramData\chocolatey" }
    $known = @(
        (Join-Path $chocoRoot "lib\mpvio.install\tools\mpv.exe"),
        (Join-Path $chocoRoot "lib\mpv.install\tools\mpv.exe"),
        (Join-Path $chocoRoot "lib\mpv\tools\mpv.exe")
    )
    foreach ($candidate in $known) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }

    $command = Get-Command "mpv.exe" -ErrorAction SilentlyContinue
    if ($command -and (Test-Path -LiteralPath $command.Source -PathType Leaf)) {
        return (Resolve-Path -LiteralPath $command.Source).Path
    }

    Fail "could not find mpv.exe; install mpv or set TDRIVE_MPV_BIN"
}

if ($env:OS -ne "Windows_NT") {
    Fail "this script only runs on Windows"
}
if (-not (Test-Path -LiteralPath $AppExe -PathType Leaf)) {
    Fail "app executable not found: $AppExe"
}

$appPath = (Resolve-Path -LiteralPath $AppExe).Path
$appDir = Split-Path -Parent $appPath
$mediaDir = Join-Path $appDir "media"
$mpvPath = Find-MpvExecutable
$mpvDir = Split-Path -Parent $mpvPath

Write-Host "package-mpv-windows: using mpv: $mpvPath"

Remove-Item -LiteralPath $mediaDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $mediaDir | Out-Null

Copy-Item -LiteralPath $mpvPath -Destination (Join-Path $mediaDir "mpv.exe") -Force
Get-ChildItem -LiteralPath $mpvDir -File -Filter "*.dll" | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination $mediaDir -Force
}

$bundledMPV = Join-Path $mediaDir "mpv.exe"
& $bundledMPV --no-config --version | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "bundled mpv smoke test failed"
}

Write-Host "package-mpv-windows: bundled mpv runtime into $mediaDir"
