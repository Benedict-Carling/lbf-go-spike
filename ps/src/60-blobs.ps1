$script:BlobApiVersion = '2026-12-06'
$script:BlobMaxRetries = 3
$script:BlobRetryDelayMs = 800
$script:BlobMaxRetryDelayMs = 60000
$script:BlobRetryStatus = @(408, 429, 500, 502, 503, 504)
$script:BlobCheckCodes = @('AuthorizationPermissionMismatch', 'AuthorizationFailure', 'InsufficientAccountPermissions')
$script:LbfHttpClient = $null
$script:LbfCancel = $null
$script:LbfInterrupted = $false
$script:LbfCtrlCWatched = $false
$script:LbfJitter = New-Object System.Random

function Get-LbfHttpClient {
    if ($null -eq $script:LbfHttpClient) {
        Add-Type -AssemblyName System.Net.Http
        try {
            # .NET Framework allows two connections per host and may not offer TLS 1.2 unless asked.
            [Net.ServicePointManager]::DefaultConnectionLimit = [Math]::Max([Net.ServicePointManager]::DefaultConnectionLimit, 128)
            [Net.ServicePointManager]::Expect100Continue = $false
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        } catch { }
        $handler = New-Object System.Net.Http.HttpClientHandler
        $handler.AllowAutoRedirect = $false
        $handler.UseCookies = $false
        $client = New-Object System.Net.Http.HttpClient ($handler)
        $client.Timeout = [Threading.Timeout]::InfiniteTimeSpan
        $script:LbfHttpClient = $client
    }
    return $script:LbfHttpClient
}

# A ServicePoint made before the default limit was raised keeps .NET Framework's two connections per host.
function Initialize-LbfBlobAccess {
    param([Parameter(Mandatory)][hashtable]$Target)
    [void](Get-LbfHttpClient)
    try {
        $sp = [Net.ServicePointManager]::FindServicePoint([Uri](Get-LbfContainerUrl $Target))
        if ($sp.ConnectionLimit -lt 128) { $sp.ConnectionLimit = 128 }
    } catch { }
}

function Get-LbfCancelToken {
    if ($null -eq $script:LbfCancel) { $script:LbfCancel = New-Object System.Threading.CancellationTokenSource }
    return $script:LbfCancel.Token
}

# In a console, Ctrl-C arrives as a key so a transfer can stop cleanly as Go's does; a second Ctrl-C is PowerShell's own.
function Enter-LbfInterruptible {
    $script:LbfCtrlCWatched = $false
    try {
        if ($Host.Name -eq 'ConsoleHost' -and -not [Console]::IsInputRedirected) {
            [Console]::TreatControlCAsInput = $true
            $script:LbfCtrlCWatched = $true
        }
    } catch { $script:LbfCtrlCWatched = $false }
}

function Exit-LbfInterruptible {
    if ($script:LbfCtrlCWatched) {
        try { [Console]::TreatControlCAsInput = $false } catch { }
        $script:LbfCtrlCWatched = $false
    }
}

function Test-LbfCtrlC {
    if (-not $script:LbfCtrlCWatched) { return }
    try {
        while ([Console]::KeyAvailable) {
            $k = [Console]::ReadKey($true)
            if ($k.Key -eq [ConsoleKey]::C -and ($k.Modifiers -band [ConsoleModifiers]::Control)) {
                Exit-LbfInterruptible
                Invoke-LbfInterrupt
                return
            }
        }
    } catch { }
}

function Invoke-LbfInterrupt {
    $script:LbfInterrupted = $true
    [void](Get-LbfCancelToken)
    $script:LbfCancel.Cancel()
}

function Test-LbfInterrupted {
    return $script:LbfInterrupted
}

# Waits until one of the tasks finishes, at most Ms milliseconds, noticing Ctrl-C meanwhile.
function Wait-LbfTasks {
    param([System.Collections.Generic.List[object]]$Tasks, [int]$Ms = 100)
    Test-LbfCtrlC
    if ($Ms -lt 1) { $Ms = 1 }
    if ($null -ne $Tasks -and $Tasks.Count -gt 0) {
        $arr = New-Object 'System.Threading.Tasks.Task[]' $Tasks.Count
        for ($i = 0; $i -lt $Tasks.Count; $i++) { $arr[$i] = $Tasks[$i] }
        try { [void][System.Threading.Tasks.Task]::WaitAny($arr, $Ms) } catch { }
    } else {
        [System.Threading.Thread]::Sleep($Ms)
    }
}

# ---------- URLs as the Go SDK builds them ----------

# Each byte's text in Go's url.PathEscape and url.QueryEscape.
$script:PathEscapes = New-Object string[] 256
$script:QueryEscapes = New-Object string[] 256
for ($i = 0; $i -lt 256; $i++) {
    $c = [char]$i
    $unreserved = ($i -ge 0x61 -and $i -le 0x7A) -or ($i -ge 0x41 -and $i -le 0x5A) -or ($i -ge 0x30 -and $i -le 0x39) -or '-_.~'.IndexOf($c) -ge 0
    $hex = '%' + $i.ToString('X2')
    if ($unreserved -or '$&+:=@'.IndexOf($c) -ge 0) { $script:PathEscapes[$i] = [string]$c } else { $script:PathEscapes[$i] = $hex }
    if ($unreserved) { $script:QueryEscapes[$i] = [string]$c } elseif ($i -eq 0x20) { $script:QueryEscapes[$i] = '+' } else { $script:QueryEscapes[$i] = $hex }
}
$script:UnreservedText = New-Object System.Text.RegularExpressions.Regex ('\A[A-Za-z0-9._~-]*\z')

function ConvertTo-LbfEscaped {
    param([string]$Text, [string[]]$Table)
    if ($script:UnreservedText.IsMatch($Text)) { return $Text }
    $sb = New-Object System.Text.StringBuilder ($Text.Length * 3)
    foreach ($b in $script:Utf8NoBom.GetBytes($Text)) { [void]$sb.Append($Table[$b]) }
    return $sb.ToString()
}

# Go's url.PathEscape.
function ConvertTo-LbfPathEscape {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    return ConvertTo-LbfEscaped $Text $script:PathEscapes
}

# Go's url.QueryEscape.
function ConvertTo-LbfQueryEscape {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    return ConvertTo-LbfEscaped $Text $script:QueryEscapes
}

# Go's url.QueryUnescape, or $null for a malformed escape.
function ConvertFrom-LbfQueryEscape {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text, [switch]$Path)
    $bytes = New-Object System.Collections.Generic.List[byte]
    for ($i = 0; $i -lt $Text.Length; $i++) {
        $c = $Text[$i]
        if ($c -eq '%') {
            if ($i + 2 -ge $Text.Length) { return $null }
            $hex = $Text.Substring($i + 1, 2)
            if ($hex -notmatch '^[0-9A-Fa-f]{2}$') { return $null }
            $bytes.Add([Convert]::ToByte($hex, 16))
            $i += 2
        } elseif ($c -eq '+' -and -not $Path) {
            $bytes.Add(0x20)
        } else {
            foreach ($b in $script:Utf8NoBom.GetBytes([string]$c)) { $bytes.Add($b) }
        }
    }
    return $script:Utf8NoBom.GetString($bytes.ToArray())
}

$script:SasPairCache = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)

# The SAS's parameters as url.ParseQuery reads them, each re-escaped as url.Values.Encode writes it; a key given twice keeps its values together.
function Get-LbfSasPairs {
    param([AllowEmptyString()][string]$Sas)
    $cached = $null
    if ($script:SasPairCache.TryGetValue($Sas, [ref]$cached)) { return , $cached }
    $byKey = New-Object 'System.Collections.Generic.Dictionary[string,System.Collections.Generic.List[string]]' ([StringComparer]::Ordinal)
    $order = New-Object System.Collections.Generic.List[string]
    foreach ($part in ($Sas -split '&')) {
        if ($part -eq '') { continue }
        $k = $part
        $v = ''
        $eq = $part.IndexOf('=')
        if ($eq -ge 0) { $k = $part.Substring(0, $eq); $v = $part.Substring($eq + 1) }
        $k = ConvertFrom-LbfQueryEscape $k
        $v = ConvertFrom-LbfQueryEscape $v
        if ($null -eq $k -or $null -eq $v) { continue }
        if (-not $byKey.ContainsKey($k)) { $byKey[$k] = New-Object System.Collections.Generic.List[string]; $order.Add($k) }
        $byKey[$k].Add((ConvertTo-LbfQueryEscape $k) + '=' + (ConvertTo-LbfQueryEscape $v))
    }
    $pairs = New-Object System.Collections.Generic.List[object]
    foreach ($k in $order) { $pairs.Add([object[]]@($k, ($byKey[$k] -join '&'))) }
    $script:SasPairCache[$Sas] = $pairs.ToArray()
    return , $script:SasPairCache[$Sas]
}

# A request as the Go SDK sends it: the SAS and the operation's parameters, sorted and escaped as url.Values.Encode does.
function New-LbfBlobRequest {
    param([Parameter(Mandatory)][hashtable]$Target, [AllowNull()][string]$Blob, [string]$Method = 'GET', [object[]]$Params = @(),
        [hashtable]$Headers = @{}, [byte[]]$Body = $null, [int]$Offset = 0, [int]$Count = -1, [string]$ContentType = '', [switch]$Stream)
    $display = Get-LbfContainerUrl $Target
    if ($null -ne $Blob -and $Blob -ne '') { $display += '/' + (ConvertTo-LbfPathEscape $Blob) }
    $query = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    foreach ($kv in (Get-LbfSasPairs $Target.SAS)) { $query[$kv[0]] = $kv[1] }
    for ($i = 0; $i + 1 -lt $Params.Count; $i += 2) {
        $k = [string]$Params[$i]
        $query[$k] = (ConvertTo-LbfQueryEscape $k) + '=' + (ConvertTo-LbfQueryEscape ([string]$Params[$i + 1]))
    }
    $keys = New-Object string[] $query.Count
    $query.Keys.CopyTo($keys, 0)
    [Array]::Sort($keys, [StringComparer]::Ordinal)
    $pairs = New-Object string[] $keys.Length
    for ($i = 0; $i -lt $keys.Length; $i++) { $pairs[$i] = $query[$keys[$i]] }
    $q = ($pairs -join '&').Replace('+', '%20')
    $url = $display
    if ($q -ne '') { $url += '?' + $q }
    if ($null -ne $Body -and $Count -lt 0) { $Count = $Body.Length - $Offset }
    return @{
        Method = $Method; Url = $url; Display = $display; Headers = $Headers; Body = $Body; Offset = $Offset; Count = $Count
        ContentType = $ContentType; Stream = [bool]$Stream
    }
}

# ---------- Sending with azcore's retry policy ----------

function New-LbfBlobOp {
    param([Parameter(Mandatory)][hashtable]$Request, $Token = $null)
    if ($null -eq $Token) { $Token = Get-LbfCancelToken }
    return @{ Request = $Request; Token = $Token; Try = 1; Task = $null; Message = $null; NotBefore = [DateTime]::MinValue; Done = $false; Result = $null; Error = $null }
}

function Start-LbfBlobSend {
    param([hashtable]$Op)
    $r = $Op.Request
    $m = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($r.Method), [System.Uri]::new($r.Url))
    [void]$m.Headers.TryAddWithoutValidation('x-ms-version', $script:BlobApiVersion)
    foreach ($k in $r.Headers.Keys) { [void]$m.Headers.TryAddWithoutValidation([string]$k, [string]$r.Headers[$k]) }
    if ($null -ne $r.Body) {
        $content = [System.Net.Http.ByteArrayContent]::new($r.Body, $r.Offset, $r.Count)
        if ($r.ContentType -ne '') { [void]$content.Headers.TryAddWithoutValidation('Content-Type', $r.ContentType) }
        $m.Content = $content
    }
    $how = [System.Net.Http.HttpCompletionOption]::ResponseContentRead
    if ($r.Stream) { $how = [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead }
    $Op.Message = $m
    $Op.Task = (Get-LbfHttpClient).SendAsync($m, $how, $Op.Token)
}

# azcore's calcDelay: (2^try - 1) * 800ms, jittered by 0.8 to 1.3, at most a minute.
function Get-LbfRetryDelayMs {
    param([int]$Try)
    $d = ([Math]::Pow(2, $Try) - 1) * $script:BlobRetryDelayMs * ($script:LbfJitter.NextDouble() / 2 + 0.8)
    return [Math]::Min($d, $script:BlobMaxRetryDelayMs)
}

function Get-LbfRetryAfterMs {
    param($Response)
    try {
        $ra = $Response.Headers.RetryAfter
        if ($null -ne $ra) {
            if ($null -ne $ra.Delta) { return $ra.Delta.TotalMilliseconds }
            if ($null -ne $ra.Date) { return ($ra.Date - [DateTimeOffset]::UtcNow).TotalMilliseconds }
        }
        foreach ($h in @('retry-after-ms', 'x-ms-retry-after-ms')) {
            $vals = $null
            if ($Response.Headers.TryGetValues($h, [ref]$vals)) { foreach ($v in $vals) { return [double]$v } }
        }
    } catch { }
    return 0
}

# Advances an operation without blocking; true once it has a result or an error.
function Step-LbfBlobOp {
    param([Parameter(Mandatory)][hashtable]$Op)
    if ($Op.Done) { return $true }
    if ($null -eq $Op.Task) {
        if ($Op.Token.IsCancellationRequested) {
            $Op.Error = New-LbfTransportError $Op 'context canceled'
            $Op.Done = $true
            return $true
        }
        if ([DateTime]::UtcNow -lt $Op.NotBefore) { return $false }
        try {
            Start-LbfBlobSend $Op
        } catch {
            $Op.Error = New-LbfTransportError $Op (Get-LbfNetReason $_ $Op.Request)
            $Op.Done = $true
            return $true
        }
        return $false
    }
    if (-not $Op.Task.IsCompleted) { return $false }
    $task = $Op.Task
    $Op.Task = $null
    if ($task.IsFaulted -or $task.IsCanceled) {
        $Op.Message.Dispose()
        if ($Op.Token.IsCancellationRequested) {
            $Op.Error = New-LbfTransportError $Op 'context canceled'
            $Op.Done = $true
            return $true
        }
        if ($Op.Try -le $script:BlobMaxRetries) {
            $Op.NotBefore = [DateTime]::UtcNow.AddMilliseconds((Get-LbfRetryDelayMs $Op.Try))
            $Op.Try++
            return $false
        }
        $ex = $task.Exception
        if ($null -eq $ex) { $ex = New-Object System.TimeoutException 'the request timed out' }
        $Op.Error = New-LbfTransportError $Op (Get-LbfNetReason $ex $Op.Request)
        $Op.Done = $true
        return $true
    }
    $resp = $task.Result
    if ($env:LBF_TEST_INTERRUPT -and "$($Op.Request.Method) $($Op.Request.Url)" -match $env:LBF_TEST_INTERRUPT) {
        $resp.Dispose()
        $Op.Message.Dispose()
        Invoke-LbfInterrupt
        $Op.Error = New-LbfTransportError $Op 'context canceled'
        $Op.Done = $true
        return $true
    }
    $status = [int]$resp.StatusCode
    if ($script:BlobRetryStatus -contains $status -and $Op.Try -le $script:BlobMaxRetries) {
        $delay = Get-LbfRetryAfterMs $resp
        if ($delay -le 0) { $delay = Get-LbfRetryDelayMs $Op.Try }
        if ($delay -le $script:BlobMaxRetryDelayMs) {
            $resp.Dispose()
            $Op.Message.Dispose()
            $Op.NotBefore = [DateTime]::UtcNow.AddMilliseconds($delay)
            $Op.Try++
            return $false
        }
    }
    $Op.Result = @{ Status = $status; Reason = [string]$resp.ReasonPhrase; Response = $resp; Body = $null }
    $ok = $status -ge 200 -and $status -lt 300
    if (-not $Op.Request.Stream -or -not $ok) {
        try { $Op.Result.Body = $resp.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult() } catch { $Op.Result.Body = New-Object byte[] 0 }
        if ($ok) { $Op.Message.Dispose() }
    }
    $Op.Done = $true
    return $true
}

# The result of a finished operation; throws what Go's SDK returns for a transport error or an unexpected status.
function Complete-LbfBlobOp {
    param([Parameter(Mandatory)][hashtable]$Op, [int[]]$Ok = @(200))
    if ($null -ne $Op.Error) { throw $Op.Error }
    $r = $Op.Result
    if ($Ok -notcontains $r.Status) {
        $e = New-LbfResponseError $Op.Request $r
        Close-LbfBlobResult $r
        throw $e
    }
    return $r
}

function Close-LbfBlobResult {
    param($Result)
    if ($null -ne $Result -and $null -ne $Result.Response) {
        try { $Result.Response.Dispose() } catch { }
    }
}

function Stop-LbfBlobOp {
    param([hashtable]$Op)
    if ($null -ne $Op.Task) {
        try { [void]$Op.Task.Wait(5000) } catch { }
        if ($Op.Task.IsCompleted -and -not $Op.Task.IsFaulted -and -not $Op.Task.IsCanceled) { try { $Op.Task.Result.Dispose() } catch { } }
        $Op.Task = $null
    }
    if ($null -ne $Op.Message) { try { $Op.Message.Dispose() } catch { } }
    Close-LbfBlobResult $Op.Result
    $Op.Done = $true
}

function Add-LbfOpTask {
    param([System.Collections.Generic.List[object]]$Tasks, [hashtable]$Op)
    if ($null -ne $Op -and $null -ne $Op.Task) { $Tasks.Add($Op.Task) }
}

# Milliseconds until the earliest retry among ops waits to be sent, capped at Max.
function Get-LbfOpWaitMs {
    param([object[]]$Ops, [int]$Max = 100)
    $ms = $Max
    $now = [DateTime]::UtcNow
    foreach ($op in $Ops) {
        if ($null -ne $op -and -not $op.Done -and $null -eq $op.Task) {
            $d = [Math]::Max([double]1, [Math]::Min([double]$Max, ($op.NotBefore - $now).TotalMilliseconds))
            $ms = [Math]::Min($ms, [int]$d)
        }
    }
    return $ms
}

function Invoke-LbfBlobRequest {
    param([Parameter(Mandatory)][hashtable]$Request, [int[]]$Ok = @(200), $Token = $null)
    $op = New-LbfBlobOp $Request $Token
    $tasks = New-Object System.Collections.Generic.List[object]
    while (-not (Step-LbfBlobOp $op)) {
        $tasks.Clear()
        Add-LbfOpTask $tasks $op
        Wait-LbfTasks $tasks (Get-LbfOpWaitMs @($op))
    }
    return Complete-LbfBlobOp $op $Ok
}

# ---------- Errors as azcore and net/http word them ----------

function New-LbfTransportError {
    param([hashtable]$Op, [string]$Reason)
    $m = $Op.Request.Method
    $verb = $m.Substring(0, 1) + $m.Substring(1).ToLowerInvariant()
    $e = New-LbfError "$verb $(Format-LbfQuote $Op.Request.Url): $Reason"
    if ($Reason -eq 'context canceled') { $e.Data['lbfCanceled'] = $true }
    return $e
}

function Get-LbfHostPort {
    param([string]$Url, [switch]$Resolve)
    $u = New-Object System.Uri $Url
    $h = $u.Host
    if ($Resolve -and $u.HostNameType -eq [UriHostNameType]::Dns) {
        try {
            $addrs = [System.Net.Dns]::GetHostAddresses($h)
            if ($addrs.Count -gt 0) { $h = $addrs[0].ToString() }
        } catch { }
    }
    if ($h.Contains(':') -and -not $h.StartsWith('[')) { $h = "[$h]" }
    return @{ Host = $u.Host; HostPort = "${h}:$($u.Port)" }
}

# What Go's net package says for the socket errors a user can meet: no network, a name that does not resolve, nothing listening.
function Get-LbfNetReason {
    param($ErrorOrException, [hashtable]$Request)
    $e = $ErrorOrException
    if ($e -is [System.Management.Automation.ErrorRecord]) { $e = $e.Exception }
    $socket = $null
    $inner = $e
    $last = $e
    while ($null -ne $inner) {
        if ($inner -is [System.Net.Sockets.SocketException]) { $socket = $inner; break }
        if ($inner -is [System.AggregateException] -and $inner.InnerExceptions.Count -gt 0) { $inner = $inner.InnerExceptions[0] }
        else { $inner = $inner.InnerException }
        if ($null -ne $inner) { $last = $inner }
    }
    if ($null -ne $socket) {
        $code = [string]$socket.SocketErrorCode
        switch ($code) {
            'ConnectionRefused' {
                $hp = Get-LbfHostPort $Request.Url -Resolve
                if ($script:OnWindows) { return "dial tcp $($hp.HostPort): connectex: No connection could be made because the target machine actively refused it." }
                return "dial tcp $($hp.HostPort): connect: connection refused"
            }
            { $_ -eq 'HostNotFound' -or $_ -eq 'NoData' -or $_ -eq 'TryAgain' } {
                $hp = Get-LbfHostPort $Request.Url
                return "dial tcp: lookup $($hp.Host): no such host"
            }
            'TimedOut' {
                $hp = Get-LbfHostPort $Request.Url -Resolve
                return "dial tcp $($hp.HostPort): i/o timeout"
            }
            'NetworkUnreachable' {
                $hp = Get-LbfHostPort $Request.Url -Resolve
                if ($script:OnWindows) { return "dial tcp $($hp.HostPort): connectex: A socket operation was attempted to an unreachable network." }
                return "dial tcp $($hp.HostPort): connect: network is unreachable"
            }
            'ConnectionReset' { return 'connection reset by peer' }
        }
        return $socket.Message
    }
    if ($last -is [System.Threading.Tasks.TaskCanceledException] -or $last -is [System.TimeoutException]) { return 'i/o timeout' }
    return $last.Message
}

function Get-LbfResponseErrorCode {
    param([hashtable]$Result)
    $vals = $null
    if ($Result.Response.Headers.TryGetValues('x-ms-error-code', [ref]$vals)) { foreach ($v in $vals) { if ($v -ne '') { return $v } } }
    $body = $Result.Body
    if ($null -eq $body -or $body.Length -eq 0) { return '' }
    $text = $script:Utf8NoBom.GetString($body)
    try {
        $j = ConvertFrom-LbfJson $text -LastWins
        if ($j -is [System.Collections.IDictionary]) {
            foreach ($w in @('error', 'odata.error')) {
                if ($j.Contains($w)) {
                    if ($j[$w] -is [System.Collections.IDictionary]) { $j = $j[$w] } else { return '' }
                    break
                }
            }
            if ($j.Contains('code') -and $j['code'] -is [string]) { return $j['code'] }
            return ''
        }
        return ''
    } catch { }
    $m = [regex]::Match($text, '<(?:\w+:)?[c|C]ode>\s*(\w+)\s*<\/(?:\w+:)?[c|C]ode>')
    if ($m.Success) { return $m.Groups[1].Value }
    return ''
}

# azcore's ResponseError.Error().
function New-LbfResponseError {
    param([hashtable]$Request, [hashtable]$Result)
    $sep = '-' * 80
    $code = Get-LbfResponseErrorCode $Result
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("$($Request.Method) $($Request.Display)`n$sep`n")
    [void]$sb.Append("RESPONSE $($Result.Status): $($Result.Status) $($Result.Reason)`n")
    if ($code -ne '') { [void]$sb.Append("ERROR CODE: $code`n") } else { [void]$sb.Append("ERROR CODE UNAVAILABLE`n") }
    [void]$sb.Append("$sep`n")
    if ($null -ne $Result.Body -and $Result.Body.Length -gt 0) {
        [void]$sb.Append($script:Utf8NoBom.GetString($Result.Body)).Append("`n")
    } else {
        [void]$sb.Append("Response contained no body`n")
    }
    [void]$sb.Append("$sep`n")
    $e = New-LbfError $sb.ToString()
    $e.Data['azCode'] = $code
    $e.Data['azStatus'] = $Result.Status
    return $e
}

# Go's bloberror.HasCode.
function Test-LbfAzCode {
    param([Parameter(Mandatory)]$ErrorOrException, [Parameter(Mandatory)][string[]]$Codes)
    $e = Get-LbfException $ErrorOrException
    if (-not $e.Data.Contains('azCode')) { return $false }
    return $Codes -ccontains [string]$e.Data['azCode']
}

# Go's fmt.Errorf("prefix: %w", err): the cause's code and exit code carry through.
function New-LbfWrappedError {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Prefix, [Parameter(Mandatory)]$ErrorOrException, [string]$Suffix = '')
    $inner = Get-LbfException $ErrorOrException
    $e = New-LbfError ($Prefix + $inner.Message + $Suffix)
    foreach ($k in @('azCode', 'azStatus', 'lbfExit', 'lbfCanceled', 'lbfAnotherCrate')) {
        if ($inner.Data.Contains($k)) { $e.Data[$k] = $inner.Data[$k] }
    }
    return $e
}

function Test-LbfErrorFlag {
    param([Parameter(Mandatory)]$ErrorOrException, [Parameter(Mandatory)][string]$Flag)
    return (Get-LbfException $ErrorOrException).Data.Contains($Flag)
}

# ---------- One dataset's blobs ----------

function Get-LbfMetadataValue {
    param($Metadata, [string]$Key)
    foreach ($k in $Metadata.Keys) { if ([string]::Equals([string]$k, $Key, [StringComparison]::OrdinalIgnoreCase)) { return [string]$Metadata[$k] } }
    return ''
}

function Read-LbfXml {
    param([byte[]]$Bytes)
    $doc = New-Object System.Xml.XmlDocument
    $doc.XmlResolver = $null
    $ms = New-Object System.IO.MemoryStream (, $Bytes)
    try { $doc.Load($ms) } finally { $ms.Dispose() }
    return $doc
}

# Go's blobs.list: the dataset's files by path within it, as hashtables Rel, Size, SHA256.
function Get-LbfStoredFiles {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][AllowEmptyString()][string]$Id, $Token = $null)
    $prefix = $Id + '/'
    $stored = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    $marker = ''
    while ($true) {
        $params = @('restype', 'container', 'comp', 'list', 'include', 'metadata', 'prefix', $prefix)
        if ($marker -ne '') { $params += @('marker', $marker) }
        try {
            $r = Invoke-LbfBlobRequest (New-LbfBlobRequest -Target $Target -Blob $null -Params $params -Headers @{ Accept = 'application/xml' }) -Token $Token
        } catch {
            throw (New-LbfWrappedError "listing ${Id}: " $_)
        }
        $doc = Read-LbfXml $r.Body
        foreach ($blob in $doc.DocumentElement.SelectNodes('Blobs/Blob')) {
            $nameNode = $blob.SelectSingleNode('Name')
            $name = $nameNode.InnerText
            if ($nameNode.GetAttribute('Encoded') -eq 'true') {
                $decoded = ConvertFrom-LbfQueryEscape $name
                if ($null -ne $decoded) { $name = $decoded }
            }
            $meta = @{}
            $metaNode = $blob.SelectSingleNode('Metadata')
            if ($null -ne $metaNode) { foreach ($c in $metaNode.ChildNodes) { if ($c.NodeType -eq [System.Xml.XmlNodeType]::Element) { $meta[$c.LocalName] = $c.InnerText } } }
            if ((Get-LbfMetadataValue $meta 'hdi_isfolder') -eq 'true') { continue }
            $size = [long]0
            $len = $blob.SelectSingleNode('Properties/Content-Length')
            if ($null -ne $len -and $len.InnerText -ne '') { $size = [long]::Parse($len.InnerText, $script:Invariant) }
            $rel = $name
            if ($rel.StartsWith($prefix, [StringComparison]::Ordinal)) { $rel = $rel.Substring($prefix.Length) }
            $stored[$rel] = @{ Rel = $rel; Size = $size; SHA256 = (Get-LbfMetadataValue $meta 'sha256') }
        }
        $next = $doc.DocumentElement.SelectSingleNode('NextMarker')
        if ($null -eq $next -or $next.InnerText -eq '') { break }
        $marker = $next.InnerText
    }
    return , $stored
}

function Read-LbfBlob {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][string]$Name, $Token = $null)
    $r = Invoke-LbfBlobRequest (New-LbfBlobRequest -Target $Target -Blob $Name) -Token $Token
    return , $r.Body
}

# Go's blob GetProperties: Size and Metadata, or throws.
function Get-LbfBlobProperties {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][string]$Name, $Token = $null)
    $r = Invoke-LbfBlobRequest (New-LbfBlobRequest -Target $Target -Blob $Name -Method 'HEAD') -Token $Token
    return ConvertFrom-LbfPropertiesResult $r
}

function ConvertFrom-LbfPropertiesResult {
    param([hashtable]$Result)
    $resp = $Result.Response
    $meta = @{}
    foreach ($h in $resp.Headers) {
        if ($h.Key.StartsWith('x-ms-meta-', [StringComparison]::OrdinalIgnoreCase)) { $meta[$h.Key.Substring(10)] = ($h.Value -join ',') }
    }
    $size = [long]0
    if ($null -ne $resp.Content -and $null -ne $resp.Content.Headers.ContentLength) { $size = [long]$resp.Content.Headers.ContentLength }
    Close-LbfBlobResult $Result
    return @{ Size = $size; Metadata = $meta }
}

# Go's blobs.has: whether the blob exists.
function Test-LbfBlobExists {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][string]$Name, $Token = $null)
    try {
        [void](Get-LbfBlobProperties $Target $Name $Token)
        return $true
    } catch {
        if (Test-LbfAzCode $_ @('BlobNotFound')) { return $false }
        throw
    }
}

function New-LbfIfAbsentHeaders {
    param([hashtable]$Extra = @{})
    $h = @{ 'If-None-Match' = '*' }
    foreach ($k in $Extra.Keys) { $h[$k] = $Extra[$k] }
    return $h
}

# An uncommitted block proves write access without creating a visible blob; Azure discards it after a week.
function Test-LbfWriteAccess {
    param([Parameter(Mandatory)][hashtable]$Target)
    $blockId = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes('lbf-write-check'))
    $req = New-LbfBlobRequest -Target $Target -Blob '.lbf-check' -Method 'PUT' -Params @('comp', 'block', 'blockid', $blockId) -Body ([byte[]]@(0)) -ContentType 'application/octet-stream'
    try {
        [void](Invoke-LbfBlobRequest $req -Ok @(201))
    } catch {
        if (Test-LbfAzCode $_ $script:BlobCheckCodes) {
            throw (New-LbfError "you cannot write to $($Target.Account)/$($Target.Container), so nothing was uploaded; your login needs 'Storage Blob Data Contributor' there")
        }
        throw (New-LbfWrappedError "checking write access to $($Target.Account)/$($Target.Container): " $_)
    }
}

# Go's checkAccess: lists one blob, then for upload stages a block nobody sees.
function Invoke-LbfCheckAccess {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][string]$Mode)
    Initialize-LbfBlobAccess $Target
    Set-LbfStatus "Checking access to $($Target.Account)/$($Target.Container)"
    try {
        try {
            [void](Invoke-LbfBlobRequest (New-LbfBlobRequest -Target $Target -Blob $null -Params @('restype', 'container', 'comp', 'list', 'maxresults', '1') -Headers @{ Accept = 'application/xml' }))
        } catch {
            if (Test-LbfAzCode $_ $script:BlobCheckCodes) {
                throw (New-LbfError "you cannot list $($Target.Account)/$($Target.Container); your login needs 'Storage Blob Data Reader' there")
            }
            throw (New-LbfWrappedError "checking access to $($Target.Account)/$($Target.Container): " $_)
        }
        if ($Mode -eq 'upload') { Test-LbfWriteAccess $Target }
    } finally {
        Set-LbfStatus ''
    }
}
