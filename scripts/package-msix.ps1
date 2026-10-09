param(
    [Parameter(Mandatory = $true)][string]$AppExe,
    [string]$Version,
    [string]$OutputDir = "dist",
    [switch]$TestSign
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$publisher = "CN=00B230FF-06D4-460F-89D0-2F55A6D78696"
$storeRoot = Join-Path $repoRoot "build/windows/store"

function Fail([string]$Message) { throw "package-msix: $Message" }

function Get-StoreVersion {
    if (-not $Version) {
        $config = Get-Content -LiteralPath (Join-Path $repoRoot "build/config.yml") -Raw
        $match = [regex]::Match($config, '(?m)^\s{2}version:\s*["'']?([0-9]+\.[0-9]+\.[0-9]+)["'']?\s*$')
        if (-not $match.Success) { Fail "cannot read info.version from build/config.yml" }
        $script:Version = $match.Groups[1].Value
    }
    if ($Version -notmatch '^[0-9]+\.[0-9]+(?:\.[0-9]+)?$') { Fail "version must be numeric major.minor[.patch] without a prerelease suffix" }
    $parts = @($Version.Split('.') | ForEach-Object { [int]::Parse($_) })
    if ($parts[0] -eq 0 -or @($parts | Where-Object { $_ -gt 65535 }).Count -gt 0) { Fail "version components must be between 0 and 65535, with nonzero major" }
    if ($parts.Count -eq 2) { return "$($parts[0]).$($parts[1]).0.0" }
    return "$($parts[0]).$($parts[1]).$($parts[2]).0"
}

function Get-SdkTool([string]$Name) {
    $found = Get-Command $Name -ErrorAction SilentlyContinue
    if ($found) { return $found.Source }
    $roots = @(${env:ProgramFiles(x86)}, $env:ProgramFiles) | Where-Object { $_ }
    $candidates = foreach ($root in $roots) {
        $bin = Join-Path $root "Windows Kits/10/bin"
        if (Test-Path -LiteralPath $bin -PathType Container) {
            Get-ChildItem -LiteralPath $bin -Directory | Sort-Object Name -Descending | ForEach-Object {
                Join-Path $_.FullName "x64/$Name"
            }
        }
    }
    foreach ($path in $candidates) { if (Test-Path -LiteralPath $path -PathType Leaf) { return $path } }
    Fail "$Name not found; install the Windows 10/11 SDK"
}

function Assert-NoReparsePoints([string]$Path) {
    $pending = [System.Collections.Generic.Stack[string]]::new()
    $pending.Push($Path)
    while ($pending.Count -gt 0) {
        $current = $pending.Pop()
        $item = Get-Item -LiteralPath $current -Force
        if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) { Fail "reparse point is not allowed: $current" }
        if ($item.PSIsContainer) {
            foreach ($child in Get-ChildItem -LiteralPath $current -Force) { $pending.Push($child.FullName) }
        }
    }
}

function Assert-X64PE([string]$Path) {
    $stream = [System.IO.File]::OpenRead($Path)
    $reader = [System.IO.BinaryReader]::new($stream)
    try {
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5A4D) { Fail "invalid PE executable: $Path" }
        $stream.Position = 0x3C
        $offset = $reader.ReadInt32()
        if ($offset -lt 64 -or $offset -gt $stream.Length - 6) { Fail "invalid PE header: $Path" }
        $stream.Position = $offset
        if ($reader.ReadUInt32() -ne 0x00004550 -or $reader.ReadUInt16() -ne 0x8664) { Fail "x64 PE required: $Path" }
    }
    finally { $reader.Dispose(); $stream.Dispose() }
}

function Assert-Media([string]$MediaDir) {
    if (-not (Test-Path -LiteralPath $MediaDir -PathType Container)) { Fail "media directory missing" }
    Assert-NoReparsePoints $MediaDir
    $files = @(Get-ChildItem -LiteralPath $MediaDir -File -Force)
    if (@(Get-ChildItem -LiteralPath $MediaDir -Directory -Force).Count -ne 0) { Fail "media directory must be flat" }
    foreach ($file in $files) {
        if ($file.Name -notmatch '^(?:mpv\.exe|[A-Za-z0-9_.-]+\.dll|SOURCE\.txt|THIRD_PARTY_NOTICES\.txt|media-runtime\.manifest|media-runtime\.sha256)$') {
            Fail "unexpected media file: $($file.Name)"
        }
    }
    foreach ($name in @("mpv.exe", "SOURCE.txt", "THIRD_PARTY_NOTICES.txt", "media-runtime.manifest", "media-runtime.sha256")) {
        $path = Join-Path $MediaDir $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf) -or (Get-Item -LiteralPath $path).Length -eq 0) { Fail "required media file missing or empty: $name" }
    }
    Assert-X64PE (Join-Path $MediaDir "mpv.exe")
    foreach ($dll in $files | Where-Object { $_.Extension -eq ".dll" }) { Assert-X64PE $dll.FullName }

    $metadata = @{}
    foreach ($line in Get-Content -LiteralPath (Join-Path $MediaDir "media-runtime.manifest")) {
        if ($line -notmatch '^([a-z_]+)=(.*)$' -or $metadata.ContainsKey($Matches[1])) { Fail "malformed or duplicate media manifest entry" }
        $metadata[$Matches[1]] = $Matches[2]
    }
    foreach ($entry in @(@("schema", "1"), @("platform", "windows"), @("architecture", "amd64"), @("qualification", "headless-lavfi-testsrc-64x64-2frames"))) {
        if ($metadata[$entry[0]] -ne $entry[1]) { Fail "media manifest $($entry[0]) is invalid" }
    }
    if ($metadata.mpv_version -notmatch '^\S+$' -or $metadata.ffmpeg_version -notmatch '^\S+$' -or $metadata.package_source -notmatch '^\S.*$') { Fail "media runtime provenance incomplete" }
    if ($metadata.source_archive_sha256 -notmatch '^[0-9a-f]{64}$') { Fail "media source archive SHA-256 is invalid" }
    if ($metadata.license_metadata -ne "SOURCE.txt,THIRD_PARTY_NOTICES.txt" -or $metadata.license_review_required -ne "true") { Fail "media license metadata incomplete" }
    if ($metadata.release_runtime -eq "true" -and $metadata.ci_fixture -eq "false") {
        if ($metadata.source_archive_sha256 -eq ('0' * 64)) { Fail "release media source archive SHA-256 is invalid" }
        if ($metadata.package_source -like "*not-release-runtime*") { Fail "media runtime is not release-qualified" }
    }
    elseif (-not $TestSign -or $metadata.release_runtime -ne "false" -or $metadata.ci_fixture -ne "true") {
        Fail "Store package requires a release-qualified media runtime; CI fixture requires -TestSign"
    }

    $hashNames = @{}
    foreach ($line in Get-Content -LiteralPath (Join-Path $MediaDir "media-runtime.sha256")) {
        if ($line -notmatch '^([0-9a-fA-F]{64})  ([A-Za-z0-9_.-]+)$') { Fail "malformed media SHA-256 line" }
        $name = $Matches[2]
        if ($name -eq "media-runtime.sha256" -or $hashNames.ContainsKey($name)) { Fail "duplicate media SHA-256 entry: $name" }
        $hashNames[$name] = $Matches[1].ToLowerInvariant()
    }
    if ($hashNames.Count -ne $files.Count - 1) { Fail "media SHA-256 file list does not match payload" }
    foreach ($file in $files | Where-Object { $_.Name -ne "media-runtime.sha256" }) {
        if (-not $hashNames.ContainsKey($file.Name) -or (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hashNames[$file.Name]) {
            Fail "media SHA-256 mismatch: $($file.Name)"
        }
    }
    return $metadata
}

if ($env:OS -ne "Windows_NT") { Fail "Windows host required" }
if (-not (Test-Path -LiteralPath $AppExe -PathType Leaf)) { Fail "app executable missing: $AppExe" }
$appPath = (Resolve-Path -LiteralPath $AppExe).Path
$appDir = Split-Path -Parent $appPath
if ((Split-Path -Leaf $appPath) -cne "TDrive.exe") { Fail "app executable must be named TDrive.exe" }
Assert-NoReparsePoints $appDir
if (@(Get-ChildItem -LiteralPath $appDir -File -Force | Where-Object { $_.Name -ne "TDrive.exe" }).Count -gt 0) {
    # Build directories often contain other outputs; only the executable and media are copied.
    Write-Verbose "Ignoring unrelated build outputs beside TDrive.exe"
}
Assert-X64PE $appPath
$media = Assert-Media (Join-Path $appDir "media")
$packageVersion = Get-StoreVersion
$makeappx = Get-SdkTool "makeappx.exe"
$signtool = if ($TestSign) { Get-SdkTool "signtool.exe" } else { $null }
$suffix = if ($TestSign) { "-test" } else { "" }
$stem = "TDrive-$Version-windows-x64$suffix"
$outputPath = Join-Path $OutputDir "$stem.msix"
$certPath = Join-Path $OutputDir "$stem.cer"
$instructionsPath = Join-Path $OutputDir "$stem-install.txt"
if (Test-Path -LiteralPath $outputPath) { Fail "output already exists: $outputPath" }
if ($TestSign -and ((Test-Path -LiteralPath $certPath) -or (Test-Path -LiteralPath $instructionsPath))) { Fail "test signing output already exists" }
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("tdrive-msix-" + [guid]::NewGuid().ToString("N"))
$certificate = $null
$complete = $false
try {
    $stage = Join-Path $tempRoot "stage"
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    Copy-Item -LiteralPath $appPath -Destination (Join-Path $stage "TDrive.exe")
    Copy-Item -LiteralPath (Join-Path $appDir "media") -Destination (Join-Path $stage "media") -Recurse
    Copy-Item -LiteralPath (Join-Path $storeRoot "Assets") -Destination (Join-Path $stage "Assets") -Recurse
    [xml]$manifest = Get-Content -LiteralPath (Join-Path $storeRoot "AppxManifest.xml") -Raw
    $manifest.Package.Identity.Version = $packageVersion
    $manifest.Save((Join-Path $stage "AppxManifest.xml"))
    $temporaryPackage = Join-Path $tempRoot "$stem.msix"
    & $makeappx pack /d $stage /p $temporaryPackage /o
    if ($LASTEXITCODE -ne 0) { Fail "makeappx pack failed" }
    if ($TestSign) {
        $certificate = New-SelfSignedCertificate -Subject $publisher -Type CodeSigningCert -CertStoreLocation "Cert:\CurrentUser\My" -KeyExportPolicy NonExportable -NotAfter (Get-Date).AddDays(30)
        if (-not $certificate -or $certificate.Subject -ne $publisher) { Fail "test certificate creation failed" }
        & $signtool sign /fd SHA256 /sha1 $certificate.Thumbprint /s My $temporaryPackage
        if ($LASTEXITCODE -ne 0) { Fail "signtool sign failed" }
        $signature = Get-AuthenticodeSignature -LiteralPath $temporaryPackage
        if (-not $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ne $certificate.Thumbprint) { Fail "test signature certificate mismatch" }
    }
    New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
    if ($TestSign) {
        Export-Certificate -Cert $certificate -FilePath $certPath -Type CERT | Out-Null
        @(
            "TEST ONLY. This MSIX is not submission-ready and must not be uploaded to Microsoft Store.",
            "Package: $stem.msix",
            "Public certificate: $stem.cer",
            "Expected SHA-1 certificate thumbprint: $($certificate.Thumbprint)",
            "Before import, compare (Get-PfxCertificate '.\$stem.cer').Thumbprint with the expected thumbprint above.",
            "On a test machine, as Administrator: Import-Certificate -FilePath '.\$stem.cer' -CertStoreLocation Cert:\LocalMachine\TrustedPeople",
            "Then install: Add-AppxPackage -Path '.\$stem.msix'",
            "When testing is finished, remove the package and certificate from LocalMachine\TrustedPeople using the thumbprint above.",
            "Do not import this certificate into Trusted Root Certification Authorities.",
            "Only the public certificate is exported. Its private key is deleted from CurrentUser\My after packaging."
        ) | Set-Content -LiteralPath $instructionsPath -Encoding UTF8
    }
    Move-Item -LiteralPath $temporaryPackage -Destination $outputPath
    $complete = $true
}
finally {
    try {
        if ($certificate) {
            Remove-Item -LiteralPath ("Cert:\CurrentUser\My\" + $certificate.Thumbprint) -DeleteKey -Force -ErrorAction Stop
        }
    }
    finally {
        if (-not $complete -and $TestSign) {
            Remove-Item -LiteralPath $certPath, $instructionsPath -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
Write-Host "package-msix: created $outputPath"
