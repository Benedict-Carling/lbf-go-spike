$script:BrowseCrateName = 'ro-crate-metadata.json'
$script:BrowseStorageScope = 'https://storage.azure.com/.default'
$script:BrowseApiVersion = '2025-01-05'
$script:BrowseParallel = 16

# ---------- Go's fmt and strconv, as flattenCrate uses them ----------

# Go's %v of a float64: the shortest digits, in exponent form below 1e-4 or from 1e6.
function Format-LbfGoFloat {
    param([double]$Value)
    if ([double]::IsNaN($Value)) { return 'NaN' }
    if ([double]::IsPositiveInfinity($Value)) { return '+Inf' }
    if ([double]::IsNegativeInfinity($Value)) { return '-Inf' }
    if ($Value -eq 0) { return '0' }
    $r = $Value.ToString('R', $script:Invariant)
    $sign = ''
    if ($r.StartsWith('-', [StringComparison]::Ordinal)) { $sign = '-'; $r = $r.Substring(1) }
    $exp10 = 0
    $e = $r.IndexOfAny([char[]]'Ee')
    if ($e -ge 0) { $exp10 = [int]::Parse($r.Substring($e + 1), $script:Invariant); $r = $r.Substring(0, $e) }
    $dot = $r.IndexOf([char]'.')
    $intPart = $r; $frac = ''
    if ($dot -ge 0) { $intPart = $r.Substring(0, $dot); $frac = $r.Substring($dot + 1) }
    $digits = ($intPart + $frac).TrimStart('0')
    $lead = ($intPart + $frac).Length - $digits.Length
    $dp = $intPart.Length - $lead + $exp10
    $digits = $digits.TrimEnd('0')
    if ($digits -eq '') { return $sign + '0' }
    $x = $dp - 1
    if ($x -lt -4 -or $x -ge 6) {
        $m = $digits.Substring(0, 1)
        if ($digits.Length -gt 1) { $m += '.' + $digits.Substring(1) }
        $es = '+'
        if ($x -lt 0) { $es = '-' }
        $ax = [Math]::Abs($x).ToString($script:Invariant)
        if ($ax.Length -lt 2) { $ax = '0' + $ax }
        return "$sign${m}e$es$ax"
    }
    if ($dp -le 0) { return $sign + '0.' + ('0' * (-$dp)) + $digits }
    if ($dp -ge $digits.Length) { return $sign + $digits + ('0' * ($dp - $digits.Length)) }
    return $sign + $digits.Substring(0, $dp) + '.' + $digits.Substring($dp)
}

# Go's fmt.Sprint of a value json.Unmarshal put in an any.
function Format-LbfGoSprint {
    param([AllowNull()]$Value)
    if ($null -eq $Value) { return '<nil>' }
    if ($Value -is [string]) { return $Value }
    if ($Value -is [bool]) { if ($Value) { return 'true' } return 'false' }
    if ($Value -is [long] -or $Value -is [int] -or $Value -is [double]) { return Format-LbfGoFloat ([double]$Value) }
    if ($Value -is [System.Collections.IDictionary]) {
        $parts = foreach ($k in (Get-LbfSortedUtf8 @($Value.Keys | ForEach-Object { [string]$_ }))) { $k + ':' + (Format-LbfGoSprint $Value[$k]) }
        return 'map[' + (@($parts) -join ' ') + ']'
    }
    if ($Value -is [System.Collections.IEnumerable]) {
        $parts = foreach ($v in $Value) { Format-LbfGoSprint $v }
        return '[' + (@($parts) -join ' ') + ']'
    }
    return [string]$Value
}

# Go's fmt.Sscan into an int64: the leading integer, with a base prefix, or 0 if there is none.
function ConvertFrom-LbfGoScanInt {
    param([AllowEmptyString()][string]$Text)
    $i = 0
    while ($i -lt $Text.Length -and [char]::IsWhiteSpace($Text[$i])) { $i++ }
    $sign = ''
    if ($i -lt $Text.Length -and ($Text[$i] -eq '+' -or $Text[$i] -eq '-')) { $sign = [string]$Text[$i]; $i++ }
    $digits = '0123456789_'
    $prefix = ''
    if ($i -lt $Text.Length -and $Text[$i] -eq '0') {
        $i++
        $prefix = '0'
        $digits = '01234567_'
        if ($i -lt $Text.Length) {
            switch -CaseSensitive ([string]$Text[$i]) {
                { Test-LbfIn $_ @('b', 'B') } { $digits = '01_'; $prefix += $_; $i++ }
                { Test-LbfIn $_ @('o', 'O') } { $prefix += $_; $i++ }
                { Test-LbfIn $_ @('x', 'X') } { $digits = '0123456789abcdefABCDEF_'; $prefix += $_; $i++ }
            }
        }
    }
    $start = $i
    while ($i -lt $Text.Length -and $digits.IndexOf($Text[$i]) -ge 0) { $i++ }
    $body = $Text.Substring($start, $i - $start)
    if ($prefix -eq '' -and $body -eq '') { return [long]0 }
    return ConvertFrom-LbfGoParseInt ($sign + $prefix + $body)
}

# Go's strconv.ParseInt(s, 0, 64), giving 0 where it fails.
function ConvertFrom-LbfGoParseInt {
    param([string]$Text)
    $s = $Text
    $neg = $false
    if ($s.StartsWith('+', [StringComparison]::Ordinal) -or $s.StartsWith('-', [StringComparison]::Ordinal)) { $neg = $s[0] -eq '-'; $s = $s.Substring(1) }
    if ($s -eq '') { return [long]0 }
    $base = 10
    $hex = $false
    $body = $s
    if ($s.Length -ge 3 -and $s[0] -eq '0' -and 'bBoOxX'.IndexOf($s[1]) -ge 0) {
        switch ([char]::ToLowerInvariant($s[1])) { 'b' { $base = 2 } 'o' { $base = 8 } 'x' { $base = 16; $hex = $true } }
        $body = $s.Substring(2)
    } elseif ($s[0] -eq '0') {
        $base = 8
        $body = $s.Substring(1)
    }
    if ($s.IndexOf([char]'_') -ge 0) {
        $saw = '^'
        $j = 0
        if ($s.Length -ge 2 -and $s[0] -eq '0' -and 'bBoOxX'.IndexOf($s[1]) -ge 0) { $j = 2; $saw = '0' }
        for (; $j -lt $s.Length; $j++) {
            $c = $s[$j]
            if ('0123456789'.IndexOf($c) -ge 0 -or ($hex -and 'abcdefABCDEF'.IndexOf($c) -ge 0)) { $saw = '0'; continue }
            if ($c -eq '_') { if ($saw -ne '0') { return [long]0 }; $saw = '_'; continue }
            if ($saw -eq '_') { return [long]0 }
            $saw = '!'
        }
        if ($saw -eq '_') { return [long]0 }
        $body = $body.Replace('_', '')
    }
    $limit = [decimal]9223372036854775807
    if ($neg) { $limit += 1 }
    $n = [decimal]0
    foreach ($c in $body.ToCharArray()) {
        $d = '0123456789abcdef'.IndexOf([char]::ToLowerInvariant($c))
        if ($d -lt 0 -or $d -ge $base) { return [long]0 }
        $n = $n * $base + $d
        if ($n -gt $limit) { return [long]0 }
    }
    if ($neg) { $n = -$n }
    return [long]$n
}

function Test-LbfUrlUnescapable {
    param([string]$Text)
    return -not [regex]::IsMatch($Text, '%(?![0-9A-Fa-f]{2})')
}

# Whether Go's url.Parse accepts the text and finds a scheme in it.
function Test-LbfGoUrlHasScheme {
    param([AllowEmptyString()][string]$Text)
    $hash = $Text.IndexOf([char]'#')
    $u = $Text
    $frag = ''
    if ($hash -ge 0) { $u = $Text.Substring(0, $hash); $frag = $Text.Substring($hash + 1) }
    foreach ($c in $u.ToCharArray()) { if ([int]$c -lt 0x20 -or [int]$c -eq 0x7F) { return $false } }
    $colon = -1
    for ($i = 0; $i -lt $u.Length; $i++) {
        $c = [string]$u[$i]
        if ($c -cmatch '^[A-Za-z]$') { continue }
        if ($c -cmatch '^[0-9+.-]$') { if ($i -eq 0) { return $false }; continue }
        if ($u[$i] -eq [char]':') { $colon = $i }
        break
    }
    if ($colon -le 0) { return $false }
    if (-not (Test-LbfUrlUnescapable $frag)) { return $false }
    $rest = $u.Substring($colon + 1)
    $q = $rest.IndexOf([char]'?')
    if ($q -ge 0) { $rest = $rest.Substring(0, $q) }
    if (-not $rest.StartsWith('/', [StringComparison]::Ordinal)) { return $true }
    if ($rest.StartsWith('//', [StringComparison]::Ordinal)) {
        $authority = $rest.Substring(2)
        $slash = $authority.IndexOf([char]'/')
        $rest = ''
        if ($slash -ge 0) { $rest = $authority.Substring($slash); $authority = $authority.Substring(0, $slash) }
        $at = $authority.LastIndexOf([char]'@')
        $hostPart = $authority
        if ($at -ge 0) {
            $userinfo = $authority.Substring(0, $at)
            $hostPart = $authority.Substring($at + 1)
            if ($userinfo -cnotmatch "\A[A-Za-z0-9\-._:~!$&'()*+,;=%@]*\z") { return $false }
            if (-not (Test-LbfUrlUnescapable $userinfo)) { return $false }
        }
        $port = ''
        if ($hostPart.StartsWith('[', [StringComparison]::Ordinal)) {
            $close = $hostPart.LastIndexOf([char]']')
            if ($close -lt 0) { return $false }
            $port = $hostPart.Substring($close + 1)
        } else {
            $pc = $hostPart.LastIndexOf([char]':')
            if ($pc -ge 0) { $port = $hostPart.Substring($pc) }
            if ($hostPart -cnotmatch "\A(?:[A-Za-z0-9\-._~!$&'()*+,;=:\[\]<>`"%]|[^\x00-\x7F])*\z") { return $false }
        }
        if ($port.Length -gt 0 -and $port -cnotmatch '\A:[0-9]*\z') { return $false }
        if (-not (Test-LbfUrlUnescapable $hostPart)) { return $false }
    }
    return Test-LbfUrlUnescapable $rest
}

# ---------- flattenCrate ----------

function Get-LbfJsonKind {
    param($Value)
    if ($Value -is [string]) { return 'string' }
    if ($Value -is [bool]) { return 'bool' }
    if ($Value -is [System.Collections.IDictionary]) { return 'object' }
    if ($Value -is [object[]]) { return 'array' }
    return 'number'
}

function Get-LbfAsList {
    param([AllowNull()]$Value)
    if ($null -eq $Value) { return , ([object[]]@()) }
    if ($Value -is [object[]]) { return , $Value }
    return , ([object[]]@(, $Value))
}

function Get-LbfRefId {
    param([AllowNull()]$Value)
    if ($Value -is [System.Collections.IDictionary] -and $Value.Contains('@id') -and $Value['@id'] -is [string]) { return $Value['@id'] }
    return ''
}

function Get-LbfTypesOf {
    param($Entity)
    $t = $null
    if ($Entity.Contains('@type')) { $t = $Entity['@type'] }
    $types = foreach ($x in (Get-LbfAsList $t)) { Format-LbfGoSprint $x }
    return , ([string[]]@($types))
}

function Get-LbfMapValue {
    param([AllowNull()]$Map, [string]$Key)
    if ($Map -is [System.Collections.IDictionary] -and $Map.Contains($Key)) { return , $Map[$Key] }
    return $null
}

# Go's flattenCrate: one row per root Dataset and the run that made it, with its files and the names of linked IRIs.
function ConvertFrom-LbfBrowseCrate {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $prefix = "its $($script:BrowseCrateName) is not valid JSON: "
    try {
        $doc = ConvertFrom-LbfJson $Text -LastWins
    } catch {
        $e = Get-LbfException $_
        $detail = Get-LbfErrorText $_
        if ($e.Data.Contains('jsonKind')) {
            $detail = $e.Data['jsonDetail']
            if ($e.Data['jsonKind'] -eq 'escape') {
                $off = [int]$e.Data['jsonOffset']
                $len = 2
                if ([string]::Equals($detail, '\u', [StringComparison]::Ordinal)) { $len = 6 }
                $detail = "invalid escape sequence ``$($Text.Substring($off, [Math]::Min($len, $Text.Length - $off)))`` in string"
            }
        }
        throw (New-LbfError ($prefix + $detail))
    }
    $graphList = [object[]]@()
    $err = $null
    if ($doc -is [System.Collections.IDictionary]) {
        foreach ($k in @($doc.Keys)) {
            if (-not [string]::Equals([string]$k, '@graph', [StringComparison]::OrdinalIgnoreCase)) { continue }
            $g = $doc[$k]
            if ($null -eq $g) { $graphList = [object[]]@(); continue }
            if ($g -isnot [object[]]) {
                if ($null -eq $err) { $err = "json: cannot unmarshal $(Get-LbfJsonKind $g) into Go struct field .@graph of type []map[string]interface {}" }
                continue
            }
            for ($i = 0; $i -lt $g.Count; $i++) {
                if ($null -ne $g[$i] -and $g[$i] -isnot [System.Collections.IDictionary] -and $null -eq $err) {
                    $err = "json: cannot unmarshal $(Get-LbfJsonKind $g[$i]) into .@graph.$i of type map[string]interface {}"
                }
            }
            $graphList = $g
        }
    } elseif ($null -ne $doc) {
        $err = "json: cannot unmarshal $(Get-LbfJsonKind $doc) into Go value of type struct { Graph []map[string]interface {} `"json:\`"@graph\`"`" }"
    }
    if ($null -ne $err) { throw (New-LbfError ($prefix + $err)) }

    $graph = New-LbfOrdered
    foreach ($e in $graphList) {
        if ($e -is [System.Collections.IDictionary] -and $e.Contains('@id') -and $e['@id'] -is [string]) { $graph[$e['@id']] = $e }
    }
    $rootId = './'
    $about = Get-LbfMapValue (Get-LbfMapValue $graph $script:BrowseCrateName) 'about'
    if ($about -is [System.Collections.IDictionary]) {
        $rootId = ''
        if ($about.Contains('@id') -and $about['@id'] -is [string]) { $rootId = $about['@id'] }
    }
    if (-not $graph.Contains($rootId)) { throw (New-LbfError "its $($script:BrowseCrateName) has no root dataset") }
    $root = $graph[$rootId]

    $names = New-LbfMap
    $ctx = @{ Graph = $graph; Names = $names }
    $row = New-LbfMap
    foreach ($k in @($root.Keys)) {
        $v = $root[$k]
        switch -CaseSensitive ($k) {
            { Test-LbfIn $_ @('@id', '@type', 'hasPart', 'mentions') } { break }
            { Test-LbfSame $_ 'additionalProperty' } {
                foreach ($ref in (Get-LbfAsList $v)) {
                    $pv = $null
                    if ($ref -is [System.Collections.IDictionary]) { $pv = $ref }
                    $key = Format-LbfGoSprint (Get-LbfMapValue $pv '@id')
                    if ($graph.Contains($key)) { $pv = $graph[$key] }
                    $name = Get-LbfMapValue $pv 'name'
                    if ($name -is [string]) { $row[$name] = Format-LbfGoSprint (Get-LbfMapValue $pv 'value') }
                }
                break
            }
            default { $row[$k] = Get-LbfBrowseLabels $ctx $v }
        }
    }
    foreach ($e in @($graph.Values)) {
        if (-not (Test-LbfIn 'CreateAction' (Get-LbfTypesOf $e))) { continue }
        $made = $false
        foreach ($r in (Get-LbfAsList (Get-LbfMapValue $e 'result'))) { if (Test-LbfSame (Get-LbfRefId $r) $rootId) { $made = $true; break } }
        if (-not $made) { continue }
        foreach ($k in @($e.Keys)) {
            if (-not (Test-LbfIn $k @('@id', '@type', 'result'))) { $row["run_$k"] = Get-LbfBrowseLabels $ctx $e[$k] }
        }
    }

    $files = New-Object System.Collections.Generic.List[object]
    $total = [long]0
    foreach ($ref in (Get-LbfAsList (Get-LbfMapValue $root 'hasPart'))) {
        $id = Get-LbfRefId $ref
        if (-not $graph.Contains($id)) { continue }
        $e = $graph[$id]
        if (-not (Test-LbfIn 'File' (Get-LbfTypesOf $e))) { continue }
        $size = ConvertFrom-LbfGoScanInt (Format-LbfGoSprint (Get-LbfMapValue $e 'contentSize'))
        $f = New-LbfOrdered
        $f['path'] = $id
        $f['size'] = [long]$size
        $sha = Get-LbfMapValue $e 'sha256'
        if ($sha -is [string] -and $sha.Length -gt 0) { $f['sha256'] = $sha }
        $files.Add($f)
        $total += $size
    }
    $row['files'] = [int]$files.Count
    $row['size_bytes'] = [long]$total
    $fileList = $null
    if ($files.Count -gt 0) { $fileList = $files.ToArray() }
    return @{ Row = $row; Files = $fileList; Names = $names }
}

function Get-LbfBrowseLabel {
    param([hashtable]$Ctx, [AllowNull()]$Value)
    if ($Value -isnot [System.Collections.IDictionary]) { return Format-LbfGoSprint $Value }
    $id = Format-LbfGoSprint (Get-LbfMapValue $Value '@id')
    $e = $Value
    if ($Ctx.Graph.Contains($id)) { $e = $Ctx.Graph[$id] }
    $hasName = $e.Contains('name')
    $versioned = $e.Contains('version')
    $name = $null
    if ($hasName) { $name = Format-LbfGoSprint $e['name'] }
    if ($hasName -and $versioned) { $name = $name + ' ' + (Format-LbfGoSprint $e['version']) }
    # One IRI for every version, such as a repository, would show every crate the first version seen.
    if ((Test-LbfGoUrlHasScheme $id) -and (-not $versioned -or ($id.IndexOf((Format-LbfGoSprint $e['version']), [StringComparison]::Ordinal) -ge 0))) {
        if ($hasName -and -not (Test-LbfSame (Format-LbfGoSprint $e['name']) $id)) { $Ctx.Names[$id] = $name }
        return $id
    }
    if ($hasName) { return $name }
    foreach ($k in @('identifier', 'value', 'contentUrl')) {
        if ($e.Contains($k)) { return Format-LbfGoSprint $e[$k] }
    }
    return $id
}

function Get-LbfBrowseLabels {
    param([hashtable]$Ctx, [AllowNull()]$Value)
    $parts = New-Object System.Collections.Generic.List[string]
    foreach ($item in (Get-LbfAsList $Value)) { $parts.Add((Get-LbfBrowseLabel $Ctx $item)) }
    if ($parts.Count -eq 0) { return $null }
    if ($parts.Count -eq 1) { return $parts[0] }
    return , ([string[]]$parts.ToArray())
}

function Get-LbfFetchCommand {
    param([string]$Id, [string]$Container, [string]$Tag, [string]$Account)
    $cmd = "lbf fetch $Id"
    if (-not (Test-LbfSame $Container 'bronze')) { $cmd += " --container $Container" }
    if (-not (Test-LbfSame $Tag 'tag=storage')) { $cmd += " --tag $Tag" }
    return "$cmd --account $Account"
}

# ---------- Blob REST with a bearer token ----------

function Get-LbfBrowseAccountUrl {
    param([string]$Account)
    return "https://$Account.blob.core.windows.net"
}

function Get-LbfBrowseToken {
    param($Credential)
    $tok = Get-LbfAccessToken -Credential $Credential -Scope $script:BrowseStorageScope
    if ($tok -is [string]) { return $tok }
    foreach ($n in @('Token', 'AccessToken', 'access_token')) {
        if ($tok -is [System.Collections.IDictionary]) { if ($tok.Contains($n)) { return [string]$tok[$n] } }
        elseif ($null -ne $tok -and $tok.PSObject.Properties[$n]) { return [string]$tok.$n }
    }
    return [string]$tok
}

function New-LbfBrowseRequest {
    param([string]$Url, [string]$Token)
    $req = New-Object System.Net.Http.HttpRequestMessage -ArgumentList ([System.Net.Http.HttpMethod]::Get), $Url
    [void]$req.Headers.TryAddWithoutValidation('Authorization', "Bearer $Token")
    [void]$req.Headers.TryAddWithoutValidation('x-ms-version', $script:BrowseApiVersion)
    [void]$req.Headers.TryAddWithoutValidation('x-ms-date', [DateTime]::UtcNow.ToString('R', $script:Invariant))
    return $req
}

function Get-LbfResponseHeader {
    param($Response, [string]$Name)
    $values = $null
    if ($Response.Headers.TryGetValues($Name, [ref]$values)) { return [string](@($values)[0]) }
    return ''
}

# What azcore's ResponseError says for a failed request.
function New-LbfBlobResponseError {
    param([string]$Url, $Response, [byte[]]$Body)
    $sep = '-' * 80
    $u = New-Object System.Uri -ArgumentList $Url
    $code = Get-LbfResponseHeader $Response 'x-ms-error-code'
    $text = $script:Utf8NoBom.GetString($Body)
    if ($code.Length -eq 0) {
        $m = [regex]::Match($text, '<Code>([^<]*)</Code>')
        if ($m.Success) { $code = $m.Groups[1].Value }
    }
    $status = [int]$Response.StatusCode
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("GET $($u.Scheme)://$($u.Authority)$($u.AbsolutePath)`n$sep`n")
    [void]$sb.Append("RESPONSE ${status}: $status $($Response.ReasonPhrase)`n")
    if ($code.Length -gt 0) { [void]$sb.Append("ERROR CODE: $code`n") } else { [void]$sb.Append("ERROR CODE UNAVAILABLE`n") }
    [void]$sb.Append("$sep`n")
    if ($Body.Length -gt 0) { [void]$sb.Append("$text`n") } else { [void]$sb.Append("Response contained no body`n") }
    [void]$sb.Append("$sep`n")
    $e = New-LbfError $sb.ToString()
    $e.Data['lbfBlobCode'] = $code
    return $e
}

function Test-LbfRetryStatus {
    param([int]$Status)
    return @(408, 429, 500, 502, 503, 504) -contains $Status
}

function Wait-LbfRetry {
    param([int]$Try)
    $ms = [Math]::Min(800 * [Math]::Pow(2, $Try), 60000) * (0.8 + (Get-Random -Maximum 0.5))
    Start-Sleep -Milliseconds ([int]$ms)
}

# One GET, retried as azcore retries: the body, or azcore's error for the response.
function Invoke-LbfBlobGet {
    param($Client, [string]$Url, [string]$Token, $First)
    for ($try = 0; ; $try++) {
        $resp = $null
        try {
            if ($null -ne $First -and $try -eq 0) { $resp = $First.GetAwaiter().GetResult() }
            else { $resp = $Client.SendAsync((New-LbfBrowseRequest $Url $Token)).GetAwaiter().GetResult() }
        } catch {
            if ($try -lt 3) { Wait-LbfRetry $try; continue }
            throw (New-LbfError "Get `"$Url`": $(Get-LbfErrorText $_)")
        }
        try {
            $status = [int]$resp.StatusCode
            if ((Test-LbfRetryStatus $status) -and $try -lt 3) { Wait-LbfRetry $try; continue }
            $body = $resp.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
            if ($status -lt 200 -or $status -ge 300) { throw (New-LbfBlobResponseError $Url $resp $body) }
            return , $body
        } finally { $resp.Dispose() }
    }
}

function Test-LbfBlobCode {
    param($ErrorOrException, [string[]]$Codes)
    $e = Get-LbfException $ErrorOrException
    return $e.Data.Contains('lbfBlobCode') -and (Test-LbfIn ([string]$e.Data['lbfBlobCode']) $Codes)
}

function Get-LbfBlobListing {
    param($Client, [string]$Url, [string]$Token, [string]$Path)
    $names = New-Object System.Collections.Generic.List[string]
    $marker = ''
    while ($true) {
        $u = $Url
        if ($marker.Length -gt 0) { $u += '&marker=' + [Uri]::EscapeDataString($marker) }
        $xml = New-Object System.Xml.XmlDocument
        $xml.XmlResolver = $null
        $xml.LoadXml($script:Utf8NoBom.GetString((Invoke-LbfBlobGet $Client $u $Token $null)).TrimStart([char]0xFEFF))
        foreach ($n in $xml.SelectNodes($Path)) { $names.Add($n.InnerText) }
        $next = $xml.SelectSingleNode('/EnumerationResults/NextMarker')
        if ($null -eq $next -or $next.InnerText.Length -eq 0) { break }
        $marker = $next.InnerText
    }
    return , $names.ToArray()
}

function Get-LbfBrowseContainers {
    param($Client, [string]$Endpoint, [string]$Token)
    return , (Get-LbfBlobListing $Client "$Endpoint/?comp=list" $Token '/EnumerationResults/Containers/Container/Name')
}

function Join-LbfBlobPath {
    param([string]$Path)
    return ((($Path -split '/') | ForEach-Object { [Uri]::EscapeDataString($_) }) -join '/')
}

# Go's syncCrates: crates never change once written, so a cached one is never stale; folders without one are asked again each time.
function Sync-LbfCrates {
    param($Client, [string]$Endpoint, [string]$Container, [string]$Token, [string]$Cache)
    $base = "$Endpoint/$([Uri]::EscapeDataString($Container))"
    $ids = New-Object System.Collections.Generic.List[string]
    foreach ($p in (Get-LbfBlobListing $Client "${base}?restype=container&comp=list&delimiter=%2F" $Token '/EnumerationResults/Blobs/BlobPrefix/Name')) {
        if ($p.EndsWith('/', [StringComparison]::Ordinal)) { $p = $p.Substring(0, $p.Length - 1) }
        $ids.Add($p)
    }
    $queue = New-Object System.Collections.Generic.Queue[hashtable]
    foreach ($id in $ids) {
        $path = Join-LbfCratePath $Cache $id
        if ([IO.File]::Exists($path) -or [IO.Directory]::Exists($path)) { continue }
        $queue.Enqueue(@{ Url = "$base/$(Join-LbfBlobPath ($id + '/' + $script:BrowseCrateName))"; Path = $path })
    }
    $running = New-Object System.Collections.Generic.List[hashtable]
    $failure = $null
    while ($null -eq $failure -and ($queue.Count -gt 0 -or $running.Count -gt 0)) {
        while ($queue.Count -gt 0 -and $running.Count -lt $script:BrowseParallel) {
            $job = $queue.Dequeue()
            $job.Task = $Client.SendAsync((New-LbfBrowseRequest $job.Url $Token))
            $running.Add($job)
        }
        $tasks = [System.Threading.Tasks.Task[]]@($running | ForEach-Object { $_.Task })
        $i = [System.Threading.Tasks.Task]::WaitAny($tasks)
        $job = $running[$i]
        $running.RemoveAt($i)
        try {
            $crate = Invoke-LbfBlobGet $Client $job.Url $Token $job.Task
        } catch {
            if (Test-LbfBlobCode $_ @('BlobNotFound')) { continue }
            $failure = $_
            break
        }
        try { Write-LbfCacheFile $job.Path $crate } catch { $failure = $_ }
    }
    if ($null -ne $failure) { throw $failure }
    return , $ids.ToArray()
}

function Join-LbfCratePath {
    param([string]$Cache, [string]$Id)
    return [IO.Path]::GetFullPath([IO.Path]::Combine([IO.Path]::Combine($Cache, $Id), $script:BrowseCrateName))
}

function Write-LbfCacheFile {
    param([string]$Path, [byte[]]$Bytes)
    $dir = [IO.Path]::GetDirectoryName($Path)
    try { [void][IO.Directory]::CreateDirectory($dir) } catch { throw (New-LbfError (Format-LbfOsError 'mkdir' $dir $_)) }
    try { [IO.File]::WriteAllBytes($Path, $Bytes) } catch { throw (New-LbfError (Format-LbfOsError 'open' $Path $_)) }
}

# ---------- browse ----------

function Get-LbfAccountName {
    param($Match)
    if ($Match -is [string]) { return $Match }
    if ($Match -is [System.Collections.IDictionary]) { return [string]$Match['Name'] }
    return [string]$Match.Name
}

function Invoke-LbfBrowse {
    param([Parameter(Mandatory)][hashtable]$Options)
    $signedIn = Get-LbfCredential -Tenant $Options.tenant
    $cred = $signedIn.Credential
    Set-LbfStatus "Finding the storage account tagged $($Options.tag)"
    try { $found = Find-LbfAccounts -Credential $cred -Tag $Options.tag } finally { Set-LbfStatus '' }
    $chosen = Select-LbfAccount -Matches $found -Tag $Options.tag -Want $Options.account
    $account = Get-LbfAccountName $chosen
    $endpoint = Get-LbfBrowseAccountUrl $account
    $token = $null
    $client = New-LbfHttpClient
    try {
        $containers = @($Options.container.Split([char]',') | Where-Object { $_.Length -gt 0 })
        if ($containers.Count -eq 0) {
            try {
                $token = Get-LbfBrowseToken $cred
                $containers = Get-LbfBrowseContainers $client $endpoint $token
            } catch {
                throw (New-LbfError "listing the containers in $account needs read access to the whole account; name them with --container instead: $(Get-LbfErrorText $_)")
            }
        }
        $cacheRoot = Get-LbfUserCacheDir
        $cat = New-LbfCatalogue
        foreach ($name in $containers) {
            Set-LbfStatus "Reading crates in $name"
            if ($null -eq $token) { $token = Get-LbfBrowseToken $cred }
            $cache = [IO.Path]::Combine($cacheRoot, 'lbf', 'crates', $account, $name)
            try {
                $ids = Sync-LbfCrates $client $endpoint $name $token $cache
            } catch {
                if (Test-LbfBlobCode $_ @('AuthorizationPermissionMismatch', 'AuthorizationFailure')) {
                    Write-LbfLog "Skipping ${name}: you cannot read it`n"
                    continue
                }
                throw
            }
            foreach ($id in $ids) {
                $path = Join-LbfCratePath $cache $id
                try {
                    $raw = [IO.File]::ReadAllBytes($path)
                } catch {
                    $e = Get-LbfException $_
                    if ($e -is [IO.FileNotFoundException] -or $e -is [IO.DirectoryNotFoundException]) { continue }
                    throw (New-LbfError (Format-LbfOsError 'open' $path $_))
                }
                try {
                    $flat = ConvertFrom-LbfBrowseCrate ($script:Utf8NoBom.GetString($raw))
                } catch {
                    Write-LbfLog "Skipping $name/${id}: $(Get-LbfErrorText $_)`n"
                    continue
                }
                Add-LbfCatalogueEntry $cat $flat $id $name (Get-LbfFetchCommand $id $name $Options.tag $account) $path
            }
        }
    } finally { $client.Dispose() }
    Set-LbfStatus ''
    Write-LbfLog "$($cat.Datasets.Count) datasets in $account`n"
    Start-LbfCatalogueServer $cat $account $Options.port
}

function New-LbfCatalogue {
    return @{
        Datasets = (New-Object System.Collections.Generic.List[object])
        Files = (New-LbfMap)
        Labels = (New-LbfMap)
        Crates = (New-LbfMap)
    }
}

function Add-LbfCatalogueEntry {
    param([hashtable]$Cat, [hashtable]$Flat, [string]$Id, [string]$Container, [string]$Fetch, [string]$Path)
    $row = $Flat.Row
    $row['id'] = $Id
    $row['container'] = $Container
    $row['fetch'] = $Fetch
    $Cat.Datasets.Add($row)
    $Cat.Files["$Container/$Id"] = $Flat.Files
    $Cat.Crates["$Container/$Id"] = $Path
    foreach ($iri in @($Flat.Names.Keys)) {
        if (-not $Cat.Labels.ContainsKey($iri) -or ([string]$Cat.Labels[$iri]).Length -eq 0) { $Cat.Labels[$iri] = $Flat.Names[$iri] }
    }
}

function ConvertTo-LbfCatalogueJson {
    param([hashtable]$Cat)
    $doc = New-LbfOrdered
    $doc['datasets'] = $null
    if ($Cat.Datasets.Count -gt 0) { $doc['datasets'] = $Cat.Datasets.ToArray() }
    $doc['files'] = $Cat.Files
    $doc['labels'] = $Cat.Labels
    return ConvertTo-LbfJson $doc
}

# ---------- serving on 127.0.0.1, as catalogueHandler does ----------

# Go's net.Listen error for "127.0.0.1:<port>", or the port number.
function ConvertTo-LbfListenPort {
    param([string]$Port)
    $m = [regex]::Match($Port, '\A([+-]?)([0-9]+)\z')
    if (-not $m.Success) { throw (New-LbfError "listen tcp: lookup tcp/${Port}: unknown port") }
    $n = [decimal]::Parse($m.Groups[2].Value.Substring(0, [Math]::Min(20, $m.Groups[2].Value.Length)), $script:Invariant)
    if ($m.Groups[1].Value -eq '-' -or $n -gt 65535) { throw (New-LbfError "listen tcp: address ${Port}: invalid port") }
    return [int]$n
}

function Format-LbfSocketReason {
    param($ErrorOrException)
    $e = Get-LbfException $ErrorOrException
    if ($script:OnWindows -and $e -is [System.Net.Sockets.SocketException]) { return Get-LbfWin32Message $e.ErrorCode }
    if ($script:OnWindows -or $e -isnot [System.Net.Sockets.SocketException]) { return $e.Message }
    switch ($e.SocketErrorCode) {
        'AddressAlreadyInUse' { return 'address already in use' }
        'AccessDenied' { return 'permission denied' }
        'AddressNotAvailable' { return "can't assign requested address" }
    }
    $m = $e.Message
    if ($m.Length -gt 0) { $m = $m.Substring(0, 1).ToLowerInvariant() + $m.Substring(1) }
    return $m
}

function Open-LbfBrowser {
    param([string]$Url)
    try {
        if ($script:OnWindows) {
            Start-Process -FilePath 'rundll32.exe' -ArgumentList 'url.dll,FileProtocolHandler', $Url -WindowStyle Hidden
        } else {
            $open = 'for b in xdg-open x-www-browser www-browser; do command -v "$b" >/dev/null 2>&1 && exec "$b" "$0" >/dev/null 2>&1; done'
            if (Test-LbfMacOS) { $open = 'exec /usr/bin/open "$0" >/dev/null 2>&1' }
            $psi = New-Object System.Diagnostics.ProcessStartInfo -ArgumentList '/bin/sh'
            $psi.UseShellExecute = $false
            foreach ($a in @('-c', $open, $Url)) { $psi.ArgumentList.Add($a) }
            [void][Diagnostics.Process]::Start($psi)
        }
    } catch { }
}

function Start-LbfCatalogueServer {
    param([hashtable]$Cat, [string]$Account, [string]$Port)
    $data = $script:Utf8NoBom.GetBytes((ConvertTo-LbfCatalogueJson $Cat))
    $page = $script:Utf8NoBom.GetBytes($script:EmbeddedBrowseHtml)
    $n = ConvertTo-LbfListenPort $Port
    $listener = New-Object System.Net.Sockets.TcpListener -ArgumentList ([Net.IPAddress]::Loopback), $n
    try {
        $listener.Start()
    } catch {
        throw (New-LbfError "listen tcp 127.0.0.1:${n}: bind: $(Format-LbfSocketReason $_)")
    }
    $site = @{ Page = $page; Data = $data; Crates = $Cat.Crates }
    try {
        $url = "http://127.0.0.1:$($listener.LocalEndpoint.Port)/"
        Write-LbfOut "Browsing $Account at $url (Ctrl-C to stop)`n"
        Open-LbfBrowser $url
        Invoke-LbfServeLoop $listener $site
    } finally {
        $listener.Stop()
    }
}

# Ctrl-C arrives as a key when the console allows it, so the server stops and lbf exits as Go's does.
function Enable-LbfCtrlCKey {
    try {
        if ([Console]::IsInputRedirected) { return $null }
        $saved = [Console]::TreatControlCAsInput
        [Console]::TreatControlCAsInput = $true
        return @{ Saved = $saved }
    } catch { return $null }
}

function Test-LbfCtrlCPressed {
    param($State)
    if ($null -eq $State) { return $false }
    try {
        while ([Console]::KeyAvailable) {
            $k = [Console]::ReadKey($true)
            if ($k.Key -eq [ConsoleKey]::C -and ($k.Modifiers -band [ConsoleModifiers]::Control)) { return $true }
        }
    } catch { }
    return $false
}

function Invoke-LbfServeLoop {
    param($Listener, [hashtable]$Site)
    $conns = New-Object System.Collections.Generic.List[hashtable]
    $ctrl = Enable-LbfCtrlCKey
    try {
        while ($true) {
            $busy = $false
            while ($Listener.Pending()) {
                $client = $Listener.AcceptTcpClient()
                $client.NoDelay = $true
                $conns.Add(@{ Client = $client; Stream = $client.GetStream(); Buf = (New-Object System.IO.MemoryStream); Seen = [DateTime]::UtcNow })
                $busy = $true
            }
            for ($i = $conns.Count - 1; $i -ge 0; $i--) {
                $c = $conns[$i]
                $drop = $false
                try {
                    $sock = $c.Client.Client
                    $avail = $sock.Available
                    if ($avail -gt 0) {
                        $chunk = New-Object byte[] $avail
                        $got = $c.Stream.Read($chunk, 0, $avail)
                        $c.Buf.Write($chunk, 0, $got)
                        $c.Seen = [DateTime]::UtcNow
                        $busy = $true
                        $drop = -not (Invoke-LbfServeBuffered $c $Site)
                    } elseif ($sock.Poll(0, [System.Net.Sockets.SelectMode]::SelectRead) -and $sock.Available -eq 0) {
                        $drop = $true
                    } elseif (([DateTime]::UtcNow - $c.Seen).TotalSeconds -gt 120) {
                        $drop = $true
                    }
                } catch { $drop = $true; if ($env:LBF_TEST_TRACE) { Write-LbfErr ("$_`n" + $_.ScriptStackTrace + "`n") } }
                if ($drop) {
                    try { $c.Client.Close() } catch { }
                    $conns.RemoveAt($i)
                }
            }
            if (Test-LbfCtrlCPressed $ctrl) { return }
            if (-not $busy) { Start-Sleep -Milliseconds 10 }
        }
    } finally {
        foreach ($c in $conns) { try { $c.Client.Close() } catch { } }
        if ($null -ne $ctrl) { try { [Console]::TreatControlCAsInput = $ctrl.Saved } catch { } }
    }
}

# Answers every complete request in the connection's buffer; false when the connection should close.
function Invoke-LbfServeBuffered {
    param([hashtable]$Conn, [hashtable]$Site)
    while ($true) {
        $bytes = $Conn.Buf.ToArray()
        $text = [Text.Encoding]::GetEncoding(28591).GetString($bytes)
        $end = $text.IndexOf("`r`n`r`n", [StringComparison]::Ordinal)
        if ($end -lt 0) {
            if ($bytes.Length -gt 1MB) {
                Send-LbfHttpResponse $Conn.Stream (New-LbfHttpResponse 431 'text/plain; charset=utf-8' ([Text.Encoding]::ASCII.GetBytes('431 Request Header Fields Too Large'))) $false $true
                return $false
            }
            return $true
        }
        $rest = New-Object System.IO.MemoryStream
        $rest.Write($bytes, $end + 4, $bytes.Length - $end - 4)
        $Conn.Buf = $rest
        $lines = $text.Substring(0, $end) -split "`r`n"
        $req = @{ Headers = @{} }
        $first = $lines[0] -split ' '
        if ($first.Count -ne 3 -or $first[2] -cnotmatch '\AHTTP/1\.[01]\z') {
            Send-LbfHttpResponse $Conn.Stream (New-LbfHttpResponse 400 'text/plain; charset=utf-8' ([Text.Encoding]::ASCII.GetBytes('400 Bad Request'))) $false $true
            return $false
        }
        $req.Method = $first[0]; $req.Target = $first[1]; $req.Proto = $first[2]
        for ($i = 1; $i -lt $lines.Count; $i++) {
            $colon = $lines[$i].IndexOf([char]':')
            if ($colon -gt 0) { $req.Headers[$lines[$i].Substring(0, $colon).Trim().ToLowerInvariant()] = $lines[$i].Substring($colon + 1).Trim() }
        }
        $close = Test-LbfSame $req.Proto 'HTTP/1.0'
        if ($req.Headers.ContainsKey('connection')) {
            $connHeader = $req.Headers['connection']
            if ([string]::Equals($connHeader, 'close', [StringComparison]::OrdinalIgnoreCase)) { $close = $true }
            elseif ([string]::Equals($connHeader, 'keep-alive', [StringComparison]::OrdinalIgnoreCase)) { $close = $false }
        }
        $hasBody = $req.Headers.ContainsKey('transfer-encoding') -or ($req.Headers.ContainsKey('content-length') -and -not (Test-LbfSame $req.Headers['content-length'] '0'))
        if ($hasBody) { $close = $true }
        $resp = Get-LbfCatalogueResponse $req $Site
        Send-LbfHttpResponse $Conn.Stream $resp (Test-LbfSame $req.Method 'HEAD') $close
        if ($close) { return $false }
    }
}

function New-LbfHttpResponse {
    param([int]$Status, [string]$ContentType, [byte[]]$Body, [hashtable]$Headers = @{})
    $h = @{}
    foreach ($k in $Headers.Keys) { $h[$k] = $Headers[$k] }
    if ($ContentType -ne '') { $h['Content-Type'] = $ContentType }
    if ($null -eq $Body) { $Body = [byte[]]@() }
    return @{ Status = $Status; Headers = $h; Body = $Body }
}

function New-LbfHttpTextResponse {
    param([int]$Status, [string]$Text, [hashtable]$Headers = @{})
    $Headers['X-Content-Type-Options'] = 'nosniff'
    return New-LbfHttpResponse $Status 'text/plain; charset=utf-8' ([Text.Encoding]::ASCII.GetBytes($Text + "`n")) $Headers
}

$script:HttpStatusText = @{
    200 = 'OK'; 304 = 'Not Modified'; 307 = 'Temporary Redirect'; 400 = 'Bad Request'; 403 = 'Forbidden'; 404 = 'Not Found'
    405 = 'Method Not Allowed'; 431 = 'Request Header Fields Too Large'; 500 = 'Internal Server Error'
}

function Send-LbfHttpResponse {
    param($Stream, [hashtable]$Response, [bool]$Head, [bool]$Close)
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("HTTP/1.1 $($Response.Status) $($script:HttpStatusText[$Response.Status])`r`n")
    $h = $Response.Headers
    $h['Date'] = [DateTime]::UtcNow.ToString('R', $script:Invariant)
    if ($Response.Status -ne 304) { $h['Content-Length'] = $Response.Body.Length.ToString($script:Invariant) }
    if ($Close) { $h['Connection'] = 'close' }
    foreach ($k in $h.Keys) { [void]$sb.Append("${k}: $($h[$k])`r`n") }
    [void]$sb.Append("`r`n")
    $headBytes = [Text.Encoding]::ASCII.GetBytes($sb.ToString())
    $Stream.Write($headBytes, 0, $headBytes.Length)
    if (-not $Head -and $Response.Status -ne 304 -and $Response.Body.Length -gt 0) { $Stream.Write($Response.Body, 0, $Response.Body.Length) }
    $Stream.Flush()
}

# Go's cleanPath: path.Clean, keeping a trailing slash.
function Get-LbfCleanUrlPath {
    param([string]$Path)
    if ($Path.Length -eq 0) { return '/' }
    if (-not $Path.StartsWith('/', [StringComparison]::Ordinal)) { $Path = '/' + $Path }
    $out = New-Object System.Collections.Generic.List[string]
    foreach ($seg in ($Path -split '/')) {
        if ($seg.Length -eq 0 -or (Test-LbfSame $seg '.')) { continue }
        if (Test-LbfSame $seg '..') { if ($out.Count -gt 0) { $out.RemoveAt($out.Count - 1) }; continue }
        $out.Add($seg)
    }
    $clean = '/' + ($out -join '/')
    if ($Path.EndsWith('/', [StringComparison]::Ordinal) -and -not (Test-LbfSame $clean '/')) { $clean += '/' }
    return $clean
}

function ConvertFrom-LbfUrlEscapes {
    param([string]$Text)
    if (-not (Test-LbfUrlUnescapable $Text)) { return $null }
    $bytes = New-Object System.Collections.Generic.List[byte]
    for ($i = 0; $i -lt $Text.Length; $i++) {
        if ($Text[$i] -eq [char]'%') { $bytes.Add([Convert]::ToByte($Text.Substring($i + 1, 2), 16)); $i += 2 }
        else { $bytes.Add([byte][int]$Text[$i]) }
    }
    return $script:Utf8NoBom.GetString($bytes.ToArray())
}

function ConvertTo-LbfHtmlEscaped {
    param([string]$Text)
    return $Text.Replace('&', '&amp;').Replace('<', '&lt;').Replace('>', '&gt;').Replace('"', '&#34;').Replace("'", '&#39;')
}

# Go's catalogueHandler on a ServeMux: GET / for the page, /catalogue.json, and /crate/{container}/{id} for one crate.
function Get-LbfCatalogueResponse {
    param([hashtable]$Req, [hashtable]$Site)
    $target = $Req.Target
    if ((Test-LbfSame $Req.Method 'OPTIONS') -and (Test-LbfSame $target '*')) { return New-LbfHttpResponse 200 '' $null }
    $m = [regex]::Match($target, '^[A-Za-z][A-Za-z0-9+.-]*://[^/]*')
    if ($m.Success) { $target = $target.Substring($m.Length) }
    $query = ''
    $q = $target.IndexOf([char]'?')
    if ($q -ge 0) { $query = $target.Substring($q + 1); $target = $target.Substring(0, $q) }
    if ($null -eq (ConvertFrom-LbfUrlEscapes $target) -or -not $target.StartsWith('/', [StringComparison]::Ordinal)) { return New-LbfHttpTextResponse 400 '400 Bad Request' }
    $clean = Get-LbfCleanUrlPath $target
    if (-not (Test-LbfSame $clean $target)) {
        $loc = $clean
        if ($query.Length -gt 0) { $loc += '?' + $query }
        $body = $null
        $type = ''
        if (Test-LbfIn $Req.Method @('GET', 'HEAD')) { $type = 'text/html; charset=utf-8' }
        if (Test-LbfSame $Req.Method 'GET') { $body = $script:Utf8NoBom.GetBytes("<a href=`"$(ConvertTo-LbfHtmlEscaped $loc)`">Temporary Redirect</a>.`n`n") }
        return New-LbfHttpResponse 307 $type $body @{ Location = $loc }
    }
    $segs = @($target.Substring(1) -split '/')
    $route = ''
    if (Test-LbfSame $target '/') { $route = 'page' }
    elseif ($segs.Count -eq 1 -and (Test-LbfSame (ConvertFrom-LbfUrlEscapes $segs[0]) 'catalogue.json')) { $route = 'data' }
    elseif ($segs.Count -eq 3 -and (Test-LbfSame (ConvertFrom-LbfUrlEscapes $segs[0]) 'crate') -and $segs[1].Length -gt 0 -and $segs[2].Length -gt 0) { $route = 'crate' }
    if ($route.Length -eq 0) { return New-LbfHttpTextResponse 404 '404 page not found' }
    if (-not (Test-LbfIn $Req.Method @('GET', 'HEAD'))) { return New-LbfHttpTextResponse 405 'Method Not Allowed' @{ Allow = 'GET, HEAD' } }
    switch -CaseSensitive ($route) {
        'page' { return New-LbfHttpResponse 200 'text/html; charset=utf-8' $Site.Page }
        'data' { return New-LbfHttpResponse 200 'application/json' $Site.Data }
    }
    $key = (ConvertFrom-LbfUrlEscapes $segs[1]) + '/' + (ConvertFrom-LbfUrlEscapes $segs[2])
    if (-not $Site.Crates.ContainsKey($key)) { return New-LbfHttpTextResponse 404 '404 page not found' }
    return Get-LbfCrateFileResponse $Req $Site.Crates[$key]
}

# Go's http.ServeFile for a crate whose Content-Type is already set, without byte ranges.
function Get-LbfCrateFileResponse {
    param([hashtable]$Req, [string]$Path)
    try {
        $bytes = [IO.File]::ReadAllBytes($Path)
        $mod = [IO.File]::GetLastWriteTimeUtc($Path)
    } catch {
        $e = Get-LbfException $_
        if ($e -is [IO.FileNotFoundException] -or $e -is [IO.DirectoryNotFoundException]) { return New-LbfHttpTextResponse 404 '404 page not found' }
        if ($e -is [UnauthorizedAccessException]) { return New-LbfHttpTextResponse 403 '403 Forbidden' }
        return New-LbfHttpTextResponse 500 '500 Internal Server Error'
    }
    $mod = New-Object DateTime -ArgumentList ($mod.Ticks - $mod.Ticks % [TimeSpan]::TicksPerSecond), ([DateTimeKind]::Utc)
    $lastModified = $mod.ToString('R', $script:Invariant)
    if ($Req.Headers.ContainsKey('if-modified-since') -and -not $Req.Headers.ContainsKey('if-none-match')) {
        $since = [DateTime]::MinValue
        $styles = [Globalization.DateTimeStyles]::AdjustToUniversal -bor [Globalization.DateTimeStyles]::AssumeUniversal
        if ([DateTime]::TryParseExact($Req.Headers['if-modified-since'], 'r', $script:Invariant, $styles, [ref]$since) -and $mod -le $since) {
            return New-LbfHttpResponse 304 '' $null @{ 'Last-Modified' = $lastModified }
        }
    }
    return New-LbfHttpResponse 200 'application/json' $bytes @{ 'Last-Modified' = $lastModified; 'Accept-Ranges' = 'bytes' }
}
