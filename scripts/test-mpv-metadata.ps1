$ErrorActionPreference = "Stop"
. "$PSScriptRoot/mpv-metadata.ps1"

$passed = 0

function Assert-Parse($Name, $InputText, $ExpectedMpv, $ExpectedFFmpeg) {
    $metadata = Get-TDriveMpvMetadataFromOutput -Output $InputText
    if ($metadata.Mpv -ne $ExpectedMpv) {
        throw "FAIL ${Name}: mpv=$($metadata.Mpv) want $ExpectedMpv"
    }
    if ($metadata.FFmpeg -ne $ExpectedFFmpeg) {
        throw "FAIL ${Name}: ffmpeg=$($metadata.FFmpeg) want $ExpectedFFmpeg"
    }
    $script:passed++
}

function Assert-Reject($Name, $InputText) {
    try {
        Get-TDriveMpvMetadataFromOutput -Output $InputText | Out-Null
    }
    catch {
        $script:passed++
        return
    }
    throw "FAIL ${Name}: parser accepted invalid output"
}

Assert-Parse "plain-mpv-version" "mpv 0.41.0 Copyright`nFFmpeg version: 7.1.1`n" "0.41.0" "7.1.1"
Assert-Parse "v-prefixed-mpv-version" "mpv v0.41.0 Copyright`nFFmpeg version: 7.1.1`n" "0.41.0" "7.1.1"
Assert-Parse "crlf-and-leading-whitespace" "  mpv v0.41.0 Copyright`r`n  FFmpeg version: 7.1.1`r`n" "0.41.0" "7.1.1"
Assert-Reject "missing-mpv" "not mpv`nFFmpeg version: 7.1.1`n"
Assert-Reject "missing-ffmpeg" "mpv 0.41.0 Copyright`nlibavcodec 61.19.101`n"
Assert-Reject "malformed-mpv" "mpv next Copyright`nFFmpeg version: 7.1.1`n"
Assert-Reject "conflicting-mpv" "mpv 0.40.0 Copyright`nmpv v0.41.0 Copyright`nFFmpeg version: 7.1.1`n"
Assert-Reject "conflicting-ffmpeg" "mpv 0.41.0 Copyright`nFFmpeg version: 7.0`nFFmpeg version: 7.1.1`n"

Write-Host "mpv metadata PowerShell parser tests passed: $passed"
