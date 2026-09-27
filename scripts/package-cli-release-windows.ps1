[CmdletBinding()]
param(
    [ValidateNotNullOrEmpty()]
    [string]$Version = "dev"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$safeVersion = $Version -replace '[^0-9A-Za-z._-]', '-'
$name = "TDrive-$safeVersion-windows-amd64-cli"
$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$commit = & git -C $repositoryRoot rev-parse --verify HEAD
if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-fA-F]{40,64}$') {
    throw "Could not determine the Git commit for CLI version stamping"
}
$linkerFlags = "-X main.buildVersion=$safeVersion -X main.buildCommit=$commit"
$distDirectory = Join-Path $repositoryRoot "dist"
$workDirectory = Join-Path $distDirectory $name
$archivePath = Join-Path $distDirectory "$name.zip"
$executablePath = Join-Path $workDirectory "tdrive.exe"
$readmePath = Join-Path $workDirectory "README.txt"

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGOEnabled = $env:CGO_ENABLED

try {
    New-Item -ItemType Directory -Force -Path $distDirectory | Out-Null
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $workDirectory
    New-Item -ItemType Directory -Path $workDirectory | Out-Null

    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"

    Push-Location $repositoryRoot
    try {
        & go build -trimpath -ldflags $linkerFlags -o $executablePath ./cmd/tdrive
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

    @"
TDrive CLI for Windows (amd64)
================================

1. Extract this folder to a location owned by your Windows user.
2. Open PowerShell in the extracted folder.
3. Run: .\tdrive.exe help
4. Sign in, then start the drive mount:
     .\tdrive.exe login
     .\tdrive.exe mount --windows-drive T:
5. Follow the command's Windows mapping instructions if prompted.

Eligible personal drives support writes; shared drives are read-only. An
encrypted drive asks for its existing vault password if locked. Keep the
localhost WebDAV URL private: it contains a per-process capability token.
Stop the background process with:
  .\tdrive.exe daemon stop

No driver, installer, registry change, or system service is included.
"@ | Set-Content -Encoding utf8 $readmePath

    Remove-Item -Force -ErrorAction SilentlyContinue $archivePath
    Compress-Archive -Path $workDirectory -DestinationPath $archivePath -CompressionLevel Optimal
    Write-Output "wrote: dist/$name.zip"
}
finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $workDirectory

    if ($null -eq $previousGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGOOS }
    if ($null -eq $previousGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGOARCH }
    if ($null -eq $previousCGOEnabled) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $previousCGOEnabled }
}
