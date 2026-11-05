param(
    [string]$Directory = (Join-Path $PSScriptRoot '../.cache/hyperv/iso')
)

$ErrorActionPreference = 'Stop'
$base = 'https://releases.ubuntu.com/24.04'
$file = 'ubuntu-24.04.5-live-server-amd64.iso'
$directoryPath = [IO.Path]::GetFullPath($Directory)
New-Item -ItemType Directory -Path $directoryPath -Force | Out-Null
$checksums = Join-Path $directoryPath 'SHA256SUMS'
Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $checksums
$entry = @(Get-Content -LiteralPath $checksums | Where-Object {
    $_ -match ('^[0-9a-fA-F]{64}\s+\*?' + [regex]::Escape($file) + '$')
})
if ($entry.Count -ne 1) { throw 'Expected ISO checksum is missing or ambiguous.' }
$expected = ($entry[0] -split '\s+')[0].ToLowerInvariant()
$iso = Join-Path $directoryPath $file
if (Test-Path -LiteralPath $iso) {
    $actual = (Get-FileHash -LiteralPath $iso -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw 'Existing ISO checksum mismatch. Inspect the file before retrying.' }
} else {
    $partial = "$iso.partial"
    if (Test-Path -LiteralPath $partial) { throw 'Partial download exists. Inspect it before retrying.' }
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile $partial
    $actual = (Get-FileHash -LiteralPath $partial -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw 'Downloaded ISO checksum mismatch. Partial file retained for inspection.' }
    Move-Item -LiteralPath $partial -Destination $iso
}
[ordered]@{
    source = "$base/$file"
    path = $iso
    bytes = (Get-Item -LiteralPath $iso).Length
    sha256 = $actual
    checksumMatched = $true
} | ConvertTo-Json
