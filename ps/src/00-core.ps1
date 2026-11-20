Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$script:Utf8NoBom = New-Object System.Text.UTF8Encoding $false
$script:Invariant = [Globalization.CultureInfo]::InvariantCulture
$script:OnWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT

# PowerShell's -eq, -ceq, -contains, switch and @{} compare by culture, which ignores some characters and Unicode forms.
function Test-LbfSame {
    param([AllowNull()][AllowEmptyString()][string]$A, [AllowNull()][AllowEmptyString()][string]$B)
    return [string]::Equals($A, $B, [StringComparison]::Ordinal)
}

function Test-LbfIn {
    param([AllowNull()][AllowEmptyString()][string]$Value, [AllowEmptyCollection()][string[]]$Choices)
    foreach ($c in $Choices) { if ([string]::Equals($Value, $c, [StringComparison]::Ordinal)) { return $true } }
    return $false
}

$script:HighChars = New-Object System.Text.RegularExpressions.Regex ('[\uD800-\uFFFF]')

# A key whose UTF-16 ordinal order is the UTF-8 byte order Go sorts by; the two differ only above U+D7FF.
function Get-LbfUtf8SortKey {
    param([AllowEmptyString()][string]$Text)
    if (-not $script:HighChars.IsMatch($Text)) { return $Text }
    $chars = $Text.ToCharArray()
    for ($i = 0; $i -lt $chars.Length; $i++) {
        $c = [int]$chars[$i]
        if ($c -ge 0xD800 -and $c -le 0xDFFF) { $chars[$i] = [char]($c + 0x2000) }
        elseif ($c -ge 0xE000) { $chars[$i] = [char]($c - 0x800) }
    }
    return New-Object string (, $chars)
}

# Go's strings.Compare.
function Compare-LbfUtf8 {
    param([AllowEmptyString()][string]$A, [AllowEmptyString()][string]$B)
    return [Math]::Sign([string]::CompareOrdinal((Get-LbfUtf8SortKey $A), (Get-LbfUtf8SortKey $B)))
}

# Go's slices.Sort of strings.
function Get-LbfSortedUtf8 {
    param([AllowEmptyCollection()][string[]]$Strings)
    $keys = New-Object string[] $Strings.Length
    for ($i = 0; $i -lt $keys.Length; $i++) { $keys[$i] = Get-LbfUtf8SortKey $Strings[$i] }
    $out = [string[]]$Strings.Clone()
    [Array]::Sort([Array]$keys, [Array]$out, [Collections.IComparer][StringComparer]::Ordinal)
    return , $out
}

function New-LbfError {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Message, [int]$ExitCode = 1, [System.Exception]$Inner)
    if ($Inner) { $e = New-Object System.Exception -ArgumentList $Message, $Inner } else { $e = New-Object System.Exception -ArgumentList $Message }
    $e.Data['lbf'] = $true
    $e.Data['lbfExit'] = $ExitCode
    return $e
}

# Strips the wrappers PowerShell puts around .NET exceptions so the message is the cause's own.
function Get-LbfException {
    param([Parameter(Mandatory)]$ErrorOrException)
    $e = $ErrorOrException
    if ($e -is [System.Management.Automation.ErrorRecord]) { $e = $e.Exception }
    while ($true) {
        if ($e.Data.Contains('lbf')) { return $e }
        if (($e -is [System.Management.Automation.MethodInvocationException] -or
             $e -is [System.Management.Automation.GetValueInvocationException] -or
             $e -is [System.Management.Automation.SetValueInvocationException] -or
             $e -is [System.Reflection.TargetInvocationException] -or
             $e -is [System.AggregateException]) -and $null -ne $e.InnerException) {
            $e = $e.InnerException
            continue
        }
        return $e
    }
}

function Get-LbfErrorText {
    param([Parameter(Mandatory)]$ErrorOrException)
    return (Get-LbfException $ErrorOrException).Message
}

function Get-LbfExitCode {
    param([Parameter(Mandatory)]$ErrorOrException)
    $e = Get-LbfException $ErrorOrException
    if ($e.Data.Contains('lbfExit')) { return [int]$e.Data['lbfExit'] }
    return 1
}

function Test-LbfExitCode {
    param([Parameter(Mandatory)]$ErrorOrException, [int]$Code)
    return (Get-LbfExitCode $ErrorOrException) -eq $Code
}

# What Go's os package says for an error, e.g. "open C:\x: The system cannot find the file specified."
function Format-LbfOsError {
    param([Parameter(Mandatory)][string]$Op, [Parameter(Mandatory)][AllowEmptyString()][string]$Path, [Parameter(Mandatory)]$ErrorOrException)
    return "$Op ${Path}: $(Get-LbfOsReason $ErrorOrException)"
}

# FormatMessage's text as Go prints it; .NET Framework drops the final full stop.
function Get-LbfWin32Message {
    param([Parameter(Mandatory)][int]$Code)
    $m = (New-Object System.ComponentModel.Win32Exception $Code).Message.TrimEnd()
    if (-not $m.EndsWith('.', [StringComparison]::Ordinal)) { $m += '.' }
    return $m
}

# What Go reports when a path runs through a file: ERROR_PATH_NOT_FOUND on Windows, ENOTDIR elsewhere.
function Get-LbfNotDirReason {
    if ($script:OnWindows) { return Get-LbfWin32Message 3 }
    return 'not a directory'
}

function Get-LbfOsReason {
    param([Parameter(Mandatory)]$ErrorOrException)
    $e = Get-LbfException $ErrorOrException
    if ($script:OnWindows) {
        $hr = $e.HResult
        if (($hr -band 0xFFFF0000) -eq 0x80070000) {
            return Get-LbfWin32Message ([int]($hr -band 0xFFFF))
        }
        return $e.Message
    }
    if ($e -is [System.IO.FileNotFoundException] -or $e -is [System.IO.DirectoryNotFoundException]) { return 'no such file or directory' }
    if ($e -is [System.UnauthorizedAccessException]) { return 'permission denied' }
    $m = $e.Message
    if ($m.Length -gt 0) { $m = $m.Substring(0, 1).ToLowerInvariant() + $m.Substring(1) }
    return $m
}

$script:StatusShown = $false
$script:StderrLive = $null

function Write-LbfErr {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    [Console]::Error.Write($Text)
    [Console]::Error.Flush()
}

function Test-LbfStderrLive {
    if ($null -eq $script:StderrLive) {
        $live = -not [Console]::IsErrorRedirected -and -not (Test-LbfSame $env:TERM 'dumb')
        if ($live -and $script:OnWindows) {
            $vt = $false
            try { $vt = [bool]$Host.UI.SupportsVirtualTerminal } catch { $vt = $false }
            $live = $vt
        }
        $script:StderrLive = $live
    }
    return $script:StderrLive
}

function Get-LbfTermSize {
    try {
        $w = [Console]::WindowWidth
        $h = [Console]::WindowHeight
        if ($w -gt 0 -and $h -gt 0) { return , @($w, $h) }
    } catch { }
    return , @(80, 24)
}

# Go's status: one line at the bottom of a live terminal, replaced or cleared, overwritten by logs.
function Set-LbfStatus {
    param([AllowEmptyString()][string]$Message = '')
    if (-not (Test-LbfStderrLive)) { return }
    $text = "`r$([char]27)[K"
    $script:StatusShown = $Message.Length -ne 0
    if ($script:StatusShown) {
        $size = Get-LbfTermSize
        $text += Format-LbfFit ((Format-LbfPrintable $Message) + '...') ($size[0] - 1)
    }
    Write-LbfErr $text
}

# Go's logf: the message is complete, newline included.
function Write-LbfLog {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    if ($script:StatusShown) {
        Write-LbfErr "`r$([char]27)[K"
        $script:StatusShown = $false
    }
    Write-LbfErr $Text
}

# Code points of a string, pairing surrogates as Go's range over a string does.
function Get-LbfRunes {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $runes = New-Object System.Collections.Generic.List[int]
    for ($i = 0; $i -lt $Text.Length; $i++) {
        $c = $Text[$i]
        if ([char]::IsHighSurrogate($c) -and $i + 1 -lt $Text.Length -and [char]::IsLowSurrogate($Text[$i + 1])) {
            $runes.Add([char]::ConvertToUtf32($c, $Text[$i + 1]))
            $i++
        } elseif ([char]::IsSurrogate($c)) {
            $runes.Add(0xFFFD)
        } else {
            $runes.Add([int]$c)
        }
    }
    return , $runes.ToArray()
}

function ConvertFrom-LbfRune {
    param([Parameter(Mandatory)][int]$Rune)
    return [char]::ConvertFromUtf32($Rune)
}

function Get-LbfRuneCategory {
    param([Parameter(Mandatory)][int]$Rune)
    return [Globalization.CharUnicodeInfo]::GetUnicodeCategory([char]::ConvertFromUtf32($Rune), 0)
}

# Go's unicode.IsPrint.
function Test-LbfPrintRune {
    param([Parameter(Mandatory)][int]$Rune)
    if ($Rune -eq 0x20) { return $true }
    if ($Rune -ge 0xD800 -and $Rune -le 0xDFFF) { return $false }
    switch ([int](Get-LbfRuneCategory $Rune)) {
        { $_ -le 10 } { return $true }    # letters, marks, numbers
        { $_ -ge 18 -and $_ -le 28 } { return $true }  # punctuation, symbols
        default { return $false }
    }
}

# Go's unicode.IsControl or unicode.Cf, as progress's printable replaces.
function Test-LbfHiddenRune {
    param([Parameter(Mandatory)][int]$Rune)
    if ($Rune -lt 0x20 -or ($Rune -ge 0x7F -and $Rune -lt 0xA0)) { return $true }
    if ($Rune -ge 0xD800 -and $Rune -le 0xDFFF) { return $false }
    return (Get-LbfRuneCategory $Rune) -eq [Globalization.UnicodeCategory]::Format
}

function Format-LbfPrintable {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $sb = New-Object System.Text.StringBuilder
    foreach ($r in (Get-LbfRunes $Text)) {
        if (Test-LbfHiddenRune $r) { [void]$sb.Append('?') } else { [void]$sb.Append([char]::ConvertFromUtf32($r)) }
    }
    return $sb.ToString()
}

function Get-LbfUtf8Length {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    return $script:Utf8NoBom.GetByteCount($Text)
}

# Go's fit: at most w UTF-8 bytes, cut at a character boundary.
function Format-LbfFit {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text, [Parameter(Mandatory)][int]$Width)
    $bytes = $script:Utf8NoBom.GetBytes($Text)
    if ($bytes.Length -le $Width) { return $Text }
    $w = [Math]::Max($Width, 0)
    while ($w -gt 0 -and ($bytes[$w] -band 0xC0) -eq 0x80) { $w-- }
    return $script:Utf8NoBom.GetString($bytes, 0, $w)
}

# Go's clipLeft: the end of s in w bytes, led by "...".
function Format-LbfClipLeft {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text, [Parameter(Mandatory)][int]$Width)
    $bytes = $script:Utf8NoBom.GetBytes($Text)
    if ($bytes.Length -le $Width) { return $Text }
    if ($Width -lt 4) { return Format-LbfFit '...' $Width }
    $i = $bytes.Length - ($Width - 3)
    while ($i -lt $bytes.Length -and ($bytes[$i] -band 0xC0) -eq 0x80) { $i++ }
    return '...' + $script:Utf8NoBom.GetString($bytes, $i, $bytes.Length - $i)
}

# Go's %q (strconv.Quote); MaxRunes is %.Nq.
function Format-LbfQuote {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text, [int]$MaxRunes = -1)
    $runes = Get-LbfRunes $Text
    if ($MaxRunes -ge 0 -and $runes.Length -gt $MaxRunes) { $runes = $runes[0..($MaxRunes - 1)]; if ($MaxRunes -eq 0) { $runes = @() } }
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('"')
    :runes foreach ($r in $runes) {
        if ($r -eq 0x22) { [void]$sb.Append('\"'); continue }
        if ($r -eq 0x5C) { [void]$sb.Append('\\'); continue }
        if (Test-LbfPrintRune $r) { [void]$sb.Append([char]::ConvertFromUtf32($r)); continue }
        switch ($r) {
            7 { [void]$sb.Append('\a'); continue runes }
            8 { [void]$sb.Append('\b'); continue runes }
            12 { [void]$sb.Append('\f'); continue runes }
            10 { [void]$sb.Append('\n'); continue runes }
            13 { [void]$sb.Append('\r'); continue runes }
            9 { [void]$sb.Append('\t'); continue runes }
            11 { [void]$sb.Append('\v'); continue runes }
        }
        if ($r -lt 0x20 -or $r -eq 0x7F) { [void]$sb.Append('\x' + $r.ToString('x2')) }
        elseif ($r -lt 0x10000) { [void]$sb.Append('\u' + $r.ToString('x4')) }
        else { [void]$sb.Append('\U' + $r.ToString('x8')) }
    }
    [void]$sb.Append('"')
    return $sb.ToString()
}

# Go's time.RFC3339 for a UTC time.
function Format-LbfTimestamp {
    param([Parameter(Mandatory)][datetime]$Time)
    return $Time.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $script:Invariant)
}

function Get-LbfSha256Hex {
    param([byte[]]$Bytes, [string]$Path)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        if ($PSBoundParameters.ContainsKey('Path')) {
            $fs = [IO.File]::OpenRead($Path)
            try { $hash = $sha.ComputeHash($fs) } finally { $fs.Dispose() }
        } else {
            $hash = $sha.ComputeHash($Bytes)
        }
    } finally { $sha.Dispose() }
    return ConvertTo-LbfHex $hash
}

function ConvertTo-LbfHex {
    param([Parameter(Mandatory)][AllowEmptyCollection()][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes) -replace '-', '').ToLowerInvariant()
}

function Get-LbfRandomBytes {
    param([Parameter(Mandatory)][int]$Count)
    $b = New-Object byte[] $Count
    $rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    try { $rng.GetBytes($b) } finally { $rng.Dispose() }
    return , $b
}

function Write-LbfTextFile {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function Read-LbfTextFile {
    param([Parameter(Mandatory)][string]$Path)
    return $script:Utf8NoBom.GetString([IO.File]::ReadAllBytes($Path))
}

function Get-LbfFullPath {
    param([Parameter(Mandatory)][string]$Path)
    return $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
}

# ---------- JSON as Go's encoding/json writes it ----------

function ConvertTo-LbfJsonString {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $sb = New-Object System.Text.StringBuilder ($Text.Length + 2)
    [void]$sb.Append('"')
    $n = $Text.Length
    :chars for ($i = 0; $i -lt $n; $i++) {
        $c = $Text[$i]
        $code = [int]$c
        if ($code -ge 0x20 -and $code -ne 0x22 -and $code -ne 0x5C -and $code -ne 0x3C -and $code -ne 0x3E -and $code -ne 0x26 -and $code -ne 0x2028 -and $code -ne 0x2029 -and -not [char]::IsSurrogate($c)) {
            [void]$sb.Append($c)
            continue
        }
        switch ($code) {
            0x22 { [void]$sb.Append('\"'); continue chars }
            0x5C { [void]$sb.Append('\\'); continue chars }
            8 { [void]$sb.Append('\b'); continue chars }
            12 { [void]$sb.Append('\f'); continue chars }
            10 { [void]$sb.Append('\n'); continue chars }
            13 { [void]$sb.Append('\r'); continue chars }
            9 { [void]$sb.Append('\t'); continue chars }
        }
        if ([char]::IsHighSurrogate($c) -and $i + 1 -lt $n -and [char]::IsLowSurrogate($Text[$i + 1])) {
            [void]$sb.Append($c).Append($Text[$i + 1])
            $i++
            continue
        }
        if ([char]::IsSurrogate($c)) { [void]$sb.Append('\ufffd'); continue }
        [void]$sb.Append('\u' + $code.ToString('x4'))
    }
    [void]$sb.Append('"')
    return $sb.ToString()
}

# Go's strconv.FormatFloat(f, 'g'-like as encoding/json does.
function ConvertTo-LbfJsonFloat {
    param([Parameter(Mandatory)][double]$Value)
    if ([double]::IsNaN($Value) -or [double]::IsInfinity($Value)) { throw (New-LbfError "json: unsupported value: $Value") }
    $abs = [Math]::Abs($Value)
    if ($abs -ne 0 -and ($abs -lt 1e-6 -or $abs -ge 1e21)) {
        $s = $Value.ToString('R', $script:Invariant)
        if ($s -notmatch 'E') { $s = $Value.ToString('E16', $script:Invariant) }
        $m = [regex]::Match($s, '^(-?[0-9.]+)E([+-])0*([0-9]+)$')
        $mant = $m.Groups[1].Value
        if ($mant.Contains('.')) { $mant = $mant.TrimEnd('0').TrimEnd('.') }
        $exp = $m.Groups[3].Value
        if ($exp.Length -lt 2) { $exp = '0' + $exp }
        return "${mant}e$($m.Groups[2].Value)$exp"
    }
    $s = $Value.ToString('R', $script:Invariant)
    if ($s -match 'E') { $s = ([decimal]$Value).ToString($script:Invariant) }
    return $s
}

function Test-LbfJsonStruct {
    param($Value)
    return $Value -is [System.Collections.Specialized.OrderedDictionary] -or $Value -is [System.Management.Automation.PSCustomObject]
}

function Get-LbfJsonMembers {
    param([Parameter(Mandatory)]$Value)
    $pairs = New-Object System.Collections.Generic.List[object]
    if ($Value -is [System.Management.Automation.PSCustomObject]) {
        foreach ($p in $Value.PSObject.Properties) { $pairs.Add(@($p.Name, $p.Value)) }
        return , $pairs
    }
    [string[]]$keys = @($Value.Keys | ForEach-Object { [string]$_ })
    if (-not ($Value -is [System.Collections.Specialized.OrderedDictionary])) {
        $keys = Get-LbfSortedUtf8 $keys
    }
    foreach ($k in $keys) { $pairs.Add(@($k, $Value[$k])) }
    return , $pairs
}

function Write-LbfJsonValue {
    param([System.Text.StringBuilder]$Sb, $Value, [string]$Indent, [string]$Prefix)
    if ($null -eq $Value) { [void]$Sb.Append('null'); return }
    if ($Value -is [string]) { [void]$Sb.Append((ConvertTo-LbfJsonString $Value)); return }
    if ($Value -is [char]) { [void]$Sb.Append((ConvertTo-LbfJsonString ([string]$Value))); return }
    if ($Value -is [bool]) { [void]$Sb.Append($(if ($Value) { 'true' } else { 'false' })); return }
    if ($Value -is [int] -or $Value -is [long] -or $Value -is [int16] -or $Value -is [byte] -or $Value -is [sbyte] -or $Value -is [uint16] -or $Value -is [uint32] -or $Value -is [uint64]) {
        [void]$Sb.Append($Value.ToString($script:Invariant)); return
    }
    if ($Value -is [double] -or $Value -is [single]) { [void]$Sb.Append((ConvertTo-LbfJsonFloat ([double]$Value))); return }
    if ($Value -is [decimal]) { [void]$Sb.Append($Value.ToString($script:Invariant)); return }
    if ($Value -is [datetime]) { [void]$Sb.Append((ConvertTo-LbfJsonString ($Value.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss.FFFFFFF'Z'", $script:Invariant)))); return }
    if ($Value -is [System.Collections.IDictionary] -or $Value -is [System.Management.Automation.PSCustomObject]) {
        $members = Get-LbfJsonMembers $Value
        if ($members.Count -eq 0) { [void]$Sb.Append('{}'); return }
        [void]$Sb.Append('{')
        $inner = $Prefix + $Indent
        $first = $true
        foreach ($m in $members) {
            if (-not $first) { [void]$Sb.Append(',') }
            $first = $false
            if ($Indent -ne '') { [void]$Sb.Append("`n").Append($inner) }
            [void]$Sb.Append((ConvertTo-LbfJsonString $m[0])).Append(':')
            if ($Indent -ne '') { [void]$Sb.Append(' ') }
            Write-LbfJsonValue $Sb $m[1] $Indent $inner
        }
        if ($Indent -ne '') { [void]$Sb.Append("`n").Append($Prefix) }
        [void]$Sb.Append('}')
        return
    }
    if ($Value -is [System.Collections.IEnumerable]) {
        $items = New-Object System.Collections.Generic.List[object]
        foreach ($it in $Value) { $items.Add($it) }
        if ($items.Count -eq 0) { [void]$Sb.Append('[]'); return }
        [void]$Sb.Append('[')
        $inner = $Prefix + $Indent
        for ($i = 0; $i -lt $items.Count; $i++) {
            if ($i -gt 0) { [void]$Sb.Append(',') }
            if ($Indent -ne '') { [void]$Sb.Append("`n").Append($inner) }
            Write-LbfJsonValue $Sb $items[$i] $Indent $inner
        }
        if ($Indent -ne '') { [void]$Sb.Append("`n").Append($Prefix) }
        [void]$Sb.Append(']')
        return
    }
    throw (New-LbfError "json: unsupported type: $($Value.GetType().FullName)")
}

# Hashtables and Dictionaries encode like Go maps (keys sorted), ordered dictionaries and PSCustomObjects like Go structs.
# -Indent '    ' is json.MarshalIndent(v, "", "    "); no -Indent is json.Marshal; add "`n" yourself for json.Encoder.
function ConvertTo-LbfJson {
    param([Parameter(Mandatory)][AllowNull()][AllowEmptyCollection()][AllowEmptyString()]$Value, [string]$Indent = '')
    $sb = New-Object System.Text.StringBuilder
    Write-LbfJsonValue $sb $Value $Indent ''
    return $sb.ToString()
}

function New-LbfOrdered {
    return , (New-Object System.Collections.Specialized.OrderedDictionary ([StringComparer]::Ordinal))
}

function New-LbfMap {
    return , (New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
}

# ---------- JSON reading ----------

$script:JsonToken = New-Object System.Text.RegularExpressions.Regex (
    '[ \t\r\n]*(?:(?<s>"(?:[^"\\\x00-\x1f]|\\.)*")|(?<n>-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)|(?<p>[{}\[\]:,])|(?<l>true|false|null)|(?<x>[^ \t\r\n]))',
    [System.Text.RegularExpressions.RegexOptions]::Singleline)
$script:JsonEscape = New-Object System.Text.RegularExpressions.Regex ('\\(u[0-9a-fA-F]{4}|["\\/bfnrt])')

function New-LbfJsonError {
    param([string]$Kind, [int]$Offset, [string]$Detail)
    $e = New-LbfError "json $Kind at offset ${Offset}: $Detail"
    $e.Data['jsonKind'] = $Kind
    $e.Data['jsonOffset'] = $Offset
    $e.Data['jsonDetail'] = $Detail
    return $e
}

function ConvertFrom-LbfJsonStringToken {
    param([string]$Raw, [int]$Offset)
    $body = $Raw.Substring(1, $Raw.Length - 2)
    if ($body.IndexOf([char]'\') -lt 0) { return $body }
    $bad = [regex]::Match($body, '\\(?!u[0-9a-fA-F]{4}|["\\/bfnrt])')
    if ($bad.Success) { throw (New-LbfJsonError 'escape' ($Offset + 1 + $bad.Index) $body.Substring($bad.Index, [Math]::Min(2, $body.Length - $bad.Index))) }
    return $script:JsonEscape.Replace($body, {
        param($m)
        $v = $m.Groups[1].Value
        switch -CaseSensitive ($v) {
            '"' { return '"' }
            '\' { return '\' }
            '/' { return '/' }
            'b' { return [string][char]8 }
            'f' { return [string][char]12 }
            'n' { return "`n" }
            'r' { return "`r" }
            't' { return "`t" }
        }
        return [string][char][Convert]::ToInt32($v.Substring(1), 16)
    })
}

# Parses JSON into OrderedDictionary (ordinal keys, in document order), object[], string, long or double, bool and $null.
# Duplicate keys throw unless -LastWins, which keeps the last as Go's json.Unmarshal into a map does.
function ConvertFrom-LbfJson {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text, [switch]$LastWins)
    $ms = $script:JsonToken.Matches($Text)
    $count = $ms.Count
    $kinds = New-Object char[] $count
    $vals = New-Object string[] $count
    $offs = New-Object int[] $count
    for ($i = 0; $i -lt $count; $i++) {
        $m = $ms[$i]
        if ($m.Groups['s'].Success) { $g = $m.Groups['s']; $kinds[$i] = 's' }
        elseif ($m.Groups['p'].Success) { $g = $m.Groups['p']; $kinds[$i] = $g.Value[0] }
        elseif ($m.Groups['n'].Success) { $g = $m.Groups['n']; $kinds[$i] = 'n' }
        elseif ($m.Groups['l'].Success) { $g = $m.Groups['l']; $kinds[$i] = 'l' }
        else { $g = $m.Groups['x']; $kinds[$i] = 'x' }
        $vals[$i] = $g.Value
        $offs[$i] = $g.Index
    }
    $state = @{ i = 0; kinds = $kinds; vals = $vals; offs = $offs; n = $count; text = $Text; lastWins = [bool]$LastWins }
    $value = Read-LbfJsonValue $state
    if ($state.i -lt $state.n) {
        throw (New-LbfJsonError 'syntax' $offs[$state.i] "invalid character $(Format-LbfJsonChar $vals[$state.i]) after top-level value")
    }
    return , $value
}

function Format-LbfJsonChar {
    param([string]$Token)
    if ($Token.Length -eq 0) { return "''" }
    return "'" + $Token[0] + "'"
}

function Read-LbfJsonValue {
    param([hashtable]$S)
    if ($S.i -ge $S.n) { throw (New-LbfJsonError 'eof' $S.text.Length 'unexpected end of JSON input') }
    $k = $S.kinds[$S.i]
    $v = $S.vals[$S.i]
    $off = $S.offs[$S.i]
    $S.i++
    switch -CaseSensitive ($k) {
        's' { return (ConvertFrom-LbfJsonStringToken $v $off) }
        'n' {
            if ($v -notmatch '[.eE]') {
                $l = 0L
                if ([long]::TryParse($v, [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$l)) { return $l }
            }
            return [double]::Parse($v, $script:Invariant)
        }
        'l' {
            if ($v -ceq 'true') { return $true }
            if ($v -ceq 'false') { return $false }
            return $null
        }
        '{' {
            $obj = New-LbfOrdered
            if ($S.i -lt $S.n -and $S.kinds[$S.i] -eq '}') { $S.i++; return , $obj }
            while ($true) {
                if ($S.i -ge $S.n) { throw (New-LbfJsonError 'eof' $S.text.Length 'unexpected end of JSON input') }
                if ($S.kinds[$S.i] -ne 's') { throw (New-LbfJsonError 'syntax' $S.offs[$S.i] "invalid character $(Format-LbfJsonChar $S.vals[$S.i]) looking for beginning of object key string") }
                $keyOff = $S.offs[$S.i]
                $key = ConvertFrom-LbfJsonStringToken $S.vals[$S.i] $keyOff
                $S.i++
                if ($S.i -ge $S.n) { throw (New-LbfJsonError 'eof' $S.text.Length 'unexpected end of JSON input') }
                if ($S.kinds[$S.i] -ne ':') { throw (New-LbfJsonError 'syntax' $S.offs[$S.i] "invalid character $(Format-LbfJsonChar $S.vals[$S.i]) after object key") }
                $S.i++
                $val = Read-LbfJsonValue $S
                if ($obj.Contains($key)) {
                    if (-not $S.lastWins) { $e = New-LbfJsonError 'duplicate' $keyOff $key; $e.Data['jsonKey'] = $key; throw $e }
                    $obj[$key] = $val
                } else {
                    $obj.Add($key, $val)
                }
                if ($S.i -ge $S.n) { throw (New-LbfJsonError 'eof' $S.text.Length 'unexpected end of JSON input') }
                $sep = $S.kinds[$S.i]
                if ($sep -eq ',') { $S.i++; continue }
                if ($sep -eq '}') { $S.i++; return , $obj }
                throw (New-LbfJsonError 'syntax' $S.offs[$S.i] "invalid character $(Format-LbfJsonChar $S.vals[$S.i]) after object key:value pair")
            }
        }
        '[' {
            $list = New-Object System.Collections.Generic.List[object]
            if ($S.i -lt $S.n -and $S.kinds[$S.i] -eq ']') { $S.i++; return , ([object[]]@()) }
            while ($true) {
                $list.Add((Read-LbfJsonValue $S))
                if ($S.i -ge $S.n) { throw (New-LbfJsonError 'eof' $S.text.Length 'unexpected end of JSON input') }
                $sep = $S.kinds[$S.i]
                if ($sep -eq ',') { $S.i++; continue }
                if ($sep -eq ']') { $S.i++; return , $list.ToArray() }
                throw (New-LbfJsonError 'syntax' $S.offs[$S.i] "invalid character $(Format-LbfJsonChar $S.vals[$S.i]) after array element")
            }
        }
    }
    throw (New-LbfJsonError 'syntax' $off "invalid character $(Format-LbfJsonChar $v) looking for beginning of value")
}
