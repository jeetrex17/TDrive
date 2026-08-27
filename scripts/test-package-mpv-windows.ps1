$ErrorActionPreference = "Stop"

function Fail($Message) {
    throw "test-package-mpv-windows: $Message"
}

if ($env:OS -ne "Windows_NT") {
    Write-Host "test-package-mpv-windows: skipped on non-Windows host"
    exit 0
}

$scriptPath = Join-Path $PSScriptRoot "package-mpv-windows.ps1"
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("tdrive-package-mpv-windows-test-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $temporaryRoot | Out-Null

try {
    $appExe = Join-Path $temporaryRoot "TDrive.exe"
    Set-Content -LiteralPath $appExe -Value "not a real exe" -Encoding ascii

    $runtimeRoot = Join-Path $temporaryRoot "runtime"
    New-Item -ItemType Directory -Force -Path $runtimeRoot | Out-Null
    Set-Content -LiteralPath (Join-Path $runtimeRoot "mpv.exe") -Value "not a real exe" -Encoding ascii
    Set-Content -LiteralPath (Join-Path $runtimeRoot "SOURCE.txt") -Value "source provenance" -Encoding utf8
    Set-Content -LiteralPath (Join-Path $runtimeRoot "THIRD_PARTY_NOTICES.txt") -Value "third party notices" -Encoding utf8

    $oldRuntimeDir = $env:TDRIVE_MPV_RUNTIME_DIR
    $oldArchiveSha = $env:TDRIVE_MPV_ARCHIVE_SHA256
    $oldPackageSource = $env:TDRIVE_MPV_PACKAGE_SOURCE
    $oldAllowFixture = $env:TDRIVE_MPV_ALLOW_UNPINNED_CI_FIXTURE

    $passed = 0

    function Expect-Failure([string]$Name, [string]$ExpectedMessage, [scriptblock]$Body) {
        try {
            & $Body
        }
        catch {
            if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
                Fail "${Name}: expected '$ExpectedMessage', got '$($_.Exception.Message)'"
            }
            $script:passed += 1
            return
        }
        Fail "${Name}: command unexpectedly succeeded"
    }

    $env:TDRIVE_MPV_RUNTIME_DIR = $null
    $env:TDRIVE_MPV_ARCHIVE_SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    $env:TDRIVE_MPV_PACKAGE_SOURCE = "file://fixture"
    $env:TDRIVE_MPV_ALLOW_UNPINNED_CI_FIXTURE = $null
    Expect-Failure "requires-runtime-dir" "TDRIVE_MPV_RUNTIME_DIR is required" {
        & $scriptPath $appExe
    }

    $env:TDRIVE_MPV_RUNTIME_DIR = $runtimeRoot
    $env:TDRIVE_MPV_ARCHIVE_SHA256 = $null
    $env:TDRIVE_MPV_PACKAGE_SOURCE = "file://fixture"
    Expect-Failure "requires-archive-sha" "TDRIVE_MPV_ARCHIVE_SHA256 must be a SHA-256 value" {
        & $scriptPath $appExe
    }

    $env:TDRIVE_MPV_ARCHIVE_SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    $env:TDRIVE_MPV_PACKAGE_SOURCE = $null
    Expect-Failure "requires-package-source" "TDRIVE_MPV_PACKAGE_SOURCE is required" {
        & $scriptPath $appExe
    }

    Remove-Item -LiteralPath (Join-Path $runtimeRoot "THIRD_PARTY_NOTICES.txt") -Force
    $env:TDRIVE_MPV_PACKAGE_SOURCE = "file://fixture"
    Expect-Failure "requires-notices" "runtime archive must include a non-empty THIRD_PARTY_NOTICES.txt" {
        & $scriptPath $appExe
    }

    Write-Host "package-mpv-windows contract tests passed: $passed"
}
finally {
    $env:TDRIVE_MPV_RUNTIME_DIR = $oldRuntimeDir
    $env:TDRIVE_MPV_ARCHIVE_SHA256 = $oldArchiveSha
    $env:TDRIVE_MPV_PACKAGE_SOURCE = $oldPackageSource
    $env:TDRIVE_MPV_ALLOW_UNPINNED_CI_FIXTURE = $oldAllowFixture
    Remove-Item -LiteralPath $temporaryRoot -Recurse -Force -ErrorAction SilentlyContinue
}
