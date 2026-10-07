# Wrapped so a download cut off part way does not parse, and the preferences below stay out of the caller's session.
& {
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 downloads very slowly while drawing a progress bar, and older .NET defaults predate TLS 1.2.
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# PROCESSOR_ARCHITECTURE says AMD64 or x86 to an emulated PowerShell on Arm64; the registry gives the machine's own.
$native = try { (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE } catch { $null }
if (-not $native) { $native = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE } }
$arch = if ($native -eq 'ARM64') { 'arm64' } else { 'amd64' }

$repo = 'Benedict-Carling/lbf-go-spike'
$asset = "lbf-windows-$arch.exe"
$base = "https://github.com/$repo/releases/latest/download"
$dir = Join-Path $env:LOCALAPPDATA 'lbf'
$exe = Join-Path $dir 'lbf.exe'
$new = "$exe.new"
$old = "$exe.old"

New-Item -ItemType Directory -Force $dir | Out-Null
Write-Host "Downloading lbf for Windows $arch..."
Invoke-WebRequest "$base/$asset" -OutFile $new -UseBasicParsing
Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$exe.sums" -UseBasicParsing
$want = Get-Content "$exe.sums" | ForEach-Object { $f = $_ -split '\s+'; if ($f[1] -eq $asset -or $f[1] -eq "*$asset") { $f[0] } } | Select-Object -First 1
Remove-Item "$exe.sums"
$got = (Get-FileHash $new -Algorithm SHA256).Hash
if (-not $want -or $got -ne $want) {
    Remove-Item $new
    throw "the download does not match its published checksum; nothing was installed. Try again."
}

# Windows will not overwrite a running exe, but it will rename one.
try { [IO.File]::Delete($old) } catch {}
if ([IO.File]::Exists($exe)) {
    try {
        [IO.File]::Move($exe, $old)
    } catch {
        Remove-Item $new
        throw "could not replace $exe while an older lbf is still running; close every lbf window and run this again."
    }
}
try {
    [IO.File]::Move($new, $exe)
} catch {
    Remove-Item $new -ErrorAction SilentlyContinue
    if ([IO.File]::Exists($old)) { [IO.File]::Move($old, $exe) }
    throw
}
try { [IO.File]::Delete($old) } catch {}
Unblock-File $exe

function Test-OnPath($list) {
    foreach ($p in $list -split ';') {
        if ($p -and [Environment]::ExpandEnvironmentVariables($p).TrimEnd('\') -eq $dir.TrimEnd('\')) { return $true }
    }
    return $false
}

# Read and written raw, so %VAR% entries stay unexpanded and the value stays REG_EXPAND_SZ.
$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
try {
    $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if (-not (Test-OnPath $userPath)) {
        $userPath = $userPath.TrimEnd(';')
        $userPath = if ($userPath) { "$userPath;$dir" } else { $dir }
        $key.SetValue('Path', $userPath, [Microsoft.Win32.RegistryValueKind]::ExpandString)
        # Deleting a variable through .NET broadcasts WM_SETTINGCHANGE, so Explorer gives new terminals the new Path.
        [Environment]::SetEnvironmentVariable('LBF_INSTALL_REFRESH', [NullString]::Value, 'User')
    }
} finally {
    $key.Close()
}
if (-not (Test-OnPath $env:Path)) {
    $env:Path = "$($env:Path.TrimEnd(';'));$dir"
}

& $exe version
Write-Host "Installed to $exe"
Write-Host "Next: lbf login"
}
