$ErrorActionPreference = 'Stop'

$repo = 'Benedict-Carling/lbf-go-spike'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = Join-Path $env:LOCALAPPDATA 'lbf'
$exe = Join-Path $dir 'lbf.exe'

New-Item -ItemType Directory -Force $dir | Out-Null
Write-Host "Downloading lbf for Windows $arch..."
Invoke-WebRequest "https://github.com/$repo/releases/latest/download/lbf-windows-$arch.exe" -OutFile "$exe.new" -UseBasicParsing
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
