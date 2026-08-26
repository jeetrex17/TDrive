function Get-TDriveMpvMetadataFromOutput {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Output
    )

    $normalized = $Output -replace "`r", ""
    $mpvVersions = @([regex]::Matches($normalized, '(?m)^\s*mpv\s+v?([0-9]\S*)') |
        ForEach-Object { $_.Groups[1].Value.Trim() } |
        Sort-Object -Unique)
    if ($mpvVersions.Count -eq 0) {
        throw "could not read mpv version"
    }
    if ($mpvVersions.Count -ne 1) {
        throw "conflicting mpv versions: $($mpvVersions -join ', ')"
    }

    $ffmpegVersions = @([regex]::Matches($normalized, '(?m)^\s*FFmpeg version:\s*(.+)$') |
        ForEach-Object { $_.Groups[1].Value.Trim() } |
        Where-Object { $_ -ne "" } |
        Sort-Object -Unique)
    if ($ffmpegVersions.Count -eq 0) {
        throw "could not read FFmpeg version"
    }
    if ($ffmpegVersions.Count -ne 1) {
        throw "conflicting FFmpeg versions: $($ffmpegVersions -join ', ')"
    }

    return @{
        Mpv = $mpvVersions[0]
        FFmpeg = $ffmpegVersions[0]
    }
}
