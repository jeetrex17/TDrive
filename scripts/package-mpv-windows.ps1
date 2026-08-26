param(
    [Parameter(Mandatory = $true)]
    [string]$AppExe
)

$ErrorActionPreference = "Stop"
$versionFile = Join-Path $PSScriptRoot "package-mpv-version.txt"
. (Join-Path $PSScriptRoot "mpv-metadata.ps1")

function Fail($Message) {
    throw "package-mpv-windows: $Message"
}

function Get-ExpectedMpvVersion {
    if (-not (Test-Path -LiteralPath $versionFile -PathType Leaf)) {
        Fail "expected version file not found: $versionFile"
    }
    $version = (Get-Content -LiteralPath $versionFile -Raw).Trim()
    if ([string]::IsNullOrWhiteSpace($version)) {
        Fail "expected mpv version is empty"
    }
    return $version
}

function Get-PEArchitecture([string]$Path) {
    $stream = [System.IO.File]::OpenRead($Path)
    $reader = [System.IO.BinaryReader]::new($stream)
    try {
        if ($reader.ReadUInt16() -ne 0x5A4D) {
            Fail "not a PE executable: $Path"
        }
        $stream.Position = 0x3C
        $peOffset = $reader.ReadInt32()
        if ($peOffset -lt 0 -or $peOffset -gt ($stream.Length - 6)) {
            Fail "invalid PE header offset: $Path"
        }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) {
            Fail "invalid PE signature: $Path"
        }
        $machine = $reader.ReadUInt16()
        switch ($machine) {
            0x014C { return "x86" }
            0x8664 { return "amd64" }
            0xAA64 { return "arm64" }
            default { Fail ("unsupported PE machine 0x{0:X4}: {1}" -f $machine, $Path) }
        }
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }
}

function Get-MpvMetadata([string]$Path, [string]$ExpectedVersion) {
    $output = (& $Path --no-config --version 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0) {
        Fail "could not execute mpv runtime: $Path"
    }
    $metadata = Get-TDriveMpvMetadataFromOutput -Output $output
    if ($metadata.Mpv -ne $ExpectedVersion) {
        Fail "mpv version mismatch: expected $ExpectedVersion, got $($metadata.Mpv)"
    }
    return $metadata
}

function Invoke-MpvQualification([string]$Path, [string]$RuntimeDirectory) {
    $oldPath = $env:PATH
    $runtimePaths = @($RuntimeDirectory)
    if ($env:SystemRoot) {
        $runtimePaths += (Join-Path $env:SystemRoot "System32")
        $runtimePaths += $env:SystemRoot
    }
    try {
        $env:PATH = ($runtimePaths -join [System.IO.Path]::PathSeparator)
        & $Path --no-config --terminal=no --msg-level=all=warn --vo=null --ao=null --frames=2 -- "av://lavfi:testsrc=size=64x64:rate=1:duration=2" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Fail "bundled mpv failed deterministic lavfi decode qualification"
        }
    }
    finally {
        $env:PATH = $oldPath
    }
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
$expectedMpvVersion = Get-ExpectedMpvVersion
$mpvPath = Find-MpvExecutable
$mpvDir = Split-Path -Parent $mpvPath
$appArchitecture = Get-PEArchitecture $appPath
$mpvArchitecture = Get-PEArchitecture $mpvPath
if ($appArchitecture -ne $mpvArchitecture) {
    Fail "architecture mismatch: app=$appArchitecture mpv=$mpvArchitecture"
}
$metadata = Get-MpvMetadata $mpvPath $expectedMpvVersion

Write-Host "package-mpv-windows: using mpv: $mpvPath"

Remove-Item -LiteralPath $mediaDir -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $mediaDir | Out-Null

Copy-Item -LiteralPath $mpvPath -Destination (Join-Path $mediaDir "mpv.exe") -Force
Get-ChildItem -LiteralPath $mpvDir -File -Filter "*.dll" | ForEach-Object {
    $dllArchitecture = Get-PEArchitecture $_.FullName
    if ($dllArchitecture -ne $appArchitecture) {
        Fail "architecture mismatch: app=$appArchitecture $($_.Name)=$dllArchitecture"
    }
    Copy-Item -LiteralPath $_.FullName -Destination $mediaDir -Force
}

$bundledMPV = Join-Path $mediaDir "mpv.exe"
$metadata = Get-MpvMetadata $bundledMPV $expectedMpvVersion
Invoke-MpvQualification $bundledMPV $mediaDir

$packageSource = if ($env:TDRIVE_MPV_PACKAGE_SOURCE) { $env:TDRIVE_MPV_PACKAGE_SOURCE } else { "local-unverified" }
$packageSource = ($packageSource -replace "`r|`n", " ").Trim()
$noticePath = Join-Path $mediaDir "THIRD_PARTY_NOTICES.txt"
$manifestPath = Join-Path $mediaDir "media-runtime.manifest"
$checksumsPath = Join-Path $mediaDir "media-runtime.sha256"

@(
    "TDrive media runtime notice",
    "",
    "Bundled mpv version: $($metadata.Mpv)",
    "Bundled FFmpeg version: $($metadata.FFmpeg)",
    "mpv is GPL-2.0-or-later by default and can be built LGPL-2.1-or-later with GPL features disabled.",
    "FFmpeg is LGPL-2.1-or-later by default; optional components can make a build GPL.",
    "This notice does not determine the exact build license or replace its redistribution obligations.",
    "Review the pinned package source and provide corresponding sources/notices before public distribution.",
    "",
    "mpv source and license: https://github.com/mpv-player/mpv",
    "FFmpeg source and license: https://ffmpeg.org/"
) | Set-Content -LiteralPath $noticePath -Encoding utf8

@(
    "schema=1",
    "platform=windows",
    "architecture=$appArchitecture",
    "mpv_version=$($metadata.Mpv)",
    "ffmpeg_version=$($metadata.FFmpeg)",
    "package_source=$packageSource",
    "qualification=lavfi-testsrc-64x64-2frames",
    "license_review_required=true"
) | Set-Content -LiteralPath $manifestPath -Encoding utf8

Get-ChildItem -LiteralPath $mediaDir -File |
    Where-Object { $_.Name -ne "media-runtime.sha256" } |
    Sort-Object -Property Name |
    ForEach-Object {
        $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "$hash  $($_.Name)"
    } | Set-Content -LiteralPath $checksumsPath -Encoding ascii

Write-Host "package-mpv-windows: qualified mpv $($metadata.Mpv) with FFmpeg $($metadata.FFmpeg)"
Write-Host "package-mpv-windows: bundled mpv runtime into $mediaDir"
