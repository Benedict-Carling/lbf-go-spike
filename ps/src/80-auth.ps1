$script:AzureCliClientId = '04b07795-8ddb-461a-bbee-02f9e1bf7b46'
$script:ArmScope = 'https://management.azure.com/.default'
$script:StorageScope = 'https://storage.azure.com/.default'
$script:AzNotSignedIn = "Please run 'az login'"
$script:AzTimeout = [TimeSpan]::FromMinutes(2)
$script:BlobServiceVersion = '2026-12-06'
$script:BrowserCredName = 'InteractiveBrowserCredential'
$script:AzCredName = 'AzureCLICredential'
$script:AzTroubleshoot = 'To troubleshoot, visit https://aka.ms/azsdk/go/identity/troubleshoot#azure-cli'
$script:ErrInvalidTenantId = 'invalid tenantID. You can locate your tenantID by following the instructions listed here: https://learn.microsoft.com/partner-center/find-ids-and-domain-names'
$script:ErrNoChoice = 'no storage account chosen'
$script:AuthRequired = "InteractiveBrowserCredential can't acquire a token without user interaction. Call Authenticate to authenticate a user interactively"
$script:HttpRule = '-' * 80
$script:TokenStoreChecked = $false
$script:TokenStoreErr = $null
$script:GoStatusText = @{
    200 = 'OK'; 201 = 'Created'; 202 = 'Accepted'; 204 = 'No Content'; 400 = 'Bad Request'; 401 = 'Unauthorized'; 403 = 'Forbidden'
    404 = 'Not Found'; 405 = 'Method Not Allowed'; 408 = 'Request Timeout'; 409 = 'Conflict'; 412 = 'Precondition Failed'
    429 = 'Too Many Requests'; 500 = 'Internal Server Error'; 502 = 'Bad Gateway'; 503 = 'Service Unavailable'; 504 = 'Gateway Timeout'
}

$script:OkPage = "`n<!DOCTYPE html>`n<html>`n<head>`n    <meta charset=`"utf-8`" />`n    <title>Authentication Complete</title>`n</head>`n<body>`n    <p>Authentication complete. You can return to the application. Feel free to close this browser tab.</p>`n    <p><strong>For your security:</strong> Do not share the contents of this page, the address bar, or take screenshots.</p>`n</body>`n</html>`n"
$script:FailPage = "`n<!DOCTYPE html>`n<html>`n<head>`n    <meta charset=`"utf-8`" />`n    <title>Authentication Failed</title>`n</head>`n<body>`n`t<p>Authentication failed. You can return to the application. Feel free to close this browser tab.</p>`n`t<p>Error details: error {0} error_description: {1}</p>`n</body>`n</html>`n"
$script:GetPage = "`n<!DOCTYPE html>`n<html>`n<head>`n    <meta charset=`"utf-8`" />`n    <title>Authentication Failed</title>`n</head>`n<body>`n    <p>Authentication failed. The response was received via a GET operation, which is not supported.</p>`n    <p>You can return to the application. Feel free to close this browser tab.</p>`n</body>`n</html>`n"

# ---------- test seams ----------

# LBF_TEST_AUTHORITY replaces https://login.microsoftonline.com in tests.
function Get-LbfAuthorityHost {
    if ($env:LBF_TEST_AUTHORITY) { return $env:LBF_TEST_AUTHORITY.TrimEnd('/') }
    return 'https://login.microsoftonline.com'
}

# LBF_TEST_ARM replaces https://management.azure.com in tests.
function Get-LbfArmEndpoint {
    if ($env:LBF_TEST_ARM) { return $env:LBF_TEST_ARM.TrimEnd('/') }
    return 'https://management.azure.com'
}

# LBF_TEST_BLOB_SERVICE, e.g. http://127.0.0.1:1234/{account}, replaces https://<account>.blob.core.windows.net in tests.
function Get-LbfBlobServiceUrl {
    param([Parameter(Mandatory)][string]$Account)
    if ($env:LBF_TEST_BLOB_SERVICE) { return $env:LBF_TEST_BLOB_SERVICE.Replace('{account}', $Account).TrimEnd('/') }
    return "https://$Account.blob.core.windows.net"
}

# LBF_TEST_LOGIN_TIMEOUT shortens the browser sign-in's 5 minutes to this many seconds in tests.
function Get-LbfLoginTimeout {
    if ($env:LBF_TEST_LOGIN_TIMEOUT) { return [TimeSpan]::FromSeconds([int]$env:LBF_TEST_LOGIN_TIMEOUT) }
    return [TimeSpan]::FromMinutes(5)
}

# ---------- small helpers ----------

function Get-LbfAuthGoos {
    if ($script:OnWindows) { return 'windows' }
    if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) { return 'darwin' }
    return 'linux'
}

# Go's time.Duration.String for whole seconds.
function Format-LbfAuthDuration {
    param([Parameter(Mandatory)][TimeSpan]$Duration)
    $s = [long][Math]::Floor($Duration.TotalSeconds)
    if ($s -eq 0) { return '0s' }
    $h = [long][Math]::Floor([double]$s / 3600)
    $m = [long][Math]::Floor([double]($s % 3600) / 60)
    $sec = $s % 60
    if ($h -gt 0) { return "${h}h${m}m${sec}s" }
    if ($m -gt 0) { return "${m}m${sec}s" }
    return "${sec}s"
}

# Go's url.QueryEscape.
function ConvertTo-LbfAuthQueryEscape {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $sb = New-Object System.Text.StringBuilder
    foreach ($b in $script:Utf8NoBom.GetBytes($Text)) {
        if (($b -ge 0x41 -and $b -le 0x5A) -or ($b -ge 0x61 -and $b -le 0x7A) -or ($b -ge 0x30 -and $b -le 0x39) -or $b -eq 0x2D -or $b -eq 0x2E -or $b -eq 0x5F -or $b -eq 0x7E) {
            [void]$sb.Append([char]$b)
        } elseif ($b -eq 0x20) {
            [void]$sb.Append('+')
        } else {
            [void]$sb.Append('%' + $b.ToString('X2'))
        }
    }
    return $sb.ToString()
}

# Go's url.Values.Encode: keys sorted, each value query-escaped.
function ConvertTo-LbfAuthQueryString {
    param([Parameter(Mandatory)][hashtable]$Values)
    $parts = New-Object System.Collections.Generic.List[string]
    foreach ($k in (Get-LbfSortedUtf8 @($Values.Keys))) { $parts.Add((ConvertTo-LbfAuthQueryEscape $k) + '=' + (ConvertTo-LbfAuthQueryEscape ([string]$Values[$k]))) }
    return $parts -join '&'
}

function ConvertFrom-LbfFormEncoded {
    param([AllowEmptyString()][string]$Text)
    $form = New-LbfMap
    if ($Text.Length -eq 0) { return , $form }
    foreach ($pair in $Text.Split([char]'&')) {
        if ($pair.Length -eq 0) { continue }
        if ($pair.IndexOf([char]';') -ge 0) { throw (New-LbfError 'invalid semicolon separator in query') }
        $i = $pair.IndexOf([char]'=')
        if ($i -ge 0) { $k = $pair.Substring(0, $i); $v = $pair.Substring($i + 1) } else { $k = $pair; $v = '' }
        $k = ConvertFrom-LbfAuthQueryEscape $k
        $v = ConvertFrom-LbfAuthQueryEscape $v
        if (-not $form.ContainsKey($k)) { $form[$k] = $v }
    }
    return , $form
}

function ConvertFrom-LbfAuthQueryEscape {
    param([AllowEmptyString()][string]$Text)
    $bad = [regex]::Match($Text, '%(?![0-9A-Fa-f]{2})')
    if ($bad.Success) {
        $end = [Math]::Min($bad.Index + 3, $Text.Length)
        throw (New-LbfError "invalid URL escape $(Format-LbfQuote $Text.Substring($bad.Index, $end - $bad.Index))")
    }
    $bytes = New-Object System.Collections.Generic.List[byte]
    for ($i = 0; $i -lt $Text.Length; $i++) {
        $c = $Text[$i]
        if ($c -eq '+') { $bytes.Add(0x20); continue }
        if ($c -eq '%') { $bytes.Add([Convert]::ToByte($Text.Substring($i + 1, 2), 16)); $i += 2; continue }
        foreach ($b in $script:Utf8NoBom.GetBytes([string]$c)) { $bytes.Add($b) }
    }
    return $script:Utf8NoBom.GetString($bytes.ToArray())
}

function ConvertTo-LbfBase64Url {
    param([Parameter(Mandatory)][byte[]]$Bytes)
    return [Convert]::ToBase64String($Bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function ConvertFrom-LbfBase64Url {
    param([AllowEmptyString()][string]$Text)
    $Text = $Text.Replace("`r", '').Replace("`n", '')
    if ($Text -cmatch '[^A-Za-z0-9_-]') { return $null }
    $s = $Text.Replace('-', '+').Replace('_', '/')
    switch ($s.Length % 4) {
        1 { return $null }
        2 { $s += '==' }
        3 { $s += '=' }
    }
    try { return , [Convert]::FromBase64String($s) } catch { return $null }
}

# Go's html.EscapeString.
function ConvertTo-LbfHtmlEscape {
    param([AllowEmptyString()][string]$Text)
    return $Text.Replace('&', '&amp;').Replace("'", '&#39;').Replace('<', '&lt;').Replace('>', '&gt;').Replace('"', '&#34;')
}

# The claims of a JWT, or $null when it is not one.
function Get-LbfJwtClaims {
    param([AllowEmptyString()][string]$Token)
    $parts = $Token.Split([char]'.')
    if ($parts.Count -ne 3) { return $null }
    $raw = ConvertFrom-LbfBase64Url $parts[1]
    if ($null -eq $raw) { return $null }
    try { $claims = ConvertFrom-LbfJson ($script:Utf8NoBom.GetString($raw)) -LastWins } catch { return $null }
    if ($claims -isnot [System.Collections.Specialized.OrderedDictionary]) { return $null }
    return , $claims
}

# encoding/json's foldName as far as ASCII field names need it: only the Kelvin sign and long s fold onto ASCII letters.
function ConvertTo-LbfJsonFoldName {
    param([AllowEmptyString()][string]$Name)
    $chars = $Name.ToCharArray()
    for ($i = 0; $i -lt $chars.Length; $i++) {
        $c = [int]$chars[$i]
        if ($c -ge 0x61 -and $c -le 0x7A) { $chars[$i] = [char]($c - 0x20) }
        elseif ($c -eq 0x212A) { $chars[$i] = [char]0x4B }
        elseif ($c -eq 0x17F) { $chars[$i] = [char]0x53 }
    }
    return New-Object string (, $chars)
}

function Get-LbfClaimString {
    param($Claims, [string]$Name)
    if ($null -eq $Claims -or -not $Claims.Contains($Name)) { return '' }
    $v = $Claims[$Name]
    if ($v -is [string]) { return $v }
    return ''
}

function Test-LbfTenantId {
    param([AllowEmptyString()][string]$Tenant)
    return $Tenant -cmatch '\A[A-Za-z0-9.-]+\z'
}

function Assert-LbfTenantId {
    param([AllowEmptyString()][string]$Tenant)
    if (-not (Test-LbfTenantId $Tenant)) { throw (New-LbfError $script:ErrInvalidTenantId) }
}

# Go's exec.LookPath: the first match on PATH, or $null.
function Find-LbfExecutable {
    param([Parameter(Mandatory)][string]$Name)
    $path = [Environment]::GetEnvironmentVariable('PATH')
    if ($null -eq $path) { $path = '' }
    $exts = @('')
    if ($script:OnWindows) {
        $pathext = [Environment]::GetEnvironmentVariable('PATHEXT')
        if ([string]::IsNullOrEmpty($pathext)) { $pathext = '.com;.exe;.bat;.cmd' }
        $exts = @($pathext.ToLowerInvariant().Split([char]';') | Where-Object { $_.Length -gt 0 } | ForEach-Object { if ($_.StartsWith('.', [StringComparison]::Ordinal)) { $_ } else { '.' + $_ } })
        if ([IO.Path]::HasExtension($Name)) { $exts = @('') + $exts }
    }
    foreach ($dir in $path.Split([IO.Path]::PathSeparator)) {
        if ($script:OnWindows) { $dir = $dir.Trim('"') }
        if ($dir.Length -eq 0) { $dir = '.' }
        foreach ($ext in $exts) {
            $candidate = [IO.Path]::Combine($dir, $Name + $ext)
            if (-not [IO.File]::Exists($candidate)) { continue }
            if (-not $script:OnWindows -and -not (Test-LbfUnixExecutable $candidate)) { continue }
            if (-not [IO.Path]::IsPathRooted($dir)) { return $null }
            return $candidate
        }
    }
    return $null
}

function Test-LbfUnixExecutable {
    param([string]$Path)
    try {
        $mode = [int][IO.File]::GetUnixFileMode($Path)
        return ($mode -band 0x49) -ne 0
    } catch { return $true }
}

# ---------- HTTP ----------

function Invoke-LbfAuthHttpOnce {
    param([string]$Method, [string]$Uri, [hashtable]$Headers, [byte[]]$Body, [string]$ContentType, [string]$Accept)
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }
    $resp = $null
    try {
        $req = [System.Net.HttpWebRequest]([System.Net.WebRequest]::Create($Uri))
        $req.Method = $Method
        $req.Timeout = 300000
        $req.ReadWriteTimeout = 300000
        try { $req.ServicePoint.Expect100Continue = $false } catch { }
        try { if ($null -ne $req.Proxy) { $req.Proxy.Credentials = [System.Net.CredentialCache]::DefaultNetworkCredentials } } catch { }
        if ($Accept -ne '') { $req.Accept = $Accept }
        if ($null -ne $Headers) { foreach ($k in $Headers.Keys) { $req.Headers[$k] = [string]$Headers[$k] } }
        if ($null -ne $Body) {
            if ($ContentType -ne '') { $req.ContentType = $ContentType }
            $req.ContentLength = $Body.Length
            $rs = $req.GetRequestStream()
            try { $rs.Write($Body, 0, $Body.Length) } finally { $rs.Dispose() }
        }
        $resp = $req.GetResponse()
    } catch {
        $ex = Get-LbfException $_
        if ($ex -is [System.Net.WebException] -and $null -ne $ex.Response) {
            $resp = $ex.Response
        } else {
            $verb = $Method.Substring(0, 1) + $Method.Substring(1).ToLowerInvariant()
            return @{ TransportError = "$verb $(Format-LbfQuote $Uri): $($ex.Message)" }
        }
    }
    try {
        $ms = New-Object System.IO.MemoryStream
        $s = $resp.GetResponseStream()
        try { $s.CopyTo($ms) } finally { $s.Dispose() }
        $bytes = $ms.ToArray()
        $hdr = @{}
        foreach ($k in $resp.Headers.AllKeys) { $hdr[$k.ToLowerInvariant()] = $resp.Headers[$k] }
        return @{
            TransportError = $null; Method = $Method; Uri = $Uri; Status = [int]$resp.StatusCode; Reason = [string]$resp.StatusDescription
            Headers = $hdr; Body = $bytes; Text = $script:Utf8NoBom.GetString($bytes)
        }
    } finally { $resp.Close() }
}

# One request with azcore's retries on throttling, server errors and dropped connections.
function Invoke-LbfAuthHttp {
    param([Parameter(Mandatory)][string]$Method, [Parameter(Mandatory)][string]$Uri, [hashtable]$Headers = @{},
        [byte[]]$Body = $null, [string]$ContentType = '', [string]$Accept = '')
    $try = 0
    while ($true) {
        $r = Invoke-LbfAuthHttpOnce -Method $Method -Uri $Uri -Headers $Headers -Body $Body -ContentType $ContentType -Accept $Accept
        $retry = $null -ne $r.TransportError -or @(408, 429, 500, 502, 503, 504) -contains $r.Status
        if (-not $retry -or $try -ge 3) {
            if ($null -ne $r.TransportError) { throw (New-LbfError $r.TransportError) }
            return $r
        }
        $try++
        $delay = 800 * [Math]::Pow(2, $try - 1)
        if ($null -eq $r.TransportError -and $r.Headers.ContainsKey('retry-after')) {
            $sec = 0
            if ([int]::TryParse($r.Headers['retry-after'], [ref]$sec)) { $delay = [Math]::Min($sec, 60) * 1000 }
        }
        Start-Sleep -Milliseconds ([int]$delay)
    }
}

function Get-LbfHttpStatusLine {
    param([Parameter(Mandatory)][hashtable]$Response)
    $text = $script:GoStatusText[$Response.Status]
    if (-not $text) { $text = $Response.Reason }
    return "$($Response.Status) $text"
}

# Go's json.Indent(dst, body, "", "  "), or the body unchanged when it is not JSON.
function Format-LbfGoIndent {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    try { [void](ConvertFrom-LbfJson $Text -LastWins) } catch { return $Text }
    $sb = New-Object System.Text.StringBuilder
    $depth = 0
    $needIndent = $false
    $inString = $false
    $escaped = $false
    $started = $false
    foreach ($c in $Text.ToCharArray()) {
        if ($inString) {
            [void]$sb.Append($c)
            if ($escaped) { $escaped = $false }
            elseif ($c -eq '\') { $escaped = $true }
            elseif ($c -eq '"') { $inString = $false }
            continue
        }
        if ($c -eq ' ' -or $c -eq "`t" -or $c -eq "`r" -or $c -eq "`n") {
            if ($started -and $depth -eq 0 -and -not $needIndent) { [void]$sb.Append($c) }
            continue
        }
        if ($needIndent -and $c -ne '}' -and $c -ne ']') {
            $needIndent = $false
            $depth++
            [void]$sb.Append("`n").Append('  ' * $depth)
        }
        $started = $true
        switch ($c) {
            { $_ -eq '{' -or $_ -eq '[' } { $needIndent = $true; [void]$sb.Append($c); break }
            ',' { [void]$sb.Append(',').Append("`n").Append('  ' * $depth); break }
            ':' { [void]$sb.Append(': '); break }
            { $_ -eq '}' -or $_ -eq ']' } {
                if ($needIndent) { $needIndent = $false }
                else { $depth--; [void]$sb.Append("`n").Append('  ' * $depth) }
                [void]$sb.Append($c)
                break
            }
            '"' { $inString = $true; [void]$sb.Append($c); break }
            default { [void]$sb.Append($c) }
        }
    }
    return $sb.ToString()
}

# The error code azcore's ResponseError reports.
function Get-LbfAuthErrorCode {
    param([Parameter(Mandatory)][hashtable]$Response)
    if ($Response.Headers.ContainsKey('x-ms-error-code') -and ([string]$Response.Headers['x-ms-error-code']).Length -gt 0) { return $Response.Headers['x-ms-error-code'] }
    if ($Response.Body.Length -eq 0) { return '' }
    $code = ''
    $obj = $null
    try { $obj = ConvertFrom-LbfJson $Response.Text -LastWins } catch { $obj = $null }
    if ($obj -is [System.Collections.Specialized.OrderedDictionary]) {
        $inner = $obj
        if ($obj.Contains('error')) { $inner = $obj['error'] }
        elseif ($obj.Contains('odata.error')) { $inner = $obj['odata.error'] }
        if ($inner -is [System.Collections.Specialized.OrderedDictionary] -and $inner.Contains('code') -and $inner['code'] -is [string]) { $code = $inner['code'] }
    }
    if ($code.Length -gt 0) { return $code }
    $m = [regex]::Match($Response.Text, '<(?:[0-9A-Za-z_]+:)?[c|C]ode>[\t\n\f\r ]*([0-9A-Za-z_]+)[\t\n\f\r ]*</(?:[0-9A-Za-z_]+:)?[c|C]ode>')
    if ($m.Success) { return $m.Groups[1].Value }
    return ''
}

# Scheme, host and escaped path of a URL as written, as Go's url.URL holds them.
function Get-LbfUrlParts {
    param([Parameter(Mandatory)][string]$Uri)
    $m = [regex]::Match($Uri, '^([A-Za-z][A-Za-z0-9+.-]*)://([^/?#]*)([^?#]*)')
    return @{ Scheme = $m.Groups[1].Value.ToLowerInvariant(); Host = $m.Groups[2].Value; Path = $m.Groups[3].Value }
}

# azcore's ResponseError text.
function Format-LbfAzureResponseError {
    param([Parameter(Mandatory)][hashtable]$Response)
    $u = Get-LbfUrlParts $Response.Uri
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("$($Response.Method) $($u.Scheme)://$($u.Host)$($u.Path)`n")
    [void]$sb.Append("$script:HttpRule`n")
    [void]$sb.Append("RESPONSE $($Response.Status): $(Get-LbfHttpStatusLine $Response)`n")
    $code = Get-LbfAuthErrorCode $Response
    if ($code.Length -gt 0) { [void]$sb.Append("ERROR CODE: $code`n") } else { [void]$sb.Append("ERROR CODE UNAVAILABLE`n") }
    [void]$sb.Append("$script:HttpRule`n")
    if ($Response.Body.Length -gt 0) { [void]$sb.Append((Format-LbfGoIndent $Response.Text)).Append("`n") }
    else { [void]$sb.Append("Response contained no body`n") }
    [void]$sb.Append("$script:HttpRule`n")
    return $sb.ToString()
}

# azidentity's AuthenticationFailedError text for a failed request to Entra.
function Format-LbfAuthFailed {
    param([Parameter(Mandatory)][hashtable]$Response)
    $u = Get-LbfUrlParts $Response.Uri
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("$script:BrowserCredName authentication failed. `n")
    [void]$sb.Append("$($Response.Method) $($u.Scheme)://$($u.Host)$([Uri]::UnescapeDataString($u.Path))`n")
    [void]$sb.Append("$script:HttpRule`n")
    [void]$sb.Append("RESPONSE $($Response.Status): $(Get-LbfHttpStatusLine $Response)`n")
    [void]$sb.Append("$script:HttpRule`n")
    if ($Response.Body.Length -gt 0) { [void]$sb.Append((Format-LbfGoIndent $Response.Text)) }
    else { [void]$sb.Append('Response contained no body') }
    [void]$sb.Append("`n$script:HttpRule")
    return $sb.ToString()
}

# ---------- where lbf keeps a login ----------

# Go's os.UserConfigDir.
function Get-LbfUserConfigDir {
    if ($script:OnWindows) {
        $d = $env:APPDATA
        if ([string]::IsNullOrEmpty($d)) { throw (New-LbfError '%AppData% is not defined') }
        return $d
    }
    if ((Get-LbfAuthGoos) -eq 'darwin') {
        $h = $env:HOME
        if ([string]::IsNullOrEmpty($h)) { throw (New-LbfError '$HOME is not defined') }
        return $h.TrimEnd('/') + '/Library/Application Support'
    }
    $x = $env:XDG_CONFIG_HOME
    if ([string]::IsNullOrEmpty($x)) {
        $h = $env:HOME
        if ([string]::IsNullOrEmpty($h)) { throw (New-LbfError 'neither $XDG_CONFIG_HOME nor $HOME are defined') }
        return $h.TrimEnd('/') + '/.config'
    }
    if (-not $x.StartsWith('/', [StringComparison]::Ordinal)) { throw (New-LbfError 'path in $XDG_CONFIG_HOME is relative') }
    return $x
}

function Get-LbfRecordPath {
    $dir = (Get-LbfUserConfigDir).TrimEnd([IO.Path]::DirectorySeparatorChar)
    return [IO.Path]::Combine([IO.Path]::Combine($dir, 'lbf'), 'auth-record.json')
}

# The saved login's account (azidentity's AuthenticationRecord), or $null.
function Read-LbfAuthRecord {
    try { $path = Get-LbfRecordPath } catch { return $null }
    try { $raw = Read-LbfTextFile $path } catch { return $null }
    try { $obj = ConvertFrom-LbfJson $raw -LastWins } catch { return $null }
    if ($obj -isnot [System.Collections.Specialized.OrderedDictionary]) { return $null }
    $rec = @{ Authority = ''; ClientId = ''; HomeAccountId = ''; TenantId = ''; Username = ''; Version = '' }
    $fields = New-LbfMap
    foreach ($f in @('Authority', 'ClientId', 'HomeAccountId', 'TenantId', 'Username', 'Version')) { $fields[(ConvertTo-LbfJsonFoldName $f)] = $f }
    foreach ($k in $obj.Keys) {
        $f = $fields[(ConvertTo-LbfJsonFoldName ([string]$k))]
        if ($null -eq $f) { continue }
        $v = $obj[$k]
        if ($null -eq $v) { continue }
        if ($v -isnot [string]) { return $null }
        $rec[$f] = $v
    }
    if (-not [string]::Equals($rec.Version, '1.0')) { return $null }
    return $rec
}

function Save-LbfAuthRecord {
    param([Parameter(Mandatory)][hashtable]$Record)
    $path = Get-LbfRecordPath
    $dir = [IO.Path]::GetDirectoryName($path)
    try { [void][IO.Directory]::CreateDirectory($dir) } catch { throw (New-LbfError (Format-LbfOsError 'mkdir' $dir $_)) }
    $o = New-LbfOrdered
    $o['authority'] = $Record.Authority
    $o['clientId'] = $Record.ClientId
    $o['homeAccountId'] = $Record.HomeAccountId
    $o['tenantId'] = $Record.TenantId
    $o['username'] = $Record.Username
    $o['version'] = '1.0'
    try { Write-LbfTextFile $path (ConvertTo-LbfJson $o) } catch { throw (New-LbfError (Format-LbfOsError 'open' $path $_)) }
}

# $null when this lbf can remember logins; on Windows it keeps them with DPAPI, elsewhere it cannot.
function Get-LbfTokenStoreError {
    if (-not $script:TokenStoreChecked) {
        $script:TokenStoreChecked = $true
        if (-not $script:OnWindows) {
            $e = New-LbfError 'this build of lbf cannot remember logins'
            $e.Data['lbfNoStore'] = $true
            $script:TokenStoreErr = $e
        } elseif ($null -eq (Get-LbfProtectedDataType)) {
            $script:TokenStoreErr = New-LbfError 'the Windows token store is not available: this PowerShell cannot load System.Security.Cryptography.ProtectedData'
        }
    }
    return $script:TokenStoreErr
}

function Get-LbfProtectedDataType {
    $t = 'System.Security.Cryptography.ProtectedData' -as [type]
    if ($null -ne $t) { return $t }
    foreach ($n in @('System.Security', 'System.Security.Cryptography.ProtectedData')) {
        try { Add-Type -AssemblyName $n -ErrorAction Stop } catch { }
        $t = 'System.Security.Cryptography.ProtectedData' -as [type]
        if ($null -ne $t) { return $t }
    }
    return $null
}

function Get-LbfLocalAppData {
    $d = $env:LOCALAPPDATA
    if ([string]::IsNullOrEmpty($d)) { $d = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData) }
    return $d
}

function Get-LbfTokenFilePath {
    return [IO.Path]::Combine([IO.Path]::Combine((Get-LbfLocalAppData), 'lbf'), 'login-tokens.dat')
}

# The files that hold lbf's tokens: this lbf's, and those lbf.exe's azidentity cache keeps.
function Get-LbfTokenFiles {
    $local = Get-LbfLocalAppData
    $ids = [IO.Path]::Combine($local, '.IdentityService')
    return , @((Get-LbfTokenFilePath), [IO.Path]::Combine($ids, 'lbf'), [IO.Path]::Combine($ids, 'lbf.cae'))
}

function Read-LbfLoginTokens {
    if ($null -ne (Get-LbfTokenStoreError)) { return $null }
    $path = Get-LbfTokenFilePath
    if (-not [IO.File]::Exists($path)) { return $null }
    try {
        $dp = Get-LbfProtectedDataType
        $plain = $dp::Unprotect([IO.File]::ReadAllBytes($path), $script:Utf8NoBom.GetBytes('lbf'), [System.Security.Cryptography.DataProtectionScope]::CurrentUser)
        $obj = ConvertFrom-LbfJson ($script:Utf8NoBom.GetString($plain)) -LastWins
        if ($obj -isnot [System.Collections.Specialized.OrderedDictionary]) { return $null }
        return , $obj
    } catch { return $null }
}

function Save-LbfLoginTokens {
    param([Parameter(Mandatory)][string]$HomeAccountId, [Parameter(Mandatory)][string]$RefreshToken)
    $path = Get-LbfTokenFilePath
    $o = New-LbfOrdered
    $o['homeAccountId'] = $HomeAccountId
    $o['refreshToken'] = $RefreshToken
    $dp = Get-LbfProtectedDataType
    $sealed = $dp::Protect($script:Utf8NoBom.GetBytes((ConvertTo-LbfJson $o)), $script:Utf8NoBom.GetBytes('lbf'), [System.Security.Cryptography.DataProtectionScope]::CurrentUser)
    [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path))
    [IO.File]::WriteAllBytes($path, $sealed)
}

# Go's clearTokenCache: whether any saved tokens were found and deleted.
function Clear-LbfTokenStore {
    if (-not $script:OnWindows) { return $false }
    $found = $false
    foreach ($p in (Get-LbfTokenFiles)) {
        if (-not [IO.File]::Exists($p)) { continue }
        try { [IO.File]::Delete($p) } catch { throw (New-LbfError (Format-LbfOsError 'remove' $p $_)) }
        $found = $true
    }
    return $found
}

function Invoke-LbfLogout {
    $path = Get-LbfRecordPath
    $rec = Read-LbfAuthRecord
    $user = ''
    if ($null -ne $rec) { $user = $rec.Username }
    try { $hadTokens = Clear-LbfTokenStore }
    catch { throw (New-LbfError "could not delete lbf's saved tokens, so you are still signed in: $(Get-LbfErrorText $_)") }
    if (-not [IO.File]::Exists($path) -and -not [IO.Directory]::Exists($path)) { return @{ User = $user; HadRecord = $false; HadTokens = $hadTokens } }
    try { [IO.File]::Delete($path) } catch { throw (New-LbfError (Format-LbfOsError 'remove' $path $_)) }
    return @{ User = $user; HadRecord = $true; HadTokens = $hadTokens }
}

# ---------- whether a browser can be opened ----------

function Get-LbfNoBrowserReason {
    param([bool]$StdinTerminal, [bool]$StderrTerminal, [string]$Goos, [hashtable]$Environment)
    if (-not $StdinTerminal) { return 'stdin is not a terminal, as in a batch job' }
    if (-not $StderrTerminal) { return 'stderr is not a terminal' }
    if ($Goos -eq 'linux' -and [string]::IsNullOrEmpty($Environment['DISPLAY']) -and [string]::IsNullOrEmpty($Environment['WAYLAND_DISPLAY'])) {
        return 'there is no display (neither DISPLAY nor WAYLAND_DISPLAY is set)'
    }
    return ''
}

# LBF_TEST_BROWSER names a file that receives the sign-in URL instead of a browser, for a test that plays the browser.
function Get-LbfBrowserUnavailable {
    if ($env:LBF_TEST_BROWSER) { return '' }
    $envs = @{ DISPLAY = $env:DISPLAY; WAYLAND_DISPLAY = $env:WAYLAND_DISPLAY }
    return Get-LbfNoBrowserReason -StdinTerminal (-not [Console]::IsInputRedirected) -StderrTerminal (-not [Console]::IsErrorRedirected) -Goos (Get-LbfAuthGoos) -Environment $envs
}

function Assert-LbfBrowser {
    param([string]$Tenant)
    $reason = Get-LbfBrowserUnavailable
    if ($reason.Length -gt 0) {
        throw (New-LbfError "cannot sign in with a browser here: $reason`nSign in with 'az login --tenant $Tenant' first ('az login --tenant $Tenant --use-device-code' works over SSH and on HPC), or use --sas-env with a file from 'lbf mint-sas' run on a machine with a browser")
    }
}

# ---------- signing in with the browser ----------

function New-LbfBrowserCredential {
    param([string]$Tenant, [switch]$SilentOnly)
    return @{
        Kind = 'browser'; Tenant = $Tenant; RefreshToken = ''; HomeAccountId = ''; Tokens = @{}
        SilentOnly = [bool]$SilentOnly; Persist = $false
    }
}

function Get-LbfEntraEndpoints {
    param([string]$Tenant)
    $r = Invoke-LbfAuthHttp -Method 'GET' -Uri "$(Get-LbfAuthorityHost)/$Tenant/v2.0/.well-known/openid-configuration" -Accept 'application/json'
    if ($r.Status -ne 200) { throw (New-LbfError (Format-LbfAuthFailed $r)) }
    $doc = $null
    try { $doc = ConvertFrom-LbfJson $r.Text -LastWins } catch { $doc = $null }
    $authorize = Get-LbfClaimString $doc 'authorization_endpoint'
    $token = Get-LbfClaimString $doc 'token_endpoint'
    if ($authorize.Length -eq 0) { throw (New-LbfError "${script:BrowserCredName}: ResolveEndpoints(): TenantDiscoveryResponse: authorize endpoint was not found in the openid configuration") }
    if ($token.Length -eq 0) { throw (New-LbfError "${script:BrowserCredName}: ResolveEndpoints(): TenantDiscoveryResponse: token endpoint was not found in the openid configuration") }
    return @{ Authorize = $authorize.Replace('{tenant}', $Tenant); Token = $token.Replace('{tenant}', $Tenant) }
}

# A token request; returns the response's fields, or throws azidentity's error.
function Invoke-LbfTokenRequest {
    param([string]$Endpoint, [hashtable]$Form)
    $body = $script:Utf8NoBom.GetBytes((ConvertTo-LbfAuthQueryString $Form))
    $r = Invoke-LbfAuthHttp -Method 'POST' -Uri $Endpoint -Body $body -ContentType 'application/x-www-form-urlencoded; charset=utf-8' -Accept 'application/json'
    if ($r.Status -ne 200) { throw (New-LbfError (Format-LbfAuthFailed $r)) }
    $doc = $null
    try { $doc = ConvertFrom-LbfJson $r.Text -LastWins } catch { throw (New-LbfError "${script:BrowserCredName}: $((Get-LbfException $_).Data['jsonDetail'])") }
    $err = Get-LbfClaimString $doc 'error'
    if ($err.Length -gt 0) { throw (New-LbfError "${script:BrowserCredName}: ${err}: $(Get-LbfClaimString $doc 'error_description')") }
    if ((Get-LbfClaimString $doc 'access_token').Length -eq 0) { throw (New-LbfError "${script:BrowserCredName}: response is missing access_token") }
    return , $doc
}

function Add-LbfTokenResponse {
    param([hashtable]$Credential, [string]$Scope, $Response)
    $seconds = 3600
    $raw = $null
    if ($Response.Contains('expires_in')) { $raw = $Response['expires_in'] }
    if ($null -ne $raw) { $n = 0L; if ([long]::TryParse([string]$raw, [ref]$n)) { $seconds = $n } }
    $Credential.Tokens[$Scope] = @{ Token = $Response['access_token']; ExpiresOn = [DateTime]::UtcNow.AddSeconds($seconds) }
    $rt = Get-LbfClaimString $Response 'refresh_token'
    if ($rt.Length -gt 0) {
        $Credential.RefreshToken = $rt
        if ($Credential.Persist -and $Credential.HomeAccountId.Length -gt 0) { Save-LbfLoginTokens -HomeAccountId $Credential.HomeAccountId -RefreshToken $rt }
    }
}

# MSAL's account for a token response: its home account ID and the AuthenticationRecord azidentity saves.
function Get-LbfTokenAccount {
    param($Response)
    $id = Get-LbfJwtClaims (Get-LbfClaimString $Response 'id_token')
    $homeId = ''
    $info = ConvertFrom-LbfBase64Url (Get-LbfClaimString $Response 'client_info')
    if ($null -ne $info) {
        try {
            $ci = ConvertFrom-LbfJson ($script:Utf8NoBom.GetString($info)) -LastWins
            $uid = Get-LbfClaimString $ci 'uid'
            $utid = Get-LbfClaimString $ci 'utid'
            if ($uid.Length -gt 0 -and $utid.Length -gt 0) { $homeId = "$uid.$utid" }
        } catch { $homeId = '' }
    }
    if ($homeId.Length -eq 0 -and $null -ne $id) { $homeId = "$(Get-LbfClaimString $id 'oid').$(Get-LbfClaimString $id 'tid')" }
    $issuer = Get-LbfClaimString $id 'iss'
    $authority = ''
    $tenant = Get-LbfClaimString $id 'tid'
    $u = $null
    if ([Uri]::TryCreate($issuer, [UriKind]::Absolute, [ref]$u)) {
        $authority = "$($u.Scheme)://$($u.Authority)"
        if ($tenant.Length -eq 0) { $tenant = $u.AbsolutePath.Trim('/') }
    }
    $username = Get-LbfClaimString $id 'preferred_username'
    if ($username.Length -eq 0) { $username = Get-LbfClaimString $id 'upn' }
    return @{ HomeAccountId = $homeId; Record = @{ Authority = $authority; ClientId = (Get-LbfClaimString $id 'aud'); HomeAccountId = $homeId; TenantId = $tenant; Username = $username } }
}

function Start-LbfLoopback {
    $v4 = New-Object System.Net.Sockets.TcpListener -ArgumentList ([System.Net.IPAddress]::Loopback), 0
    $v4.Start()
    $port = ([System.Net.IPEndPoint]$v4.LocalEndpoint).Port
    $listeners = New-Object System.Collections.Generic.List[object]
    $listeners.Add($v4)
    try {
        $v6 = New-Object System.Net.Sockets.TcpListener -ArgumentList ([System.Net.IPAddress]::IPv6Loopback), $port
        $v6.Start()
        $listeners.Add($v6)
    } catch { }
    return @{ Port = $port; Listeners = $listeners }
}

function Send-LbfHttpReply {
    param($Client, [int]$Status, [string]$StatusText, [string]$ContentType, [string]$Body, [string]$Extra = '')
    $bodyBytes = $script:Utf8NoBom.GetBytes($Body)
    $head = "HTTP/1.1 $Status $StatusText`r`nContent-Type: $ContentType`r`n$($Extra)Content-Length: $($bodyBytes.Length)`r`nConnection: close`r`n`r`n"
    $headBytes = [Text.Encoding]::ASCII.GetBytes($head)
    try {
        $s = $Client.GetStream()
        $s.Write($headBytes, 0, $headBytes.Length)
        $s.Write($bodyBytes, 0, $bodyBytes.Length)
        $s.Flush()
    } catch { }
}

function Send-LbfHttpError {
    param($Client, [int]$Status, [string]$StatusText, [string]$Message)
    Send-LbfHttpReply $Client $Status $StatusText 'text/plain; charset=utf-8' "$Message`n" "X-Content-Type-Options: nosniff`r`n"
    return @{ Error = $Message }
}

# MSAL's redirect handler: the result of one complete request, or $null while it is incomplete.
function Receive-LbfRedirect {
    param([hashtable]$Conn, [string]$State)
    $client = $Conn.Client
    $buf = New-Object byte[] 8192
    $s = $client.GetStream()
    $got = $false
    while ($client.Available -gt 0) {
        $n = $s.Read($buf, 0, $buf.Length)
        if ($n -le 0) { break }
        $Conn.Data.Write($buf, 0, $n)
        $got = $true
    }
    if ($got) { $Conn.Since = [DateTime]::UtcNow }
    $latin = [Text.Encoding]::GetEncoding(28591)
    $text = $latin.GetString($Conn.Data.ToArray())
    $end = $text.IndexOf("`r`n`r`n", [StringComparison]::Ordinal)
    if ($end -lt 0) { return $null }
    $lines = $text.Substring(0, $end) -split "`r`n"
    $method = ($lines[0] -split ' ')[0]
    $length = 0
    $type = ''
    foreach ($h in $lines) {
        $i = $h.IndexOf([char]':')
        if ($i -lt 0) { continue }
        $name = $h.Substring(0, $i).Trim().ToLowerInvariant()
        $value = $h.Substring($i + 1).Trim()
        if ([string]::Equals($name, 'content-length', [StringComparison]::Ordinal)) { [void][int]::TryParse($value, [ref]$length) }
        if ([string]::Equals($name, 'content-type', [StringComparison]::Ordinal)) { $type = $value }
    }
    if ($text.Length - ($end + 4) -lt $length) { return $null }
    $body = $script:Utf8NoBom.GetString($Conn.Data.ToArray(), $end + 4, $length)
    if (-not [string]::Equals($method, 'POST')) {
        Send-LbfHttpReply $client 405 'Method Not Allowed' 'text/html; charset=utf-8' $script:GetPage
        return @{ Error = 'response was received via a GET operation, which is not supported' }
    }
    $form = New-LbfMap
    if ($type.ToLowerInvariant().StartsWith('application/x-www-form-urlencoded', [StringComparison]::Ordinal)) {
        try { $form = ConvertFrom-LbfFormEncoded $body }
        catch { return Send-LbfHttpError $client 400 'Bad Request' "failed to parse form data: $(Get-LbfErrorText $_)" }
    }
    $err = [string]$form['error']
    if ($err.Length -gt 0) {
        $desc = ConvertTo-LbfHtmlEscape ([string]$form['error_description'])
        Send-LbfHttpReply $client 200 'OK' 'text/html; charset=utf-8' ($script:FailPage -f (ConvertTo-LbfHtmlEscape $err), $desc)
        return @{ Error = $desc }
    }
    $respState = [string]$form['state']
    if ($respState.Length -eq 0) { return Send-LbfHttpError $client 500 'Internal Server Error' "server didn't send OAuth state" }
    if (-not [string]::Equals($respState, $State)) { return Send-LbfHttpError $client 500 'Internal Server Error' "mismatched OAuth state, req($State), resp($respState)" }
    $code = [string]$form['code']
    if ($code.Length -eq 0) { return Send-LbfHttpError $client 500 'Internal Server Error' 'authorization code missing in response' }
    Send-LbfHttpReply $client 200 'OK' 'text/html; charset=utf-8' $script:OkPage
    return @{ Code = $code }
}

# Serves the loopback redirect until Entra posts the result or the deadline passes; polls so Ctrl+C still works.
function Wait-LbfRedirect {
    param([System.Collections.Generic.List[object]]$Listeners, [string]$State, [DateTime]$Deadline)
    $conns = New-Object System.Collections.Generic.List[hashtable]
    try {
        while ($true) {
            foreach ($l in $Listeners) {
                while ($l.Pending()) {
                    $conns.Add(@{ Client = $l.AcceptTcpClient(); Data = (New-Object System.IO.MemoryStream); Since = [DateTime]::UtcNow })
                }
            }
            for ($i = $conns.Count - 1; $i -ge 0; $i--) {
                $cn = $conns[$i]
                $result = $null
                $drop = $false
                try {
                    $result = Receive-LbfRedirect $cn $State
                    if ($null -eq $result) {
                        $closed = $cn.Client.Client.Poll(0, [System.Net.Sockets.SelectMode]::SelectRead) -and $cn.Client.Available -eq 0
                        $drop = $closed -or ([DateTime]::UtcNow - $cn.Since).TotalSeconds -gt 30
                    }
                } catch { $drop = $true }
                if ($null -ne $result) {
                    if ($result.ContainsKey('Error')) { throw (New-LbfError "${script:BrowserCredName}: $($result.Error)") }
                    return $result.Code
                }
                if ($drop) {
                    try { $cn.Client.Close() } catch { }
                    $conns.RemoveAt($i)
                }
            }
            if ([DateTime]::UtcNow -ge $Deadline) {
                $e = New-LbfError 'context deadline exceeded'
                $e.Data['lbfTimeout'] = $true
                throw $e
            }
            Start-Sleep -Milliseconds 50
        }
    } finally {
        foreach ($cn in $conns) { try { $cn.Client.Close() } catch { } }
    }
}

# pkg/browser's OpenURL, with the launcher's output kept off stdout.
function Open-LbfSignInBrowser {
    param([Parameter(Mandatory)][string]$Url)
    if ($env:LBF_TEST_BROWSER) {
        Write-LbfTextFile (Get-LbfFullPath $env:LBF_TEST_BROWSER) $Url
        return
    }
    if ($script:OnWindows) {
        try { Start-Process -FilePath $Url } catch { throw (New-LbfError "${script:BrowserCredName}: $(Get-LbfErrorText $_)") }
        return
    }
    $providers = @('open')
    if ((Get-LbfAuthGoos) -eq 'linux') { $providers = @('xdg-open', 'x-www-browser', 'www-browser') }
    $prog = $null
    foreach ($p in $providers) {
        $prog = Find-LbfExecutable $p
        if ($null -ne $prog) { break }
    }
    if ($null -eq $prog) { throw (New-LbfError "${script:BrowserCredName}: exec: $(Format-LbfQuote ($providers -join ',')): executable file not found in `$PATH") }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $prog
    $psi.Arguments = '"' + $Url + '"'
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    try { $p = [System.Diagnostics.Process]::Start($psi) } catch { throw (New-LbfError "${script:BrowserCredName}: $(Get-LbfErrorText $_)") }
    $out = $p.StandardOutput.ReadToEndAsync()
    $err = $p.StandardError.ReadToEndAsync()
    $p.WaitForExit()
    Write-LbfErr ($out.Result + $err.Result)
    if ($p.ExitCode -ne 0) { throw (New-LbfError "${script:BrowserCredName}: exit status $($p.ExitCode)") }
}

# MSAL's AcquireTokenInteractive: PKCE authorization code flow through the browser and a loopback redirect.
function Invoke-LbfBrowserSignIn {
    param([Parameter(Mandatory)][hashtable]$Credential, [Parameter(Mandatory)][string]$Scope, [Parameter(Mandatory)][DateTime]$Deadline)
    $ep = Get-LbfEntraEndpoints $Credential.Tenant
    $verifier = ConvertTo-LbfBase64Url (Get-LbfRandomBytes 32)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { $challenge = ConvertTo-LbfBase64Url ($sha.ComputeHash([Text.Encoding]::ASCII.GetBytes($verifier))) } finally { $sha.Dispose() }
    $state = [guid]::NewGuid().ToString()
    $scopes = "$Scope openid offline_access profile"
    $loop = Start-LbfLoopback
    try {
        $redirect = "http://localhost:$($loop.Port)"
        $query = ConvertTo-LbfAuthQueryString @{
            client_id = $script:AzureCliClientId; response_type = 'code'; redirect_uri = $redirect; scope = $scopes; state = $state
            code_challenge = $challenge; code_challenge_method = 'S256'; prompt = 'select_account'; response_mode = 'form_post'
        }
        $base = $ep.Authorize
        $q = $base.IndexOf([char]'?')
        if ($q -ge 0) { $base = $base.Substring(0, $q) }
        Open-LbfSignInBrowser ($base + '?' + $query)
        $code = Wait-LbfRedirect $loop.Listeners $state $Deadline
    } finally {
        foreach ($l in $loop.Listeners) { try { $l.Stop() } catch { } }
    }
    $resp = Invoke-LbfTokenRequest $ep.Token @{
        grant_type = 'authorization_code'; code = $code; code_verifier = $verifier; redirect_uri = $redirect
        client_id = $script:AzureCliClientId; client_info = '1'; scope = $scopes
    }
    $account = Get-LbfTokenAccount $resp
    $Credential.HomeAccountId = $account.HomeAccountId
    $Credential.Record = $account.Record
    Add-LbfTokenResponse $Credential $Scope $resp
}

function Invoke-LbfRefresh {
    param([hashtable]$Credential, [string]$Scope)
    $resp = Invoke-LbfTokenRequest "$(Get-LbfAuthorityHost)/$($Credential.Tenant)/oauth2/v2.0/token" @{
        grant_type = 'refresh_token'; client_id = $script:AzureCliClientId; client_info = '1'
        refresh_token = $Credential.RefreshToken; scope = "$Scope openid offline_access profile"
    }
    Add-LbfTokenResponse $Credential $Scope $resp
}

# Go's login: sign in with the browser, then remember the account where this lbf can.
function Invoke-LbfLogin {
    param([Parameter(Mandatory)][string]$Tenant)
    Assert-LbfBrowser $Tenant
    Assert-LbfTenantId $Tenant
    $timeout = Get-LbfLoginTimeout
    $cred = New-LbfBrowserCredential -Tenant $Tenant
    $canStore = $null -eq (Get-LbfTokenStoreError)
    try {
        Invoke-LbfBrowserSignIn -Credential $cred -Scope $script:ArmScope -Deadline ([DateTime]::UtcNow + $timeout)
        if ($canStore -and $cred.HomeAccountId.Length -gt 0) {
            $cred.Persist = $true
            try { Save-LbfLoginTokens -HomeAccountId $cred.HomeAccountId -RefreshToken $cred.RefreshToken }
            catch { throw (New-LbfError "${script:BrowserCredName}: $(Get-LbfErrorText $_)") }
        }
    } catch {
        if ((Get-LbfException $_).Data.Contains('lbfTimeout')) {
            throw (New-LbfError "browser sign-in timed out after $(Format-LbfAuthDuration $timeout); if no browser opened, sign in with 'az login --tenant $Tenant --use-device-code' instead")
        }
        throw (New-LbfError "browser sign-in failed: $(Get-LbfErrorText $_)")
    }
    if ($canStore) { Save-LbfAuthRecord $cred.Record }
    return $cred
}

# Go's savedLogin: the login 'lbf login' saved, if its refresh token still gets an ARM token.
function Get-LbfSavedLogin {
    param([string]$Tenant, [hashtable]$Record)
    $storeErr = Get-LbfTokenStoreError
    if ($null -ne $storeErr) { throw $storeErr }
    Assert-LbfTenantId $Tenant
    $cred = New-LbfBrowserCredential -Tenant $Tenant -SilentOnly
    $saved = Read-LbfLoginTokens
    if ($null -ne $saved -and [string]::Equals((Get-LbfClaimString $saved 'homeAccountId'), $Record.HomeAccountId) -and $Record.HomeAccountId.Length -gt 0) {
        $cred.HomeAccountId = $Record.HomeAccountId
        $cred.RefreshToken = Get-LbfClaimString $saved 'refreshToken'
        $cred.Persist = $true
    }
    [void](Get-LbfAccessToken -Credential $cred -Scope $script:ArmScope)
    $cred.SilentOnly = $false
    return $cred
}

# ---------- az login ----------

function New-LbfAzCli {
    param([string]$Tenant)
    Assert-LbfTenantId $Tenant
    return @{ Kind = 'az'; Tenant = $Tenant; Tokens = @{} }
}

function New-LbfAzFailure {
    param([string]$Message)
    $sep = '. '
    if ($Message.EndsWith('.', [StringComparison]::Ordinal)) { $sep = ' ' }
    return New-LbfError "${script:AzCredName}: $Message$sep$script:AzTroubleshoot"
}

# azidentity's shellExec: runs az through the shell and returns its stdout.
function Invoke-LbfAzCommand {
    param([Parameter(Mandatory)][string]$Command)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    if ($script:OnWindows) {
        $root = $env:SYSTEMROOT
        if ([string]::IsNullOrEmpty($root)) { throw (New-LbfError "${script:AzCredName}: environment variable `"SYSTEMROOT`" has no value") }
        $cmdExe = Find-LbfExecutable 'cmd.exe'
        if ($null -eq $cmdExe) { throw (New-LbfAzFailure 'exec: "cmd.exe": executable file not found in %PATH%') }
        $psi.FileName = $cmdExe
        $psi.Arguments = "/c $Command"
        $psi.WorkingDirectory = $root
    } else {
        $psi.FileName = '/bin/sh'
        $psi.Arguments = "-c `"$Command`""
        $psi.WorkingDirectory = '/bin'
    }
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.StandardOutputEncoding = $script:Utf8NoBom
    $psi.StandardErrorEncoding = $script:Utf8NoBom
    $p = [System.Diagnostics.Process]::Start($psi)
    try { $p.StandardInput.Close() } catch { }
    $out = $p.StandardOutput.ReadToEndAsync()
    $err = $p.StandardError.ReadToEndAsync()
    $deadline = [DateTime]::UtcNow + $script:AzTimeout
    while (-not $p.WaitForExit(100)) {
        if ([DateTime]::UtcNow -ge $deadline) {
            try { $p.Kill() } catch { }
            throw (New-LbfError "az took longer than $(Format-LbfAuthDuration $script:AzTimeout) to return a token")
        }
    }
    $p.WaitForExit()
    [void]$out.Wait(2000)
    [void]$err.Wait(2000)
    $stdout = ''
    $stderr = ''
    if ($out.IsCompleted) { $stdout = $out.Result }
    if ($err.IsCompleted) { $stderr = $err.Result }
    if ($p.ExitCode -ne 0) {
        $msg = $stderr.Trim("`r`n".ToCharArray())
        if ($p.ExitCode -eq 127 -or $msg.Contains("' is not recognized")) { throw (New-LbfError "${script:AzCredName}: executable not found on path") }
        if ($msg.Length -eq 0) { $msg = "exit status $($p.ExitCode)" }
        throw (New-LbfAzFailure $msg)
    }
    return $stdout
}

# Go's scanner error for output that starts like true, false or null but is not, or $null.
function Get-LbfJsonLiteralError {
    param([AllowEmptyString()][string]$Text)
    $s = $Text.TrimStart(" `t`r`n".ToCharArray())
    if ($s.Length -eq 0) { return $null }
    $word = @{ [char]'t' = 'true'; [char]'f' = 'false'; [char]'n' = 'null' }[$s[0]]
    if ($null -eq $word) { return $null }
    for ($i = 1; $i -lt $word.Length; $i++) {
        if ($i -ge $s.Length) { return 'unexpected end of JSON input' }
        if ($s[$i] -cne $word[$i]) {
            $c = [string]$s[$i]
            if ($c -ceq "'") { $c = "\'" }
            return "invalid character '$c' in literal $word (expecting '$($word[$i])')"
        }
    }
    return $null
}

function Get-LbfAzToken {
    param([hashtable]$Credential, [string]$Scope)
    $c = $Credential.Tokens[$Scope]
    if ($null -ne $c -and ($c.ExpiresOn - [DateTime]::UtcNow).TotalMinutes -gt 5) { return $c.Token }
    if ($Scope -cnotmatch '\A[A-Za-z0-9._/:-]*\z') { throw (New-LbfError "${script:AzCredName}.GetToken(): invalid scope $(Format-LbfQuote $Scope)") }
    $resource = $Scope
    if ($resource.EndsWith('/.default', [StringComparison]::Ordinal)) { $resource = $resource.Substring(0, $resource.Length - '/.default'.Length) }
    $out = Invoke-LbfAzCommand "az account get-access-token -o json --resource $resource --tenant $($Credential.Tenant)"
    try { $doc = ConvertFrom-LbfJson $out -LastWins }
    catch {
        $e = Get-LbfException $_
        $literal = Get-LbfJsonLiteralError $out
        if ($null -ne $literal) { throw (New-LbfError $literal) }
        if ($e.Data.Contains('jsonDetail')) { throw (New-LbfError $e.Data['jsonDetail']) }
        throw
    }
    $token = Get-LbfClaimString $doc 'accessToken'
    $epoch = New-Object DateTime 1970, 1, 1, 0, 0, 0, ([DateTimeKind]::Utc)
    $expires = $epoch
    $n = 0L
    if ($doc.Contains('expires_on') -and [long]::TryParse([string]$doc['expires_on'], [ref]$n) -and $n -ne 0) {
        $expires = $epoch.AddSeconds($n)
    } else {
        $text = Get-LbfClaimString $doc 'expiresOn'
        $local = [DateTime]::MinValue
        if (-not [DateTime]::TryParseExact($text, 'yyyy-MM-dd HH:mm:ss.FFFFFF', $script:Invariant, [Globalization.DateTimeStyles]::AssumeLocal, [ref]$local)) {
            throw (New-LbfError "${script:AzCredName}: error parsing token expiration time $(Format-LbfQuote $text): parsing time $(Format-LbfQuote $text) as `"2006-01-02 15:04:05.999999`": cannot parse")
        }
        $expires = $local.ToUniversalTime()
    }
    $Credential.Tokens[$Scope] = @{ Token = $token; ExpiresOn = $expires }
    return $token
}

# Who az login signs in as for the tenant, or $null.
function Get-LbfAzCliUser {
    param([string]$Tenant)
    try {
        $cli = New-LbfAzCli $Tenant
        return Get-LbfSignedInUser -Credential $cli
    } catch { return $null }
}

# ---------- credentials ----------

function Get-LbfAccessToken {
    param([Parameter(Mandatory)][hashtable]$Credential, [Parameter(Mandatory)][string]$Scope)
    if ($Credential.Kind -eq 'az') { return Get-LbfAzToken $Credential $Scope }
    $c = $Credential.Tokens[$Scope]
    if ($null -ne $c -and ($c.ExpiresOn - [DateTime]::UtcNow).TotalMinutes -gt 5) { return $c.Token }
    $ok = $false
    if ($Credential.RefreshToken.Length -gt 0) {
        try { Invoke-LbfRefresh $Credential $Scope; $ok = $true } catch { $ok = $false }
    }
    if (-not $ok) {
        if ($Credential.SilentOnly -or (Get-LbfBrowserUnavailable) -ne '') { throw (New-LbfError $script:AuthRequired) }
        $timeout = Get-LbfLoginTimeout
        try { Invoke-LbfBrowserSignIn -Credential $Credential -Scope $Scope -Deadline ([DateTime]::UtcNow + $timeout) }
        catch {
            if ((Get-LbfException $_).Data.Contains('lbfTimeout')) {
                throw (New-LbfError "browser sign-in timed out after $(Format-LbfAuthDuration $timeout); if no browser opened, sign in with 'az login --tenant $($Credential.Tenant) --use-device-code' instead")
            }
            throw
        }
    }
    return $Credential.Tokens[$Scope].Token
}

function Get-LbfSignedInUser {
    param([Parameter(Mandatory)][hashtable]$Credential)
    $claims = Get-LbfJwtClaims (Get-LbfAccessToken -Credential $Credential -Scope $script:ArmScope)
    foreach ($k in @('upn', 'email', 'preferred_username', 'unique_name')) {
        $v = Get-LbfClaimString $claims $k
        if ($v.Length -gt 0) { return $v }
    }
    return 'unknown'
}

# Go's credential: the saved login, then az login, then the browser. Returns Credential and Source.
function Get-LbfCredential {
    param([Parameter(Mandatory)][string]$Tenant)
    $rec = Read-LbfAuthRecord
    if ($null -ne $rec) {
        try {
            $cred = Get-LbfSavedLogin $Tenant $rec
            return @{ Credential = $cred; Source = 'lbf login' }
        } catch {
            $who = $rec.Username
            if ($who.Length -eq 0) { $who = 'you' }
            Write-LbfLog "[auth] cannot use the login 'lbf login' saved for ${who}: $(Get-LbfErrorText $_)`n"
        }
    }
    if ($null -ne (Find-LbfExecutable 'az')) {
        $cli = New-LbfAzCli $Tenant
        $failure = $null
        try { [void](Get-LbfAzToken $cli $script:ArmScope) } catch { $failure = Get-LbfErrorText $_ }
        if ($null -eq $failure) { return @{ Credential = $cli; Source = 'az login' } }
        if (-not $failure.Contains($script:AzNotSignedIn)) {
            throw (New-LbfError "could not sign in through az login: $failure`nIf that login has expired or is for another tenant, run 'az login --tenant $Tenant' ('az login --tenant $Tenant --use-device-code' over SSH or on HPC)")
        }
    }
    Assert-LbfBrowser $Tenant
    Write-LbfLog "[auth] not signed in; opening your browser to sign in`n"
    $cred = Invoke-LbfLogin -Tenant $Tenant
    return @{ Credential = $cred; Source = 'browser' }
}

# ---------- finding and choosing the storage account ----------

function Invoke-LbfArmGet {
    param([hashtable]$Credential, [string]$Uri)
    $token = Get-LbfAccessToken -Credential $Credential -Scope $script:ArmScope
    $r = Invoke-LbfAuthHttp -Method 'GET' -Uri $Uri -Headers @{ Authorization = "Bearer $token" } -Accept 'application/json'
    if ($r.Status -ne 200) { throw (New-LbfError (Format-LbfAzureResponseError $r)) }
    try { $doc = ConvertFrom-LbfJson $r.Text -LastWins }
    catch { throw (New-LbfError "unmarshalling type: $((Get-LbfException $_).Data['jsonDetail'])") }
    return , $doc
}

function Get-LbfArmPage {
    param($Doc)
    $items = @()
    if ($Doc -is [System.Collections.Specialized.OrderedDictionary] -and $Doc.Contains('value') -and $null -ne $Doc['value']) { $items = @($Doc['value']) }
    return @{ Items = $items; Next = (Get-LbfClaimString $Doc 'nextLink') }
}

# Go's findAccounts: every storage account carrying the tag in every enabled subscription the user can see.
function Find-LbfAccounts {
    param([Parameter(Mandatory)][hashtable]$Credential, [Parameter(Mandatory)][AllowEmptyString()][string]$Tag)
    $i = $Tag.IndexOf([char]'=')
    if ($i -lt 1) { throw (New-LbfError "--tag must be KEY=VALUE, got $(Format-LbfQuote $Tag)") }
    $key = $Tag.Substring(0, $i)
    $value = $Tag.Substring($i + 1)
    $arm = Get-LbfArmEndpoint
    $found = New-Object System.Collections.Generic.List[hashtable]
    $next = "$arm/subscriptions?api-version=2022-12-01"
    while ($next.Length -gt 0) {
        try { $page = Get-LbfArmPage (Invoke-LbfArmGet $Credential $next) }
        catch { throw (New-LbfError "listing subscriptions failed (an access or connectivity problem, not a tagging problem): $(Get-LbfErrorText $_)") }
        foreach ($sub in $page.Items) {
            if (-not [string]::Equals((Get-LbfClaimString $sub 'state'), 'Enabled')) { continue }
            $subId = Get-LbfClaimString $sub 'subscriptionId'
            $subName = Get-LbfClaimString $sub 'displayName'
            if ($subId.Length -eq 0) { throw (New-LbfError 'parameter subscriptionID cannot be empty') }
            $accNext = "$arm/subscriptions/$([Uri]::EscapeDataString($subId))/providers/Microsoft.Storage/storageAccounts?api-version=2024-01-01"
            while ($accNext.Length -gt 0) {
                try { $accPage = Get-LbfArmPage (Invoke-LbfArmGet $Credential $accNext) }
                catch { throw (New-LbfError "listing storage accounts in $subName failed: $(Get-LbfErrorText $_)") }
                foreach ($acc in $accPage.Items) {
                    $tags = $null
                    if ($acc -is [System.Collections.Specialized.OrderedDictionary] -and $acc.Contains('tags')) { $tags = $acc['tags'] }
                    if ($tags -isnot [System.Collections.Specialized.OrderedDictionary] -or -not $tags.Contains($key)) { continue }
                    $v = $tags[$key]
                    if ($v -isnot [string] -or -not [string]::Equals($v, $value)) { continue }
                    $found.Add(@{ Name = (Get-LbfClaimString $acc 'name'); Location = (Get-LbfClaimString $acc 'location'); SubscriptionName = $subName; SubscriptionID = $subId })
                }
                $accNext = $accPage.Next
            }
        }
        $next = $page.Next
    }
    return , $found.ToArray()
}

function Format-LbfPadRunes {
    param([AllowEmptyString()][string]$Text, [int]$Width)
    $n = (Get-LbfRunes $Text).Length
    if ($n -ge $Width) { return $Text }
    return $Text + (' ' * ($Width - $n))
}

# Go's accountLabels: name, subscription and location in columns.
function Get-LbfAccountLabels {
    param([object[]]$Accounts)
    $nameW = 0
    $subW = 0
    foreach ($a in $Accounts) {
        $nameW = [Math]::Max($nameW, (Get-LbfUtf8Length $a.Name))
        $subW = [Math]::Max($subW, (Get-LbfUtf8Length $a.SubscriptionName))
    }
    $labels = New-Object System.Collections.Generic.List[string]
    foreach ($a in $Accounts) { $labels.Add((Format-LbfPadRunes $a.Name $nameW) + '  ' + (Format-LbfPadRunes $a.SubscriptionName $subW) + '  ' + $a.Location) }
    return , $labels.ToArray()
}

# Go's chooseAccount. -Picker is a scriptblock (prompt, labels) -> index, or $null when nobody can answer; it defaults to the terminal.
function Select-LbfAccount {
    param([AllowEmptyCollection()][AllowNull()][object[]]$Matches, [AllowEmptyString()][string]$Tag, [AllowEmptyString()][string]$Want = '', [AllowNull()][scriptblock]$Picker)
    $list = @()
    if ($null -ne $Matches) { $list = @($Matches) }
    if ($list.Count -eq 0) { throw (New-LbfError "no storage account tagged $(Format-LbfQuote $Tag) is visible to you; ask your Azure administrator to apply it") }
    $sorted = New-Object System.Collections.Generic.List[object]
    foreach ($m in $list) {
        $at = $sorted.Count
        while ($at -gt 0 -and (Compare-LbfUtf8 ([string]$sorted[$at - 1].Name) ([string]$m.Name)) -gt 0) { $at-- }
        $sorted.Insert($at, $m)
    }
    $labels = Get-LbfAccountLabels $sorted
    $choices = '  ' + ($labels -join "`n  ") + "`nChoose one by adding one of:"
    foreach ($m in $sorted) { $choices += "`n  --account $($m.Name)" }
    if ($Want.Length -gt 0) {
        foreach ($m in $sorted) { if ([string]::Equals([string]$m.Name, $Want, [StringComparison]::Ordinal)) { return $m } }
        throw (New-LbfError "no storage account named $(Format-LbfQuote $Want) is tagged $(Format-LbfQuote $Tag); those that are:`n$choices")
    }
    if ($sorted.Count -eq 1) { return $sorted[0] }
    $pick = $Picker
    if (-not $PSBoundParameters.ContainsKey('Picker')) { $pick = Get-LbfTerminalPicker }
    if ($null -eq $pick) { throw (New-LbfError "$($sorted.Count) storage accounts are tagged $(Format-LbfQuote $Tag):`n$choices") }
    $i = [int](& $pick "$($sorted.Count) storage accounts are tagged $(Format-LbfQuote $Tag). Choose one" $labels)
    Write-LbfLog "Tip: skip this question next time with --account $($sorted[$i].Name)`n"
    return $sorted[$i]
}

# ---------- the terminal picker ----------

function Write-LbfPickText {
    param($Out, [AllowEmptyString()][string]$Text)
    if ($null -eq $Out) { Write-LbfErr $Text } else { [void]$Out.Append($Text) }
}

function Write-LbfPickFrame {
    param([hashtable]$S, $Out, [string[]]$Labels, [bool]$Numbered)
    $esc = [char]27
    $width = 80
    if ($null -eq $Out) { $width = (Get-LbfTermSize)[0] }
    $sb = New-Object System.Text.StringBuilder
    if ($S.drawn -gt 0) { [void]$sb.Append("$esc[$($S.drawn)A") }
    for ($i = 0; $i -lt $Labels.Count; $i++) {
        $mark = '( )'
        if ($i -eq $S.cur) { $mark = '(*)' }
        $l = $Labels[$i]
        if ($Numbered) { $l = "$($i + 1). $l" }
        [void]$sb.Append("`r$esc[2K" + (Format-LbfFit "  $mark $l" ($width - 1)) + "`r`n")
    }
    $S.drawn = $Labels.Count
    if ($S.stray) {
        $hint = '  Use the arrow keys and Enter'
        if ($Numbered) { $hint += ", or type its number (1-$($Labels.Count))" }
        [void]$sb.Append("`r$esc[2K" + (Format-LbfFit ($hint + '; q to cancel') ($width - 1)) + "`r`n")
        $S.drawn++
    }
    [void]$sb.Append("$esc[J")
    Write-LbfPickText $Out $sb.ToString()
}

function Move-LbfPickCursor {
    param([hashtable]$S, [int]$Key, [int]$Count)
    switch ($Key) {
        { $_ -eq 0x41 -or $_ -eq 0x6B } { $S.cur = [Math]::Max($S.cur - 1, 0); $S.stray = $false; return }
        { $_ -eq 0x42 -or $_ -eq 0x6A } { $S.cur = [Math]::Min($S.cur + 1, $Count - 1); $S.stray = $false; return }
        { $_ -eq 0x43 -or $_ -eq 0x44 } { return }
        default { $S.stray = $true }
    }
}

# Go's pickArrows. -Read returns the next chunk of typed text, or $null at the end of input.
function Invoke-LbfPickArrows {
    param([Parameter(Mandatory)][scriptblock]$Read, $Out, [string]$Prompt, [string[]]$Labels)
    $numbered = $Labels.Count -le 9
    $S = @{ cur = 0; stray = $false; drawn = 0 }
    $how = 'arrow keys and Enter'
    if ($numbered) { $how += ', or its number' }
    Write-LbfPickText $Out "$Prompt ($how; q to cancel)`r`n"
    Write-LbfPickFrame $S $Out $Labels $numbered
    $state = 'plain'
    while ($true) {
        $chunk = & $Read
        if ($null -eq $chunk) { throw (New-LbfError $script:ErrNoChoice) }
        foreach ($b in $script:Utf8NoBom.GetBytes([string]$chunk)) {
            $k = [int]$b
            if ($state -eq 'esc') {
                $state = 'plain'
                if ($k -eq 0x5B) { $state = 'csi'; continue }
                if ($k -eq 0x4F) { $state = 'ss3'; continue }
            } elseif ($state -eq 'csi') {
                if ($k -ge 0x20 -and $k -le 0x3F) { continue }
                $state = 'plain'
                Move-LbfPickCursor $S $k $Labels.Count
                continue
            } elseif ($state -eq 'ss3') {
                $state = 'plain'
                Move-LbfPickCursor $S $k $Labels.Count
                continue
            }
            if ($k -eq 0x0D -or $k -eq 0x0A) {
                if (-not $S.stray) { return $S.cur }
            } elseif ($k -eq 3 -or $k -eq 0x71) {
                throw (New-LbfError $script:ErrNoChoice)
            } elseif ($k -eq 0x1B) {
                $state = 'esc'
            } elseif ($k -eq 0x6B -or $k -eq 0x6A) {
                Move-LbfPickCursor $S $k $Labels.Count
            } elseif ($numbered -and $k -ge 0x31 -and ($k - 0x30) -le $Labels.Count) {
                return $k - 0x31
            } else {
                $S.stray = $true
            }
        }
        Write-LbfPickFrame $S $Out $Labels $numbered
    }
}

# Go's pickNumbered. -ReadLine returns the next line without its line ending, or $null at the end of input.
function Invoke-LbfPickNumbered {
    param([Parameter(Mandatory)][scriptblock]$ReadLine, $Out, [string]$Prompt, [string[]]$Labels)
    Write-LbfPickText $Out "$Prompt`n"
    for ($i = 0; $i -lt $Labels.Count; $i++) { Write-LbfPickText $Out "  $($i + 1)) $($Labels[$i])`n" }
    while ($true) {
        Write-LbfPickText $Out "Choose [1-$($Labels.Count)]: "
        $line = & $ReadLine
        if ($null -eq $line) {
            Write-LbfPickText $Out "`n"
            throw (New-LbfError $script:ErrNoChoice)
        }
        $n = 0
        if ($line.Trim() -cmatch '^[+-]?[0-9]+$' -and [int]::TryParse($line.Trim(), [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$n) -and $n -ge 1 -and $n -le $Labels.Count) {
            return $n - 1
        }
    }
}

# One key from the console, as the bytes a raw terminal would send for it.
function Read-LbfConsoleKey {
    $esc = [char]27
    $k = [Console]::ReadKey($true)
    switch ([string]$k.Key) {
        'UpArrow' { return "$esc[A" }
        'DownArrow' { return "$esc[B" }
        'RightArrow' { return "$esc[C" }
        'LeftArrow' { return "$esc[D" }
        'Escape' { return [string]$esc }
    }
    if ($k.KeyChar -ne [char]0) { return [string]$k.KeyChar }
    return "$esc[~"
}

function Invoke-LbfConsolePick {
    param([string]$Prompt, [string[]]$Labels)
    $vt = $env:TERM -ne 'dumb'
    if ($vt -and $script:OnWindows) {
        try { $vt = [bool]$Host.UI.SupportsVirtualTerminal } catch { $vt = $false }
    }
    if ($vt) {
        $saved = [Console]::TreatControlCAsInput
        [Console]::TreatControlCAsInput = $true
        try { return Invoke-LbfPickArrows -Read { Read-LbfConsoleKey } -Out $null -Prompt $Prompt -Labels $Labels }
        finally { [Console]::TreatControlCAsInput = $saved }
    }
    return Invoke-LbfPickNumbered -ReadLine { [Console]::In.ReadLine() } -Out $null -Prompt $Prompt -Labels $Labels
}

# Go's terminalPicker: $null when nobody is at a terminal to answer.
function Get-LbfTerminalPicker {
    if ([Console]::IsInputRedirected -or [Console]::IsErrorRedirected) { return $null }
    return { param($Prompt, $Labels) Invoke-LbfConsolePick -Prompt $Prompt -Labels $Labels }
}

# ---------- user delegation SAS ----------

function Format-LbfSasTime {
    param([Parameter(Mandatory)][DateTime]$Time)
    return $Time.ToString("yyyy-MM-dd'T'HH:mm:ss", $script:Invariant) + 'Z'
}

function Get-LbfXmlText {
    param([xml]$Doc, [string]$Name, [switch]$Optional)
    $n = $Doc.SelectSingleNode("/UserDelegationKey/$Name")
    if ($null -eq $n) {
        if ($Optional) { return '' }
        throw (New-LbfError "the user delegation key has no $Name")
    }
    return $n.InnerText
}

function Get-LbfUserDelegationKey {
    param([hashtable]$Credential, [string]$Account, [DateTime]$Start, [DateTime]$Expiry)
    $token = Get-LbfAccessToken -Credential $Credential -Scope $script:StorageScope
    $body = '<?xml version="1.0" encoding="UTF-8"?>' + "`n" + "<KeyInfo><Expiry>$(Format-LbfSasTime $Expiry)</Expiry><Start>$(Format-LbfSasTime $Start)</Start></KeyInfo>"
    $r = Invoke-LbfAuthHttp -Method 'POST' -Uri "$(Get-LbfBlobServiceUrl $Account)/?restype=service&comp=userdelegationkey" `
        -Headers @{ Authorization = "Bearer $token"; 'x-ms-version' = $script:BlobServiceVersion } `
        -Body ($script:Utf8NoBom.GetBytes($body)) -ContentType 'application/xml' -Accept 'application/xml'
    if ($r.Status -ne 200) { throw (New-LbfError (Format-LbfAzureResponseError $r)) }
    [xml]$doc = $r.Text.TrimStart([char]0xFEFF)
    $key = @{}
    foreach ($n in @('SignedOid', 'SignedTid', 'SignedService', 'SignedVersion', 'Value')) { $key[$n] = Get-LbfXmlText $doc $n }
    $key['SignedDelegatedUserTid'] = Get-LbfXmlText $doc 'SignedDelegatedUserTid' -Optional
    foreach ($n in @('SignedStart', 'SignedExpiry')) {
        $t = [DateTimeOffset]::Parse((Get-LbfXmlText $doc $n), $script:Invariant, [Globalization.DateTimeStyles]::AssumeUniversal)
        $key[$n] = Format-LbfSasTime $t.DateTime
    }
    return $key
}

# The SAS azblob's BlobSignatureValues.SignWithUserDelegation encodes for a container.
function New-LbfUserDelegationSas {
    param([string]$Account, [string]$Container, [string]$Permissions, [DateTime]$Start, [DateTime]$Expiry, [hashtable]$Key)
    $st = Format-LbfSasTime $Start
    $se = Format-LbfSasTime $Expiry
    $sts = @(
        $Permissions, $st, $se, "/blob/$Account/$Container", $Key.SignedOid, $Key.SignedTid, $Key.SignedStart, $Key.SignedExpiry,
        $Key.SignedService, $Key.SignedVersion, '', '', '', $Key.SignedDelegatedUserTid, '', '', 'https', $script:BlobServiceVersion, 'c',
        '', '', '', '', '', '', '', '', ''
    ) -join "`n"
    $hmac = New-Object System.Security.Cryptography.HMACSHA256 -ArgumentList (, [Convert]::FromBase64String($Key.Value))
    try { $sig = [Convert]::ToBase64String($hmac.ComputeHash($script:Utf8NoBom.GetBytes($sts))) } finally { $hmac.Dispose() }
    $q = @{
        sv = $script:BlobServiceVersion; spr = 'https'; st = $st; se = $se; sr = 'c'; sp = $Permissions
        skoid = $Key.SignedOid; sktid = $Key.SignedTid; skt = $Key.SignedStart; ske = $Key.SignedExpiry; sks = $Key.SignedService; skv = $Key.SignedVersion
        sig = $sig
    }
    if ($Key.SignedDelegatedUserTid.Length -gt 0) { $q['skdutid'] = $Key.SignedDelegatedUserTid }
    return ConvertTo-LbfAuthQueryString $q
}

# Go's mintTarget: sign in, find the tagged account, and sign a user delegation SAS for the container.
function Get-LbfMintedTarget {
    param([Parameter(Mandatory)][string]$Tenant, [AllowEmptyString()][string]$Tag, [AllowEmptyString()][string]$AccountName,
        [AllowEmptyString()][string]$ContainerName, [Parameter(Mandatory)][string]$Mode)
    try {
        Set-LbfStatus 'Signing in'
        $signedIn = Get-LbfCredential -Tenant $Tenant
        $cred = $signedIn.Credential
        $user = Get-LbfSignedInUser -Credential $cred

        Set-LbfStatus "Finding the storage account tagged $Tag"
        $found = Find-LbfAccounts -Credential $cred -Tag $Tag
        Set-LbfStatus ''
        $chosen = Select-LbfAccount -Matches $found -Tag $Tag -Want $AccountName
        $account = $chosen.Name

        Set-LbfStatus "Getting a $Mode key for $account"
        $t = [DateTime]::UtcNow
        $now = New-Object DateTime ($t.Ticks - ($t.Ticks % [TimeSpan]::TicksPerSecond)), ([DateTimeKind]::Utc)
        $start = $now.AddMinutes(-15)
        $expiry = $now.Add($script:SasLifetime)
        try { $key = Get-LbfUserDelegationKey -Credential $cred -Account $account -Start $start -Expiry $expiry }
        catch { throw (New-LbfError "getting a user delegation key for $account needs the 'Storage Blob Delegator' role at storage account scope: $(Get-LbfErrorText $_)") }

        $perms = 'rl'
        if ([string]::Equals($Mode, 'upload', [StringComparison]::Ordinal)) { $perms = 'rwl' }
        $sas = New-LbfUserDelegationSas -Account $account -Container $ContainerName -Permissions $perms -Start $start -Expiry $expiry -Key $key

        Write-LbfLog "[auth] $user via $($signedIn.Source) -> $account/$ContainerName (subscription $($chosen.SubscriptionName), requested=$perms)`n"
        $endpoint = ''
        if ($env:LBF_TEST_BLOB_SERVICE) { $endpoint = Get-LbfBlobServiceUrl $account }
        return New-LbfTarget -Account $account -Container $ContainerName -SAS $sas -Expiry (New-Object DateTimeOffset $expiry, ([TimeSpan]::Zero)) `
            -Permissions $perms -User $user -SubscriptionName $chosen.SubscriptionName -SubscriptionID $chosen.SubscriptionID `
            -Source $signedIn.Source -Endpoint $endpoint
    } finally {
        Set-LbfStatus ''
    }
}
