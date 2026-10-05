# Wrapped so a download cut off part way does not parse, and the preferences below stay out of the caller's session.
& {
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 downloads very slowly while drawing a progress bar, and older .NET defaults predate TLS 1.2.
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'Benedict-Carling/lbf-go-spike'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "lbf-windows-$arch.exe"
$base = "https://github.com/$repo/releases/latest/download"
$dir = Join-Path $env:LOCALAPPDATA 'lbf'
$exe = Join-Path $dir 'lbf.exe'

New-Item -ItemType Directory -Force $dir | Out-Null
Write-Host "Downloading lbf for Windows $arch..."
Invoke-WebRequest "$base/$asset" -OutFile "$exe.new" -UseBasicParsing
Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$exe.sums" -UseBasicParsing
$want = Get-Content "$exe.sums" | ForEach-Object { $f = $_ -split '\s+'; if ($f[1] -eq $asset -or $f[1] -eq "*$asset") { $f[0] } } | Select-Object -First 1
Remove-Item "$exe.sums"
$got = (Get-FileHash "$exe.new" -Algorithm SHA256).Hash
if (-not $want -or $got -ne $want) {
    Remove-Item "$exe.new"
    throw "the download does not match its published checksum; nothing was installed. Try again."
}
Move-Item "$exe.new" $exe -Force
Unblock-File $exe

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
}
if (($env:Path -split ';') -notcontains $dir) {
    $env:Path = "$env:Path;$dir"
}

& $exe version
Write-Host "Installed to $exe"
Write-Host "Next: lbf login"
}
