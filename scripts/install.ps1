# Wrapped so a download cut off part way does not parse, and the preferences below stay out of the caller's session.
& {
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 downloads very slowly while drawing a progress bar, and older .NET defaults predate TLS 1.2.
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'ImperialCollegeLondon/lbf-cli'
$base = "https://github.com/$repo/releases/latest/download"
if ($env:LBF_TEST_INSTALL_FROM) { $base = $env:LBF_TEST_INSTALL_FROM }
$dir = Join-Path $env:LOCALAPPDATA 'lbf'
$ps1 = Join-Path $dir 'lbf.ps1'
$cmd = Join-Path $dir 'lbf.cmd'
$exe = Join-Path $dir 'lbf.exe'

# lbf is a script, so PowerShell must be allowed to run scripts. Group Policy wins over any other setting, so it is never overridden here.
$policyCheck = {
    $gp = $null
    foreach ($scope in 'MachinePolicy', 'UserPolicy') {
        $p = [string](Get-ExecutionPolicy -Scope $scope)
        if ($p -ne 'Undefined') { $gp = $p; break }
    }
    if ($gp) {
        if ($gp -eq 'AllSigned' -or $gp -eq 'Restricted') {
            throw "your organisation's Group Policy sets PowerShell's execution policy to $gp, so this computer runs only scripts signed by a publisher IT trusts, and lbf.ps1 is not signed. Nothing was installed. Ask IT to allow unsigned local scripts for your account (the RemoteSigned execution policy), or to sign lbf.ps1."
        }
        return
    }
    $local = 'Undefined'
    $from = ''
    foreach ($scope in 'CurrentUser', 'LocalMachine') {
        $p = [string](Get-ExecutionPolicy -Scope $scope)
        if ($p -ne 'Undefined') { $local = $p; $from = $scope; break }
    }
    if ($local -eq 'AllSigned') {
        throw "PowerShell's execution policy ($from) is AllSigned, so this computer runs only signed scripts, and lbf.ps1 is not signed. Nothing was installed. If you set it yourself, run 'Set-ExecutionPolicy -Scope CurrentUser RemoteSigned' and run this again; otherwise ask IT."
    }
    if ($local -eq 'Restricted' -or $local -eq 'Undefined') {
        # A Process policy, such as powershell -ExecutionPolicy Bypass, makes this report an error after it has succeeded.
        try { Set-ExecutionPolicy -Scope CurrentUser RemoteSigned -Force } catch { if ([string](Get-ExecutionPolicy -Scope CurrentUser) -ne 'RemoteSigned') { throw } }
        Write-Host "Allowed PowerShell to run local scripts such as lbf.ps1 for your account (execution policy RemoteSigned for CurrentUser)."
    }
}
& $policyCheck
# PowerShell 7 keeps its own policy; lbf typed in Windows PowerShell needs that one set too.
if ($PSVersionTable.PSEdition -eq 'Core') {
    $winps = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if (Test-Path $winps) {
        $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes("`$ErrorActionPreference = 'Stop'; & { $policyCheck }"))
        & $winps -NoProfile -NonInteractive -EncodedCommand $encoded
        if ($LASTEXITCODE) { throw "Windows PowerShell could not be set up to run lbf.ps1; see the message above." }
    }
}

New-Item -ItemType Directory -Force $dir | Out-Null
Write-Host "Downloading lbf for Windows..."
$new = "$ps1.new"
$sums = "$ps1.sums"
Invoke-WebRequest "$base/lbf.ps1" -OutFile $new -UseBasicParsing
Invoke-WebRequest "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
$want = Get-Content $sums | ForEach-Object { $f = $_ -split '\s+'; if ($f[1] -eq 'lbf.ps1' -or $f[1] -eq '*lbf.ps1') { $f[0] } } | Select-Object -First 1
Remove-Item $sums
$got = (Get-FileHash $new -Algorithm SHA256).Hash
if (-not $want -or $got -ne $want) {
    Remove-Item $new
    throw "the download does not match its published checksum; nothing was installed. Try again."
}

# Earlier versions installed lbf.exe, which cmd.exe would run before lbf.cmd; a running exe can be renamed but not deleted.
foreach ($leftover in "$exe.old", "$exe.new", "$exe.sums") { try { [IO.File]::Delete($leftover) } catch {} }
if ([IO.File]::Exists($exe)) {
    try {
        [IO.File]::Move($exe, "$exe.old")
    } catch {
        Remove-Item $new
        throw "could not remove the old $exe while it is still running; close every lbf window and run this again."
    }
    try { [IO.File]::Delete("$exe.old") } catch {}
}

# PowerShell reads a script whole before running it, so a running lbf does not stop it being replaced.
if ([IO.File]::Exists($ps1)) {
    try {
        [IO.File]::Replace($new, $ps1, [NullString]::Value)
    } catch {
        Copy-Item -LiteralPath $new -Destination $ps1 -Force
        Remove-Item -LiteralPath $new
    }
} else {
    [IO.File]::Move($new, $ps1)
}
$shim = "@echo off`r`n`"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`" -NoProfile -ExecutionPolicy RemoteSigned -File `"%~dp0lbf.ps1`" %*`r`nexit /b %ERRORLEVEL%`r`n"
[IO.File]::WriteAllText($cmd, $shim, [Text.Encoding]::ASCII)
Unblock-File $ps1
Unblock-File $cmd

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

& $ps1 version
if ($LASTEXITCODE) { throw "the installed lbf did not run; see the message above." }
$found = Get-Command lbf -ErrorAction SilentlyContinue | Select-Object -First 1
if ($found -and $found.Source -and [IO.Path]::GetDirectoryName($found.Source) -ne $dir) {
    Write-Warning "typing lbf runs $($found.Source), which comes before $dir on your PATH; remove it to use this lbf."
}
Write-Host "Installed to $ps1"
Write-Host "Next: lbf login"
}
