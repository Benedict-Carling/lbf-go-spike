Add-Type -AssemblyName System.Net.Http
# .NET Framework's defaults predate TLS 1.2, which GitHub and Azure require.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$script:ReleasesUrl = 'https://github.com/Benedict-Carling/lbf-go-spike/releases'
$script:UpdateCheckEvery = [TimeSpan]::FromHours(1)
$script:UpdateCheckLimit = [TimeSpan]::FromSeconds(2)
$script:UpgradeAsset = 'lbf.ps1'
$script:LbfScriptPath = $PSCommandPath

function Get-LbfReleasesUrl {
    if ($env:LBF_TEST_RELEASES) { return $env:LBF_TEST_RELEASES }
    return $script:ReleasesUrl
}

function Test-LbfMacOS {
    if ($script:OnWindows) { return $false }
    return [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)
}

# Go's os.UserCacheDir.
function Get-LbfUserCacheDir {
    if ($script:OnWindows) {
        if (-not $env:LOCALAPPDATA) { throw (New-LbfError '%LocalAppData% is not defined') }
        return $env:LOCALAPPDATA
    }
    if (Test-LbfMacOS) {
        if (-not $env:HOME) { throw (New-LbfError '$HOME is not defined') }
        return $env:HOME + '/Library/Caches'
    }
    if ($env:XDG_CACHE_HOME) {
        if (-not $env:XDG_CACHE_HOME.StartsWith('/', [StringComparison]::Ordinal)) { throw (New-LbfError 'path in $XDG_CACHE_HOME is relative') }
        return $env:XDG_CACHE_HOME
    }
    if (-not $env:HOME) { throw (New-LbfError 'neither $XDG_CACHE_HOME nor $HOME are defined') }
    return $env:HOME + '/.cache'
}

function New-LbfHttpClient {
    param([switch]$NoRedirect)
    $handler = New-Object System.Net.Http.HttpClientHandler
    $handler.AllowAutoRedirect = -not $NoRedirect
    # .NET Framework allows two connections per host unless told otherwise.
    if ($handler.PSObject.Properties['MaxConnectionsPerServer']) { $handler.MaxConnectionsPerServer = 64 }
    $client = New-Object System.Net.Http.HttpClient -ArgumentList $handler
    $client.Timeout = [System.Threading.Timeout]::InfiniteTimeSpan
    return $client
}

function Format-LbfUpgradeWarning {
    param([string]$Current, [string]$Latest)
    $rule = '=' * 64
    return "`n$rule`n  lbf $Current is out of date: the latest release is $Latest.`n  Please upgrade now by running:  lbf upgrade`n$rule`n"
}

# A version golang.org/x/mod/semver accepts in canonical form, as major, minor, patch and prerelease; $null otherwise.
function ConvertFrom-LbfSemver {
    param([AllowEmptyString()][string]$Version)
    $m = [regex]::Match($Version, '\Av(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?\z')
    if (-not $m.Success) { return $null }
    return @{ Nums = @($m.Groups[1].Value, $m.Groups[2].Value, $m.Groups[3].Value); Pre = $m.Groups[4].Value }
}

function Compare-LbfNumber {
    param([string]$A, [string]$B)
    if ($A.Length -ne $B.Length) { return [Math]::Sign($A.Length - $B.Length) }
    return [Math]::Sign([string]::CompareOrdinal($A, $B))
}

function Compare-LbfPrerelease {
    param([string]$A, [string]$B)
    if (Test-LbfSame $A $B) { return 0 }
    if ($A.Length -eq 0) { return 1 }
    if ($B.Length -eq 0) { return -1 }
    $pa = @($A -split '\.')
    $pb = @($B -split '\.')
    for ($i = 0; $i -lt [Math]::Min($pa.Count, $pb.Count); $i++) {
        $x = $pa[$i]; $y = $pb[$i]
        if (Test-LbfSame $x $y) { continue }
        $xn = $x -cmatch '\A[0-9]+\z'
        $yn = $y -cmatch '\A[0-9]+\z'
        if ($xn -and $yn) { return Compare-LbfNumber $x $y }
        if ($xn) { return -1 }
        if ($yn) { return 1 }
        return [Math]::Sign([string]::CompareOrdinal($x, $y))
    }
    return [Math]::Sign($pa.Count - $pb.Count)
}

# Go's newerVersion: never offers a pre-release, but moves a pre-release user on to the release.
function Test-LbfNewerVersion {
    param([AllowEmptyString()][string]$Latest, [AllowEmptyString()][string]$Current)
    $l = ConvertFrom-LbfSemver $Latest
    $c = ConvertFrom-LbfSemver $Current
    if ($null -eq $l -or $null -eq $c -or $l.Pre.Length -gt 0) { return $false }
    for ($i = 0; $i -lt 3; $i++) {
        $d = Compare-LbfNumber $l.Nums[$i] $c.Nums[$i]
        if ($d -ne 0) { return $d -gt 0 }
    }
    return (Compare-LbfPrerelease $l.Pre $c.Pre) -gt 0
}

function Start-LbfLatestVersionHead {
    param([string]$Base, [System.Threading.CancellationToken]$Token)
    $client = New-LbfHttpClient -NoRedirect
    $req = New-Object System.Net.Http.HttpRequestMessage -ArgumentList ([System.Net.Http.HttpMethod]::Head), ($Base + '/latest')
    $task = $client.SendAsync($req, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $Token)
    return @{ Client = $client; Task = $task; Base = $Base }
}

# Go's latestVersion, from a finished HEAD of releases/latest; the tag is read from the redirect, avoiding the API's rate limit.
function Get-LbfLatestVersionFromHead {
    param([hashtable]$Head)
    try {
        $resp = $Head.Task.GetAwaiter().GetResult()
    } catch {
        throw (New-LbfError "checking for the latest lbf release: Head `"$($Head.Base)/latest`": $(Get-LbfErrorText $_)")
    } finally {
        $Head.Client.Dispose()
    }
    $loc = ''
    $values = $null
    if ($resp.Headers.TryGetValues('Location', [ref]$values)) { $loc = @($values)[0] }
    $code = [int]$resp.StatusCode
    $resp.Dispose()
    $i = $loc.IndexOf('/releases/tag/', [StringComparison]::Ordinal)
    $tag = ''
    if ($i -ge 0) { $tag = $loc.Substring($i + '/releases/tag/'.Length) }
    if ($i -lt 0 -or $tag.Length -eq 0) { throw (New-LbfError "no lbf release found at $($Head.Base) (HTTP $code)") }
    return $tag
}

function Get-LbfLatestVersion {
    param([string]$Base)
    $head = Start-LbfLatestVersionHead $Base ([System.Threading.CancellationToken]::None)
    return Get-LbfLatestVersionFromHead $head
}

# Go's cachedLatestVersion, split so the request runs while the command does: asks at most once per UpdateCheckEvery, failures included.
function Start-LbfCachedLatestVersion {
    param([string]$Base, [string]$Cache)
    $state = @{ Cache = $Cache; Cached = [byte[]]@(); Head = $null; Cts = $null; Deadline = [DateTime]::UtcNow + $script:UpdateCheckLimit }
    try { $state.Cached = [IO.File]::ReadAllBytes($Cache) } catch { }
    $info = New-Object System.IO.FileInfo -ArgumentList $Cache
    if ($info.Exists -and ([DateTime]::UtcNow - $info.LastWriteTimeUtc) -lt $script:UpdateCheckEvery) { return $state }
    $state.Cts = New-Object System.Threading.CancellationTokenSource -ArgumentList $script:UpdateCheckLimit
    try { $state.Head = Start-LbfLatestVersionHead $Base $state.Cts.Token } catch { $state.Head = $null }
    $state.Fresh = $true
    return $state
}

function Complete-LbfCachedLatestVersion {
    param([hashtable]$State)
    if (-not $State.ContainsKey('Fresh')) { return $script:Utf8NoBom.GetString($State.Cached) }
    $cached = $State.Cached
    if ($null -ne $State.Head) {
        $wait = [int][Math]::Max(0, ($State.Deadline - [DateTime]::UtcNow).TotalMilliseconds)
        $done = $false
        try { $done = $State.Head.Task.Wait($wait) } catch { $done = $true }
        if ($done) {
            try { $cached = $script:Utf8NoBom.GetBytes((Get-LbfLatestVersionFromHead $State.Head)) } catch { }
        } else {
            $State.Cts.Cancel()
        }
    }
    try {
        [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($State.Cache))
        [IO.File]::WriteAllBytes($State.Cache, $cached)
    } catch { }
    return $script:Utf8NoBom.GetString($cached)
}

function Get-LbfCachedLatestVersion {
    param([string]$Base, [string]$Cache)
    return Complete-LbfCachedLatestVersion (Start-LbfCachedLatestVersion $Base $Cache)
}

# Go's startUpdateCheck: looks up the latest release while the command runs.
function Start-LbfUpdateCheck {
    param([AllowEmptyCollection()][string[]]$Arguments)
    if ($script:LbfVersion -eq 'dev' -or $env:LBF_NO_UPDATE_CHECK -or ($Arguments.Count -gt 0 -and (Test-LbfSame $Arguments[0] 'upgrade'))) { return $null }
    try { $dir = Get-LbfUserCacheDir } catch { return @{ Lookup = $null } }
    $cache = [IO.Path]::Combine($dir, 'lbf', 'latest-version')
    return @{ Lookup = (Start-LbfCachedLatestVersion (Get-LbfReleasesUrl) $cache) }
}

function Complete-LbfUpdateCheck {
    param($State)
    if ($null -eq $State -or $null -eq $State.Lookup) { return }
    $latest = Complete-LbfCachedLatestVersion $State.Lookup
    if (Test-LbfNewerVersion $latest $script:LbfVersion) {
        Write-LbfErr (Format-LbfUpgradeWarning $script:LbfVersion $latest)
    }
}

function Invoke-LbfHttpGet {
    param([string]$Url)
    $client = New-LbfHttpClient
    try {
        try {
            $resp = $client.GetAsync($Url).GetAwaiter().GetResult()
        } catch {
            throw (New-LbfError "Get `"$Url`": $(Get-LbfErrorText $_)")
        }
        try {
            if ([int]$resp.StatusCode -ne 200) { throw (New-LbfError "downloading ${Url}: HTTP $([int]$resp.StatusCode)") }
            return , $resp.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        } finally { $resp.Dispose() }
    } finally { $client.Dispose() }
}

# Go's checksumFor.
function Get-LbfChecksumFor {
    param([byte[]]$Sums, [string]$Name)
    $text = $script:Utf8NoBom.GetString($Sums)
    foreach ($line in ($text -split "`n")) {
        $line = $line.TrimEnd("`r")
        $fields = @($line -split '\s+' | Where-Object { $_.Length -gt 0 })
        if ($fields.Count -eq 2) {
            $f = $fields[1]
            if ($f.StartsWith('*', [StringComparison]::Ordinal)) { $f = $f.Substring(1) }
            if (Test-LbfSame $f $Name) { return $fields[0] }
        }
    }
    throw (New-LbfError "SHA256SUMS has no entry for $Name")
}

# The script to replace, as Go's os.Executable with filepath.EvalSymlinks.
function Get-LbfScriptPath {
    $path = $script:LbfScriptPath
    if (-not $path) { throw (New-LbfError 'lbf cannot tell which file it was started from; run lbf.ps1 by its path, or reinstall with the command in the README') }
    $item = Get-Item -LiteralPath $path -Force
    if ($item.PSObject.Properties['LinkType'] -and $item.LinkType -eq 'SymbolicLink') {
        $target = @($item.Target)[0]
        if (-not [IO.Path]::IsPathRooted($target)) { $target = [IO.Path]::Combine([IO.Path]::GetDirectoryName($path), $target) }
        $path = [IO.Path]::GetFullPath($target)
    }
    return $path
}

# Go's replaceExecutable for a script: PowerShell reads a script whole before running it, so the running file can be replaced.
function Set-LbfScriptContent {
    param([string]$Path, [byte[]]$Bytes)
    $tmp = "$Path.new"
    try { [IO.File]::Delete($tmp) } catch { }
    try {
        $fs = New-Object System.IO.FileStream -ArgumentList $tmp, ([IO.FileMode]::Create), ([IO.FileAccess]::Write), ([IO.FileShare]::None)
    } catch {
        $msg = Format-LbfOsError 'open' $tmp $_
        if ((Get-LbfException $_) -is [UnauthorizedAccessException]) {
            $msg += '; lbf cannot write to its own folder, so ask whoever installed it to upgrade, or install your own copy with the command in the README'
        }
        throw (New-LbfError $msg)
    }
    try {
        try { $fs.Write($Bytes, 0, $Bytes.Length); $fs.Flush() } finally { $fs.Dispose() }
    } catch {
        try { [IO.File]::Delete($tmp) } catch { }
        throw (New-LbfError (Format-LbfOsError 'write' $tmp $_))
    }
    $tries = 1
    if ($script:OnWindows) { $tries = 5 }
    for ($i = 1; $i -le $tries; $i++) {
        try {
            [IO.File]::Replace($tmp, $Path, [NullString]::Value)
            return
        } catch {
            if ($i -lt $tries) { Start-Sleep -Milliseconds 200; continue }
            try { [IO.File]::Delete($tmp) } catch { }
            throw (New-LbfError "rename $tmp ${Path}: $(Get-LbfOsReason $_)")
        }
    }
}

# Go's upgrade, fetching lbf.ps1 instead of an executable.
function Invoke-LbfUpgrade {
    param([string]$Base = (Get-LbfReleasesUrl), [string]$Current = $script:LbfVersion, [string]$Path)
    if (-not $Path) { $Path = Get-LbfScriptPath }
    $latest = Get-LbfLatestVersion $Base
    if (-not (Test-LbfNewerVersion $latest $Current)) { return @{ Latest = $latest; Upgraded = $false } }
    $download = "$Base/download/$latest/"
    $sums = Invoke-LbfHttpGet ($download + 'SHA256SUMS')
    $want = Get-LbfChecksumFor $sums $script:UpgradeAsset
    $bin = Invoke-LbfHttpGet ($download + $script:UpgradeAsset)
    if (-not (Test-LbfSame (Get-LbfSha256Hex -Bytes $bin) $want)) {
        throw (New-LbfError "downloaded $($script:UpgradeAsset) does not match its published checksum; nothing was changed")
    }
    try {
        Set-LbfScriptContent $Path $bin
    } catch {
        throw (New-LbfError "replacing ${Path}: $(Get-LbfErrorText $_)")
    }
    return @{ Latest = $latest; Upgraded = $true }
}
