$ErrorActionPreference = "Stop"
if ($env:OS -ne "Windows_NT") { Write-Host "test-package-msix: skipped on non-Windows host"; exit 0 }

$packager = Join-Path $PSScriptRoot "package-msix.ps1"
$root = Join-Path ([System.IO.Path]::GetTempPath()) ("tdrive-msix-test-" + [guid]::NewGuid().ToString("N"))
$payload = Join-Path $root "payload"
$media = Join-Path $payload "media"
$output = Join-Path $root "output"
$passed = 0

function Assert([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw "test-package-msix: $Message" }
    $script:passed++
}

function Expect-Failure([string]$Expected, [scriptblock]$Body) {
    try { & $Body }
    catch {
        if ($_.Exception.Message -notlike "*$Expected*") { throw "test-package-msix: expected '$Expected', got '$($_.Exception.Message)'" }
        $script:passed++
        return
    }
    throw "test-package-msix: expected failure '$Expected'"
}

function Write-FakeX64PE([string]$Path) {
    $bytes = New-Object byte[] 128
    $bytes[0] = 0x4D; $bytes[1] = 0x5A
    [BitConverter]::GetBytes([int]64).CopyTo($bytes, 0x3C)
    $bytes[64] = 0x50; $bytes[65] = 0x45
    $bytes[68] = 0x64; $bytes[69] = 0x86
    [IO.File]::WriteAllBytes($Path, $bytes)
}

function Write-Media([bool]$Release) {
    $isRelease = $Release.ToString().ToLowerInvariant()
    $isFixture = (-not $Release).ToString().ToLowerInvariant()
    $sha = if ($Release) { 'a' * 64 } else { '0' * 64 }
    $source = if ($Release) { 'https://example.invalid/pinned-runtime.zip' } else { 'CI-only-not-release-runtime' }
    @(
        "schema=1", "platform=windows", "architecture=amd64", "mpv_version=0.40.0",
        "ffmpeg_version=7.1", "package_source=$source", "source_archive_sha256=$sha",
        "release_runtime=$isRelease", "ci_fixture=$isFixture",
        "qualification=headless-lavfi-testsrc-64x64-2frames",
        "license_metadata=SOURCE.txt,THIRD_PARTY_NOTICES.txt", "license_review_required=true"
    ) | Set-Content -LiteralPath (Join-Path $media "media-runtime.manifest") -Encoding utf8
    $lines = Get-ChildItem -LiteralPath $media -File | Where-Object { $_.Name -ne 'media-runtime.sha256' } | Sort-Object Name | ForEach-Object {
        "{0}  {1}" -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $_.Name
    }
    $lines | Set-Content -LiteralPath (Join-Path $media "media-runtime.sha256") -Encoding ascii
}

function Find-MakeAppx {
    $command = Get-Command makeappx.exe -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($base in @(${env:ProgramFiles(x86)}, $env:ProgramFiles) | Where-Object { $_ }) {
        $bin = Join-Path $base "Windows Kits/10/bin"
        if (Test-Path -LiteralPath $bin) {
            foreach ($version in Get-ChildItem -LiteralPath $bin -Directory | Sort-Object Name -Descending) {
                $path = Join-Path $version.FullName "x64/makeappx.exe"
                if (Test-Path -LiteralPath $path) { return $path }
            }
        }
    }
    throw "test-package-msix: Windows SDK makeappx.exe missing"
}

New-Item -ItemType Directory -Path $media -Force | Out-Null
try {
    $app = Join-Path $payload "TDrive.exe"
    Write-FakeX64PE $app
    Write-FakeX64PE (Join-Path $media "mpv.exe")
    "source provenance" | Set-Content -LiteralPath (Join-Path $media "SOURCE.txt")
    "third party notices" | Set-Content -LiteralPath (Join-Path $media "THIRD_PARTY_NOTICES.txt")
    Write-Media $true

    Expect-Failure "version must be numeric" { & $packager -AppExe $app -Version "1.2.3-beta" -OutputDir $output }
    Expect-Failure "version components" { & $packager -AppExe $app -Version "1.65536.0" -OutputDir $output }

    "stale" | Set-Content -LiteralPath (Join-Path $media "scratch.tmp")
    Expect-Failure "unexpected media file" { & $packager -AppExe $app -Version "1.2.3" -OutputDir $output }
    Remove-Item -LiteralPath (Join-Path $media "scratch.tmp")

    "stale" | Add-Content -LiteralPath (Join-Path $media "SOURCE.txt")
    Expect-Failure "media SHA-256 mismatch" { & $packager -AppExe $app -Version "1.2.3" -OutputDir $output }
    "source provenance" | Set-Content -LiteralPath (Join-Path $media "SOURCE.txt")
    Write-Media $true

    $notices = Join-Path $media "THIRD_PARTY_NOTICES.txt"
    Remove-Item -LiteralPath $notices
    Expect-Failure "required media file missing" { & $packager -AppExe $app -Version "1.2.3" -OutputDir $output }
    "third party notices" | Set-Content -LiteralPath $notices
    Write-Media $true

    $link = Join-Path $media "linked"
    New-Item -ItemType Junction -Path $link -Target $payload | Out-Null
    Expect-Failure "reparse point" { & $packager -AppExe $app -Version "1.2.3" -OutputDir $output }
    Remove-Item -LiteralPath $link -Force

    & $packager -AppExe $app -Version "1.2.3" -OutputDir $output
    $msix = Join-Path $output "TDrive-1.2.3-windows-x64.msix"
    Assert (Test-Path -LiteralPath $msix -PathType Leaf) "unsigned package missing"
    $unpacked = Join-Path $root "unpacked"
    & (Find-MakeAppx) unpack /p $msix /d $unpacked /o | Out-Null
    Assert ($LASTEXITCODE -eq 0) "makeappx unpack failed"
    $expected = @("AppxManifest.xml", "TDrive.exe", "Assets\Square44x44Logo.png", "Assets\Square150x150Logo.png", "Assets\StoreLogo.png", "media\mpv.exe", "media\SOURCE.txt", "media\THIRD_PARTY_NOTICES.txt", "media\media-runtime.manifest", "media\media-runtime.sha256")
    foreach ($name in $expected) { Assert (Test-Path -LiteralPath (Join-Path $unpacked $name) -PathType Leaf) "missing package file $name" }
    $actual = @(Get-ChildItem -LiteralPath $unpacked -Recurse -File |
        Where-Object { $_.Name -notin @('AppxBlockMap.xml', '[Content_Types].xml', 'AppxSignature.p7x') } |
        ForEach-Object { $_.FullName.Substring($unpacked.Length + 1) })
    Assert ($actual.Count -eq $expected.Count) "unexpected package files"
    [xml]$manifest = Get-Content -LiteralPath (Join-Path $unpacked "AppxManifest.xml") -Raw
    Assert ($manifest.Package.Identity.Name -eq "Jeetrex.TDriveTelegrambasedCloudStorage") "Store identity mismatch"
    Assert ($manifest.Package.Identity.Publisher -eq "CN=00B230FF-06D4-460F-89D0-2F55A6D78696") "publisher mismatch"
    Assert ($manifest.Package.Identity.Version -eq "1.2.3.0") "version mismatch"
    Assert ($manifest.Package.Identity.ProcessorArchitecture -eq "x64") "architecture mismatch"
    Assert ($manifest.Package.Applications.Application.Executable -eq "TDrive.exe") "entry executable mismatch"
    Assert ($manifest.Package.Applications.Application.EntryPoint -eq "Windows.FullTrustApplication") "full-trust entry point mismatch"
    $ns = [System.Xml.XmlNamespaceManager]::new($manifest.NameTable)
    $ns.AddNamespace("f", "http://schemas.microsoft.com/appx/manifest/foundation/windows10")
    $ns.AddNamespace("d", "http://schemas.microsoft.com/appx/manifest/desktop/windows10/6")
    $ns.AddNamespace("v", "http://schemas.microsoft.com/appx/manifest/virtualization/windows10")
    $ns.AddNamespace("r", "http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities")
    Assert ($manifest.SelectSingleNode('/f:Package/f:Dependencies/f:TargetDeviceFamily', $ns).GetAttribute("MinVersion") -eq "10.0.19041.0") "minimum Windows version mismatch"
    Assert ($manifest.SelectSingleNode('/f:Package/f:Properties/d:FileSystemWriteVirtualization', $ns).InnerText -eq "disabled") "Windows 10 AppData virtualization mismatch"
    $excluded = @($manifest.SelectNodes('/f:Package/f:Properties/v:FileSystemWriteVirtualization/v:ExcludedDirectories/v:ExcludedDirectory', $ns) | ForEach-Object { $_.InnerText })
    $expectedExcluded = @('$(KnownFolder:RoamingAppData)\TDrive', '$(KnownFolder:LocalAppData)\TDrive', '$(KnownFolder:RoamingAppData)\TDrive.exe')
    Assert ($excluded.Count -eq $expectedExcluded.Count -and @($expectedExcluded | Where-Object { $_ -notin $excluded }).Count -eq 0) "AppData exclusions mismatch"
    Assert ($null -ne $manifest.SelectSingleNode('/f:Package/f:Capabilities/r:Capability[@Name="unvirtualizedResources"]', $ns)) "unvirtualizedResources capability missing"

    Write-Media $false
    Expect-Failure "release-qualified media runtime" { & $packager -AppExe $app -Version "1.2.4" -OutputDir $output }
    & $packager -AppExe $app -Version "1.2.4" -OutputDir $output -TestSign
    $testStem = "TDrive-1.2.4-windows-x64-test"
    $testMsix = Join-Path $output "$testStem.msix"
    $cert = Join-Path $output "$testStem.cer"
    Assert (Test-Path -LiteralPath $testMsix -PathType Leaf) "signed package missing"
    Assert (Test-Path -LiteralPath $cert -PathType Leaf) "public certificate missing"
    Assert (Test-Path -LiteralPath (Join-Path $output "$testStem-install.txt") -PathType Leaf) "test instructions missing"
    Assert (@(Get-ChildItem -LiteralPath $output -Filter '*.pfx').Count -eq 0) "private key artifact was exported"
    $signed = Get-AuthenticodeSignature -LiteralPath $testMsix
    Assert ($signed.SignerCertificate.Thumbprint -eq (Get-PfxCertificate -FilePath $cert).Thumbprint) "test signing certificate mismatch"
    Assert (-not (Test-Path -LiteralPath ("Cert:\CurrentUser\My\" + $signed.SignerCertificate.Thumbprint))) "temporary certificate left in store"
    Write-Host "test-package-msix: $passed checks passed"
}
finally { Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue }
