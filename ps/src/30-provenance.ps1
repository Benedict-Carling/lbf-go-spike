$script:LbfProperties = [string[]]@('subscription_name', 'subscription_id', 'source_path')

function New-LbfInstrumentValue {
    param([string]$Name = '', [string]$Version = '', [string]$URL = '')
    return @{ Name = $Name; Version = $Version; URL = $URL }
}

function New-LbfProvenanceValue {
    return @{
        Name = ''; Description = ''; DerivedFrom = ''
        Instruments = (New-Object System.Collections.Generic.List[hashtable])
        Properties = (New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal))
    }
}

# Go's parseInstrument: --instrument name=NAME,version=VERSION,url=URL; a comma not followed by a known key stays in the value.
function ConvertTo-LbfInstrument {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $in = New-LbfInstrumentValue
    $last = $null
    foreach ($part in $Text.Split([char]',')) {
        $eq = $part.IndexOf([char]'=')
        if ($eq -ge 0) {
            $field = $null
            switch -CaseSensitive ($part.Substring(0, $eq)) {
                'name' { $field = 'Name' }
                'version' { $field = 'Version' }
                'url' { $field = 'URL' }
            }
            if ($null -ne $field) {
                $in[$field] = $part.Substring($eq + 1)
                $last = $field
                continue
            }
        }
        if ($null -eq $last) { throw (New-LbfError "$(Format-LbfQuote $Text) is not name=NAME,version=VERSION,url=URL") }
        $in[$last] = $in[$last] + ',' + $part
    }
    return $in
}

# The JSON Go writes for the provenance struct, as a --provenance file has it.
function ConvertTo-LbfProvenanceValue {
    param([Parameter(Mandatory)][hashtable]$Provenance)
    $o = New-LbfOrdered
    if ($Provenance.Name -ne '') { $o['name'] = $Provenance.Name }
    if ($Provenance.Description -ne '') { $o['description'] = $Provenance.Description }
    if ($Provenance.DerivedFrom -ne '') { $o['derived_from'] = $Provenance.DerivedFrom }
    if ($null -ne $Provenance.Instruments -and $Provenance.Instruments.Count -gt 0) {
        $list = New-Object System.Collections.Generic.List[object]
        foreach ($in in $Provenance.Instruments) {
            $i = New-LbfOrdered
            $i['name'] = $in.Name
            $i['version'] = $in.Version
            $i['url'] = $in.URL
            $list.Add($i)
        }
        $o['instruments'] = $list
    }
    if ($null -eq $Provenance.Properties) { $o['properties'] = $null }
    else {
        $m = New-LbfMap
        foreach ($k in $Provenance.Properties.Keys) { $m[$k] = $Provenance.Properties[$k] }
        $o['properties'] = $m
    }
    return , $o
}

# Go's provenance.sameAs: instruments compared as a set.
function Test-LbfSameProvenance {
    param([hashtable]$A, [hashtable]$B)
    if ($A.Name -cne $B.Name -or $A.Description -cne $B.Description -or $A.DerivedFrom -cne $B.DerivedFrom) { return $false }
    $sa = Get-LbfInstrumentSet $A.Instruments
    $sb = Get-LbfInstrumentSet $B.Instruments
    if ($sa.Count -ne $sb.Count) { return $false }
    for ($i = 0; $i -lt $sa.Count; $i++) { if ($sa[$i] -cne $sb[$i]) { return $false } }
    $pa = $A.Properties; $pb = $B.Properties
    $na = 0; $nb = 0
    if ($null -ne $pa) { $na = $pa.Count }
    if ($null -ne $pb) { $nb = $pb.Count }
    if ($na -ne $nb) { return $false }
    if ($na -eq 0) { return $true }
    foreach ($k in $pa.Keys) {
        if (-not $pb.ContainsKey($k) -or $pb[$k] -cne $pa[$k]) { return $false }
    }
    return $true
}

function Get-LbfInstrumentSet {
    param($Instruments)
    $keys = New-Object System.Collections.Generic.List[string]
    if ($null -ne $Instruments) {
        foreach ($in in $Instruments) { $keys.Add($in.URL + [char]0 + $in.Name + [char]0 + $in.Version) }
    }
    $arr = $keys.ToArray()
    [Array]::Sort($arr, [StringComparer]::Ordinal)
    $out = New-Object System.Collections.Generic.List[string]
    foreach ($k in $arr) { if ($out.Count -eq 0 -or $out[$out.Count - 1] -cne $k) { $out.Add($k) } }
    return , $out
}

# Go's provenanceFlags.load.
function Get-LbfProvenance {
    param([Parameter(Mandatory)][hashtable]$Flags)
    $p = New-LbfProvenanceValue
    $p.DerivedFrom = [string]$Flags.derivedFrom
    foreach ($in in $Flags.instruments) { $p.Instruments.Add($in) }
    if ($Flags.file -ne '') {
        if ($Flags.derivedFrom -ne '' -or $Flags.instruments.Count -gt 0 -or $Flags.properties -ne '') {
            throw (New-LbfError '--provenance cannot be combined with --derived-from, --instrument or --properties')
        }
        $p = Read-LbfProvenanceFile $Flags.file
    }
    if ($Flags.properties -ne '') {
        try {
            $props = Read-LbfStrictJson $Flags.properties 'properties'
            if ($null -eq $props) { throw (New-LbfError 'want a JSON object of property names and values') }
        } catch {
            throw (New-LbfError "$($Flags.properties) is not a valid properties file: $(Get-LbfErrorText $_)")
        }
        $p.Properties = $props
    }
    foreach ($kv in $Flags.props) {
        $eq = $kv.IndexOf([char]'=')
        if ($eq -le 0) { throw (New-LbfError "--property $(Format-LbfQuote $kv) is not NAME=VALUE") }
        $name = $kv.Substring(0, $eq)
        if ($p.Properties.ContainsKey($name)) { throw (New-LbfError "property $(Format-LbfQuote $name) is given twice") }
        $p.Properties[$name] = $kv.Substring($eq + 1)
    }
    foreach ($field in @(@('name', 'Name', $Flags.name), @('description', 'Description', $Flags.description))) {
        $value = [string]$field[2]
        if ($value -ne '' -and $p[$field[1]] -ne '') { throw (New-LbfError "--$($field[0]) is also given in $($Flags.file)") }
        if ($p[$field[1]] -eq '') { $p[$field[1]] = $value }
    }
    Assert-LbfProvenance $p
    return $p
}

# Go's readProvenance.
function Read-LbfProvenanceFile {
    param([Parameter(Mandatory)][string]$Path)
    try {
        $p = Read-LbfStrictJson $Path 'provenance'
        Assert-LbfProvenance $p
    } catch {
        throw (New-LbfError "$Path is not a valid provenance file: $(Get-LbfErrorText $_)")
    }
    if ($null -eq $p.Properties) { $p.Properties = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal) }
    return $p
}

# Go's provenance.check.
function Assert-LbfProvenance {
    param([Parameter(Mandatory)][hashtable]$P)
    $n = 0
    if ($null -ne $P.Instruments) { $n = $P.Instruments.Count }
    if (($P.DerivedFrom -eq '') -ne ($n -eq 0)) { throw (New-LbfError 'derived_from and instruments are given together or not at all') }
    if ($P.DerivedFrom -ne '' -and -not (Test-LbfMintedId $P.DerivedFrom)) {
        throw (New-LbfError "derived_from $(Format-LbfQuote $P.DerivedFrom) is not a dataset ID such as 20261001-fancy-dassie-eadb")
    }
    $byUrl = New-Object 'System.Collections.Generic.Dictionary[string,int]' ([StringComparer]::Ordinal)
    for ($i = 0; $i -lt $n; $i++) {
        $in = $P.Instruments[$i]
        if ($in.Name -eq '' -or $in.Version -eq '' -or $in.URL -eq '') { throw (New-LbfError "instruments[$i] needs a name, version and url") }
        if (-not (Test-LbfAbsoluteUrl $in.URL)) { throw (New-LbfError "instruments[$i] url $(Format-LbfQuote $in.URL) is not an absolute URL") }
        $j = 0
        if ($byUrl.TryGetValue($in.URL, [ref]$j)) {
            $o = $P.Instruments[$j]
            if ($o.Name -cne $in.Name -or $o.Version -cne $in.Version) {
                throw (New-LbfError ("instruments[$j] ($($o.Name) $($o.Version)) and instruments[$i] ($($in.Name) $($in.Version)) share the url $($in.URL), " +
                    'which the crate uses to tell tools apart; give each its own url'))
            }
        }
        $byUrl[$in.URL] = $i
    }
    $props = $P.Properties
    if ($null -eq $props) { return }
    foreach ($k in $script:LbfProperties) {
        if ($props.ContainsKey($k)) { throw (New-LbfError "property $(Format-LbfQuote $k) is set by lbf and cannot be supplied") }
    }
    $names = New-Object string[] $props.Count
    $props.Keys.CopyTo($names, 0)
    [Array]::Sort($names, [StringComparer]::Ordinal)
    foreach ($k in $names) {
        if ($k -ceq 'uploader' -or $k -ceq 'blob-location' -or $k -ceq 'run' -or $k.StartsWith('source-', [StringComparison]::Ordinal) -or $k.StartsWith('@', [StringComparison]::Ordinal)) {
            throw (New-LbfError "property $(Format-LbfQuote $k) would clash with an entity or keyword in the crate; choose another name")
        }
    }
}

# Go's url.Parse(s) succeeding with a scheme.
function Test-LbfAbsoluteUrl {
    param([string]$Text)
    if ($Text -match '[\x00-\x1F\x7F]') { return $false }
    $m = [regex]::Match($Text, '\A([A-Za-z][A-Za-z0-9+.\-]*):')
    if (-not $m.Success) { return $false }
    $rest = $Text.Substring($m.Length)
    $frag = ''
    $hash = $rest.IndexOf([char]'#')
    if ($hash -ge 0) { $frag = $rest.Substring($hash + 1); $rest = $rest.Substring(0, $hash) }
    if (-not (Test-LbfUrlEscapes $frag)) { return $false }
    $q = $rest.IndexOf([char]'?')
    if ($q -ge 0) { $rest = $rest.Substring(0, $q) }
    if (-not $rest.StartsWith('/', [StringComparison]::Ordinal)) { return $true }
    if ($rest.StartsWith('//', [StringComparison]::Ordinal)) {
        $auth = $rest.Substring(2)
        $slash = $auth.IndexOf([char]'/')
        $path = ''
        if ($slash -ge 0) { $path = $auth.Substring($slash); $auth = $auth.Substring(0, $slash) }
        $at = $auth.LastIndexOf([char]'@')
        if ($at -ge 0) {
            $user = $auth.Substring(0, $at)
            if ($user -match '[^A-Za-z0-9\-._:~!$&''()*+,;=%@]' -or -not (Test-LbfUrlEscapes $user)) { return $false }
            $auth = $auth.Substring($at + 1)
        }
        if (-not (Test-LbfUrlHost $auth)) { return $false }
        return Test-LbfUrlEscapes $path
    }
    return Test-LbfUrlEscapes $rest
}

function Test-LbfUrlEscapes {
    param([string]$Text)
    return -not ($Text -match '%(?![0-9A-Fa-f]{2})')
}

function Test-LbfUrlHost {
    param([string]$HostPort)
    $hostPart = $HostPort
    $port = ''
    if ($HostPort.StartsWith('[', [StringComparison]::Ordinal)) {
        $close = $HostPort.LastIndexOf([char]']')
        if ($close -lt 0) { return $false }
        $port = $HostPort.Substring($close + 1)
        $hostPart = $HostPort.Substring(1, $close - 1)
        if ($port -ne '' -and -not $port.StartsWith(':', [StringComparison]::Ordinal)) { return $false }
    } else {
        $colon = $HostPort.LastIndexOf([char]':')
        if ($colon -ge 0) { $port = $HostPort.Substring($colon); $hostPart = $HostPort.Substring(0, $colon) }
    }
    if ($port -ne '' -and $port -notmatch '\A:[0-9]*\z') { return $false }
    if (-not (Test-LbfUrlEscapes $hostPart)) { return $false }
    foreach ($ch in $hostPart.ToCharArray()) {
        $c = [int]$ch
        if ($c -ge 0x80) { continue }
        if (($c -ge 0x30 -and $c -le 0x39) -or ($c -ge 0x41 -and $c -le 0x5A) -or ($c -ge 0x61 -and $c -le 0x7A)) { continue }
        if ('-._~!$&''()*+,;=:[]<>"%'.IndexOf($ch) -ge 0) { continue }
        return $false
    }
    return $true
}

# ---------- encoding/json/v2 strict decoding, with its error texts ----------

function New-LbfJsonFields {
    param([string[]]$Spec)
    $d = New-Object 'System.Collections.Generic.Dictionary[string,string[]]' ([StringComparer]::Ordinal)
    foreach ($f in $Spec) { $p = $f.Split([char]':'); $d[$p[0]] = [string[]]@($p[1], $p[2]) }
    return , $d
}

$script:JsonTypes = @{
    string = @{ Kind = 'string'; Go = 'string' }
    properties = @{ Kind = 'map'; Go = 'map[string]string' }
    instrument = @{ Kind = 'struct'; Go = 'main.instrument'; Fields = (New-LbfJsonFields 'name:Name:string', 'version:Version:string', 'url:URL:string') }
    instruments = @{ Kind = 'slice'; Go = '[]main.instrument'; Elem = 'instrument' }
    provenance = @{ Kind = 'struct'; Go = 'main.provenance'; Fields = (New-LbfJsonFields 'name:Name:string', 'description:Description:string',
            'derived_from:DerivedFrom:string', 'instruments:Instruments:instruments', 'properties:Properties:properties') }
}

# Go's decodeStrict: jsonv2.Unmarshal with RejectUnknownMembers, a leading byte-order mark dropped.
function Read-LbfStrictJson {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Type)
    try {
        $raw = [IO.File]::ReadAllBytes((Get-LbfFullPath $Path))
    } catch {
        throw (New-LbfError (Format-LbfOsError 'open' $Path $_))
    }
    $start = 0
    if ($raw.Length -ge 3 -and $raw[0] -eq 0xEF -and $raw[1] -eq 0xBB -and $raw[2] -eq 0xBF) { $start = 3 }
    $bytes = New-Object byte[] ($raw.Length - $start)
    [Array]::Copy($raw, $start, $bytes, 0, $bytes.Length)
    $d = New-LbfJsonDecoder $bytes
    $v = Read-LbfJsonInto $d $Type
    $pos = Skip-LbfJsonSpace $d $d.PrevEnd
    if ($pos -lt $d.B.Length) {
        throw (New-LbfJsonSyntaxError $d (New-LbfInvalidChar $d $pos 'after top-level value') $pos 0)
    }
    return , $v
}

function New-LbfJsonDecoder {
    param([byte[]]$Bytes)
    $stack = New-Object System.Collections.Generic.List[hashtable]
    $stack.Add(@{ Obj = $false; Len = 0; Name = ''; Ns = $null })
    return @{ B = $Bytes; PrevEnd = 0; Stack = $stack; PeekPos = -1; PeekErr = $null; Legacy = $false }
}

function New-LbfGoErr {
    param([string]$Text, [string]$Kind = '')
    return @{ Text = $Text; Kind = $Kind; Suffix = (New-Object System.Collections.Generic.List[string]) }
}

function Skip-LbfJsonSpace {
    param([hashtable]$D, [int]$Pos)
    $b = $D.B
    while ($Pos -lt $b.Length -and ($b[$Pos] -eq 0x20 -or $b[$Pos] -eq 0x09 -or $b[$Pos] -eq 0x0D -or $b[$Pos] -eq 0x0A)) { $Pos++ }
    return $Pos
}

# Go's strconv.QuoteRune of the rune at b[pos:], or '\xff' for a byte that starts no rune.
function Format-LbfQuoteRune {
    param([byte[]]$B, [int]$Pos)
    $r = Get-LbfUtf8Rune $B $Pos
    if ($r[0] -lt 0) { return "'\x" + ([int]$B[$Pos]).ToString('x') + "'" }
    $q = Format-LbfQuote ([char]::ConvertFromUtf32($r[0]))
    $inner = $q.Substring(1, $q.Length - 2).Replace('\"', '"')
    if ($inner -eq "'") { $inner = "\'" }
    return "'" + $inner + "'"
}

# The rune at b[pos:] and its length in bytes; rune -1 when invalid, -2 when cut short.
function Get-LbfUtf8Rune {
    param([byte[]]$B, [int]$Pos)
    $c = [int]$B[$Pos]
    if ($c -lt 0x80) { return @($c, 1) }
    $need = 0; $min = 0; $r = 0
    if ($c -ge 0xC2 -and $c -le 0xDF) { $need = 1; $r = $c -band 0x1F; $min = 0x80 }
    elseif ($c -ge 0xE0 -and $c -le 0xEF) { $need = 2; $r = $c -band 0x0F; $min = 0x800 }
    elseif ($c -ge 0xF0 -and $c -le 0xF4) { $need = 3; $r = $c -band 0x07; $min = 0x10000 }
    else { return @(-1, 1) }
    for ($i = 1; $i -le $need; $i++) {
        if ($Pos + $i -ge $B.Length) { return @(-2, 1) }
        $cc = [int]$B[$Pos + $i]
        if (($cc -band 0xC0) -ne 0x80) { return @(-1, 1) }
        $r = ($r -shl 6) -bor ($cc -band 0x3F)
    }
    if ($r -lt $min -or $r -gt 0x10FFFF -or ($r -ge 0xD800 -and $r -le 0xDFFF)) { return @(-1, 1) }
    return @($r, ($need + 1))
}

function New-LbfInvalidChar {
    param([hashtable]$D, [int]$Pos, [string]$Where)
    return New-LbfGoErr "invalid character $(Format-LbfQuoteRune $D.B $Pos) $Where"
}

# jsonwire.InvalidTextError for escape sequences.
function New-LbfInvalidEscape {
    param([hashtable]$D, [int]$From, [int]$To)
    if ($To -gt $D.B.Length) { $To = $D.B.Length }
    $what = $script:Utf8NoBom.GetString($D.B, $From, $To - $From)
    $label = 'escape sequence'
    if ($To - $From -gt 6) { $label = 'surrogate pair' }
    if ($what -match '[`\s\uFFFD]' -or -not (Test-LbfAllPrint $what)) { $shown = Format-LbfQuote $what } else { $shown = '`' + $what + '`' }
    return New-LbfGoErr "invalid $label $shown in string"
}

function Test-LbfAllPrint {
    param([string]$Text)
    foreach ($r in (Get-LbfRunes $Text)) { if (-not (Test-LbfPrintRune $r)) { return $false } }
    return $true
}

function Get-LbfJsontextKind {
    param([hashtable]$D, [int]$Pos)
    if ($Pos -ge $D.B.Length) { return 'x' }
    $c = [char]$D.B[$Pos]
    switch -CaseSensitive ($c) {
        '"' { return '"' }
        '{' { return '{' }
        '}' { return '}' }
        '[' { return '[' }
        ']' { return ']' }
        'n' { return 'n' }
        't' { return 't' }
        'f' { return 'f' }
        '-' { return '0' }
    }
    if ($c -ge '0' -and $c -le '9') { return '0' }
    return 'x'
}

function Get-LbfJsonLast {
    param([hashtable]$D)
    return $D.Stack[$D.Stack.Count - 1]
}

function Get-LbfJsonNeedDelim {
    param([hashtable]$D, [string]$Next)
    $last = Get-LbfJsonLast $D
    $needValue = $last.Obj -and ($last.Len % 2) -eq 1
    if ($needValue) { return ':' }
    if ($last.Len -gt 0 -and $Next -ne '}' -and $Next -ne ']' -and $D.Stack.Count -gt 1) { return ',' }
    return ''
}

# jsontext's appendStackPointer: where -1 is the previous value, 0 the current container, +1 the next value.
function Get-LbfJsonPointer {
    param([hashtable]$D, [int]$Where)
    $sb = New-Object System.Text.StringBuilder
    $depth = $D.Stack.Count
    for ($i = 1; $i -lt $depth; $i++) {
        $e = $D.Stack[$i]
        $delta = -1
        if ($i -eq $depth - 1) {
            $needValue = $e.Obj -and ($e.Len % 2) -eq 1
            $needName = $e.Obj -and ($e.Len % 2) -eq 0
            if (($Where -lt 0 -and $e.Len -eq 0) -or ($Where -eq 0 -and -not $needValue) -or ($Where -gt 0 -and $needName)) { break }
            if ($Where -gt 0 -and -not $e.Obj) { $delta = 0 }
        }
        if ($e.Obj) { [void]$sb.Append('/').Append((ConvertTo-LbfPointerToken $e.Name)) }
        else { [void]$sb.Append('/').Append(($e.Len + $delta).ToString($script:Invariant)) }
    }
    return $sb.ToString()
}

function ConvertTo-LbfPointerToken {
    param([string]$Name)
    return $Name.Replace('~', '~0').Replace('/', '~1')
}

function Get-LbfPointerLast {
    param([string]$Pointer)
    $i = $Pointer.LastIndexOf([char]'/')
    $last = $Pointer
    if ($i -ge 0) { $last = $Pointer.Substring($i + 1) }
    return $last.Replace('~1', '/').Replace('~0', '~')
}

function Get-LbfPointerParent {
    param([string]$Pointer)
    $i = $Pointer.LastIndexOf([char]'/')
    if ($i -le 0) { return '' }
    return $Pointer.Substring(0, $i)
}

# jsonwire.TruncatePointer(p, 100), quoted.
function Format-LbfPointer {
    param([string]$Pointer)
    $b = $script:Utf8NoBom.GetBytes($Pointer)
    if ($b.Length -le 100) { return Format-LbfQuote $Pointer }
    $i = 50
    $j = $b.Length - 50
    $k = [Array]::LastIndexOf($b, [byte]0x2F, $i - 1)
    if ($k -gt 0) { $i = $k }
    $k = [Array]::IndexOf($b, [byte]0x2F, $j)
    if ($k -ge 0) { $j = $k + 1 }
    while ($i -gt 0 -and $i -lt $b.Length -and ($b[$i] -band 0xC0) -eq 0x80) { $i-- }
    while ($j -lt $b.Length -and ($b[$j] -band 0xC0) -eq 0x80) { $j++ }
    $mid = $script:Utf8NoBom.GetString($b, $i, $j - $i)
    $ell = [string][char]0x2026
    $slashes = $mid.Split([char]'/').Length - 1
    if ($slashes -eq 0) { $middle = $ell }
    elseif ($slashes -eq 1) { $middle = $ell + '/' + $ell }
    else { $middle = $ell + '/' + $ell + '/' + $ell }
    if ($slashes -gt 0 -and $mid.StartsWith('/', [StringComparison]::Ordinal)) { $middle = $middle.Substring(1) }
    if ($slashes -gt 0 -and $mid.EndsWith('/', [StringComparison]::Ordinal)) { $middle = $middle.Substring(0, $middle.Length - 1) }
    return Format-LbfQuote ($script:Utf8NoBom.GetString($b, 0, $i) + $middle + $script:Utf8NoBom.GetString($b, $j, $b.Length - $j))
}

# jsontext.SyntacticError, as jsontext's wrapSyntacticError builds it.
function New-LbfJsonSyntaxError {
    param([hashtable]$D, [hashtable]$Err, [int]$Pos, [int]$Where)
    $ptr = Get-LbfJsonPointer $D $Where
    for ($i = $Err.Suffix.Count - 1; $i -ge 0; $i--) { $ptr += '/' + $Err.Suffix[$i] }
    $text = $Err.Text
    if ($Err.Kind -eq 'mismatch') {
        $place = 'at start of value'
        $last = Get-LbfJsonLast $D
        if ($D.Stack.Count -gt 1 -and $last.Len -gt 0) {
            if ($last.Obj) { $place = "after object value (expecting ',' or '}')" } else { $place = "after array element (expecting ',' or ']')" }
            $ptr = Get-LbfPointerParent $ptr
        }
        $text = (New-LbfInvalidChar $D $Pos $place).Text
    }
    return New-LbfJsonError2 $text $ptr $Pos ($Err.Kind -eq 'duplicate')
}

function New-LbfJsonError2 {
    param([string]$Text, [string]$Pointer, [long]$Offset, [bool]$Duplicate)
    $msg = 'jsontext: ' + $Text
    if ($Duplicate) {
        $msg += ' ' + (Format-LbfQuote (Get-LbfPointerLast $Pointer))
        $Pointer = Get-LbfPointerParent $Pointer
        $Offset = 0
    }
    if ($Pointer -ne '') { $msg += ' within ' + (Format-LbfPointer $Pointer) }
    if ($Offset -gt 0) { $msg += ' after offset ' + $Offset }
    return New-LbfError $msg
}

function New-LbfJsonCheckDelim {
    param([hashtable]$D, [string]$Delim, [string]$Next)
    $where = 'at start of value'
    switch ((Get-LbfJsonNeedDelim $D $Next)) {
        ':' { $where = "after object name (expecting ':')" }
        ',' { if ((Get-LbfJsonLast $D).Obj) { $where = "after object value (expecting ',' or '}')" } else { $where = "after array element (expecting ',' or ']')" } }
    }
    $pos = Skip-LbfJsonSpace $D $D.PrevEnd
    return New-LbfJsonSyntaxError $D (New-LbfInvalidChar $D $pos $where) $pos 0
}

# The leading whitespace and delimiter before the next token; returns its position, or throws.
function Get-LbfJsonNext {
    param([hashtable]$D)
    if ($D.PeekPos -ge 0) {
        $p = $D.PeekPos
        $D.PeekPos = -1
        if ($null -ne $D.PeekErr) { $e = $D.PeekErr; $D.PeekErr = $null; throw $e }
        return $p
    }
    $pos = Skip-LbfJsonSpace $D $D.PrevEnd
    if ($pos -ge $D.B.Length) {
        if ($D.Stack.Count -eq 1) { throw (New-LbfJsonEof $D) }
        throw (New-LbfJsonSyntaxError $D (New-LbfGoErr 'unexpected EOF') $pos 0)
    }
    $delim = ''
    $c = $D.B[$pos]
    if ($c -eq 0x3A -or $c -eq 0x2C) {
        $delim = [string][char]$c
        $pos = Skip-LbfJsonSpace $D ($pos + 1)
        if ($pos -ge $D.B.Length) {
            if ((Get-LbfJsonNeedDelim $D '"') -ne $delim) { throw (New-LbfJsonCheckDelim $D $delim '"') }
            throw (New-LbfJsonSyntaxError $D (New-LbfGoErr 'unexpected EOF') $pos 0)
        }
    }
    $next = Get-LbfJsontextKind $D $pos
    if ((Get-LbfJsonNeedDelim $D $next) -ne $delim) { throw (New-LbfJsonCheckDelim $D $delim $next) }
    return $pos
}

function New-LbfJsonEof {
    param([hashtable]$D)
    $e = New-LbfError ('jsontext: unexpected EOF' + $(if ($D.B.Length -gt 0) { ' after offset ' + $D.B.Length } else { '' }))
    $e.Data['jsonEOF'] = $true
    return $e
}

function Get-LbfJsonPeekKind {
    param([hashtable]$D)
    if ($D.PeekPos -ge 0) {
        if ($null -ne $D.PeekErr) { return 'x' }
        return Get-LbfJsontextKind $D $D.PeekPos
    }
    try {
        $pos = Get-LbfJsonNext $D
        $D.PeekPos = $pos
        $D.PeekErr = $null
        return Get-LbfJsontextKind $D $pos
    } catch {
        $D.PeekPos = 0
        $D.PeekErr = Get-LbfException $_
        return 'x'
    }
}

function Test-LbfHexDigits {
    param([byte[]]$B, [int]$From, [int]$Count)
    for ($i = $From; $i -lt $From + $Count; $i++) {
        $c = [int]$B[$i]
        if (-not (($c -ge 0x30 -and $c -le 0x39) -or ($c -ge 0x41 -and $c -le 0x46) -or ($c -ge 0x61 -and $c -le 0x66))) { return $false }
    }
    return $true
}

function Test-LbfEscapedUtf16Prefix {
    param([byte[]]$B, [int]$From, [bool]$Lower)
    for ($i = 0; $From + $i -lt $B.Length; $i++) {
        $c = [char]$B[$From + $i]
        if ($i -eq 0 -and $c -ne '\') { return $false }
        if ($i -eq 1 -and $c -cne 'u') { return $false }
        if ($i -eq 2 -and $Lower -and $c -ne 'd') { return $false }
        if ($i -eq 3 -and $Lower -and -not (($c -ge 'c' -and $c -le 'f') -or ($c -ge 'C' -and $c -le 'F'))) { return $false }
        if ($i -ge 2 -and $i -lt 6 -and -not (Test-LbfHexDigits $B ($From + $i) 1)) { return $false }
    }
    return $true
}

# jsonwire.ConsumeString from pos: @{ End; Value } or @{ Err; At }.
function Read-LbfJsonString {
    param([hashtable]$D, [int]$Pos)
    $b = $D.B
    $n = $Pos
    if ($b[$n] -ne 0x22) { return @{ Err = (New-LbfInvalidChar $D $n "at start of string (expecting '`"')"); At = $n } }
    $n++
    $buf = New-Object System.IO.MemoryStream
    while ($true) {
        if ($n -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $n } }
        $c = [int]$b[$n]
        if ($c -eq 0x22) {
            return @{ End = $n + 1; Value = $script:Utf8NoBom.GetString($buf.ToArray()) }
        }
        if ($c -lt 0x20) { return @{ Err = (New-LbfInvalidChar $D $n 'in string (expecting non-control character)'); At = $n } }
        if ($c -eq 0x5C) {
            if ($n + 2 -gt $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $n } }
            $e = [int]$b[$n + 1]
            $simple = -1
            switch ($e) {
                0x2F { $simple = 0x2F }
                0x22 { $simple = 0x22 }
                0x5C { $simple = 0x5C }
                0x62 { $simple = 8 }
                0x66 { $simple = 12 }
                0x6E { $simple = 10 }
                0x72 { $simple = 13 }
                0x74 { $simple = 9 }
            }
            if ($simple -ge 0) {
                $buf.WriteByte([byte]$simple)
                $n += 2
                continue
            }
            if ($e -eq 0x75) {
                if ($n + 6 -gt $b.Length) {
                    if (Test-LbfEscapedUtf16Prefix $b $n $false) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $n } }
                    return @{ Err = (New-LbfInvalidEscape $D $n $b.Length); At = $n }
                }
                if (-not (Test-LbfHexDigits $b ($n + 2) 4)) { return @{ Err = (New-LbfInvalidEscape $D $n ($n + 6)); At = $n } }
                $v1 = [Convert]::ToInt32([Text.Encoding]::ASCII.GetString($b, $n + 2, 4), 16)
                $n += 6
                $rune = $v1
                if ($v1 -ge 0xD800 -and $v1 -le 0xDFFF) {
                    if ($n + 6 -gt $b.Length) {
                        if (Test-LbfEscapedUtf16Prefix $b $n $true) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $n - 6 } }
                        return @{ Err = (New-LbfInvalidEscape $D ($n - 6) $b.Length); At = $n - 6 }
                    }
                    if ($b[$n] -ne 0x5C -or $b[$n + 1] -ne 0x75 -or -not (Test-LbfHexDigits $b ($n + 2) 4)) {
                        return @{ Err = (New-LbfInvalidEscape $D ($n - 6) ($n + 6)); At = $n - 6 }
                    }
                    $v2 = [Convert]::ToInt32([Text.Encoding]::ASCII.GetString($b, $n + 2, 4), 16)
                    if ($v1 -gt 0xDBFF -or $v2 -lt 0xDC00 -or $v2 -gt 0xDFFF) { return @{ Err = (New-LbfInvalidEscape $D ($n - 6) ($n + 6)); At = $n - 6 } }
                    $rune = 0x10000 + (($v1 - 0xD800) -shl 10) + ($v2 - 0xDC00)
                    $n += 6
                }
                $bytes = $script:Utf8NoBom.GetBytes([char]::ConvertFromUtf32($rune))
                $buf.Write($bytes, 0, $bytes.Length)
                continue
            }
            return @{ Err = (New-LbfInvalidEscape $D $n ($n + 2)); At = $n }
        }
        if ($c -ge 0x80) {
            $r = Get-LbfUtf8Rune $b $n
            if ($r[0] -eq -2) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $n } }
            if ($r[0] -lt 0) {
                if ($D.Legacy) { $buf.WriteByte([byte]$c); $n++; continue }
                return @{ Err = (New-LbfGoErr 'invalid UTF-8'); At = $n }
            }
            $buf.Write($b, $n, $r[1])
            $n += $r[1]
            continue
        }
        $buf.WriteByte([byte]$c)
        $n++
    }
}

# jsonwire.ConsumeNumber from pos: @{ End } or @{ Err; At }.
function Read-LbfJsonNumber {
    param([hashtable]$D, [int]$Pos)
    $b = $D.B
    $n = $Pos
    $isDigit = { param($i) $i -lt $b.Length -and $b[$i] -ge 0x30 -and $b[$i] -le 0x39 }
    if ($n -lt $b.Length -and $b[$n] -eq 0x2D) { $n++ }
    if ($n -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $Pos } }
    if ($b[$n] -eq 0x30) { $n++ }
    elseif ($b[$n] -ge 0x31 -and $b[$n] -le 0x39) { $n++; while (& $isDigit $n) { $n++ } }
    else { return @{ Err = (New-LbfInvalidChar $D $n 'in number (expecting digit)'); At = $n } }
    if ($n -lt $b.Length -and $b[$n] -eq 0x2E) {
        $n++
        if ($n -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $Pos } }
        if (-not (& $isDigit $n)) { return @{ Err = (New-LbfInvalidChar $D $n 'in number (expecting digit)'); At = $n } }
        while (& $isDigit $n) { $n++ }
    }
    if ($n -lt $b.Length -and ($b[$n] -eq 0x65 -or $b[$n] -eq 0x45)) {
        $n++
        if ($n -lt $b.Length -and ($b[$n] -eq 0x2D -or $b[$n] -eq 0x2B)) { $n++ }
        if ($n -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $Pos } }
        if (-not (& $isDigit $n)) { return @{ Err = (New-LbfInvalidChar $D $n 'in number (expecting digit)'); At = $n } }
        while (& $isDigit $n) { $n++ }
    }
    return @{ End = $n }
}

function Read-LbfJsonLiteral {
    param([hashtable]$D, [int]$Pos, [string]$Lit)
    $b = $D.B
    for ($i = 0; $i -lt $Lit.Length; $i++) {
        if ($Pos + $i -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $Pos + $i } }
        if ($b[$Pos + $i] -ne [byte][char]$Lit[$i]) {
            return @{ Err = (New-LbfInvalidChar $D ($Pos + $i) "in literal $Lit (expecting '$($Lit[$i])')"); At = $Pos + $i }
        }
    }
    return @{ End = $Pos + $Lit.Length }
}

# jsontext's consumeValue for a whole nested value: @{ End } or @{ Err; At }.
function Read-LbfJsonWhole {
    param([hashtable]$D, [int]$Pos)
    $b = $D.B
    $k = Get-LbfJsontextKind $D $Pos
    switch -CaseSensitive ($k) {
        'n' { return Read-LbfJsonLiteral $D $Pos 'null' }
        't' { return Read-LbfJsonLiteral $D $Pos 'true' }
        'f' { return Read-LbfJsonLiteral $D $Pos 'false' }
        '"' { return Read-LbfJsonString $D $Pos }
        '0' { return Read-LbfJsonNumber $D $Pos }
        '{' {
            $names = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
            $p = Skip-LbfJsonSpace $D ($Pos + 1)
            if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
            if ($b[$p] -eq 0x7D) { return @{ End = $p + 1 } }
            while ($true) {
                $p = Skip-LbfJsonSpace $D $p
                if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
                $s = Read-LbfJsonString $D $p
                if ($s.ContainsKey('Err')) { return $s }
                $quoted = ConvertTo-LbfPointerToken $s.Value
                if (-not $names.Add($s.Value) -and -not $D.Legacy) { $e = New-LbfGoErr 'duplicate object member name' 'duplicate'; $e.Suffix.Add($quoted); return @{ Err = $e; At = $p } }
                $p = Skip-LbfJsonSpace $D $s.End
                if ($p -ge $b.Length) { $e = New-LbfGoErr 'unexpected EOF'; $e.Suffix.Add($quoted); return @{ Err = $e; At = $p } }
                if ($b[$p] -ne 0x3A) { $e = New-LbfInvalidChar $D $p "after object name (expecting ':')"; $e.Suffix.Add($quoted); return @{ Err = $e; At = $p } }
                $p = Skip-LbfJsonSpace $D ($p + 1)
                if ($p -ge $b.Length) { $e = New-LbfGoErr 'unexpected EOF'; $e.Suffix.Add($quoted); return @{ Err = $e; At = $p } }
                $v = Read-LbfJsonWhole $D $p
                if ($v.ContainsKey('Err')) { $v.Err.Suffix.Add($quoted); return $v }
                $p = Skip-LbfJsonSpace $D $v.End
                if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
                if ($b[$p] -eq 0x2C) { $p++; continue }
                if ($b[$p] -eq 0x7D) { return @{ End = $p + 1 } }
                return @{ Err = (New-LbfInvalidChar $D $p "after object value (expecting ',' or '}')"); At = $p }
            }
        }
        '[' {
            $p = Skip-LbfJsonSpace $D ($Pos + 1)
            if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
            if ($b[$p] -eq 0x5D) { return @{ End = $p + 1 } }
            $idx = 0
            while ($true) {
                $p = Skip-LbfJsonSpace $D $p
                if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
                $v = Read-LbfJsonWhole $D $p
                if ($v.ContainsKey('Err')) { $v.Err.Suffix.Add([string]$idx); return $v }
                $p = Skip-LbfJsonSpace $D $v.End
                if ($p -ge $b.Length) { return @{ Err = (New-LbfGoErr 'unexpected EOF'); At = $p } }
                if ($b[$p] -eq 0x2C) { $p++; $idx++; continue }
                if ($b[$p] -eq 0x5D) { return @{ End = $p + 1 } }
                return @{ Err = (New-LbfInvalidChar $D $p "after array element (expecting ',' or ']')"); At = $p }
            }
        }
    }
    $last = Get-LbfJsonLast $D
    if (($last.Obj -and $k -eq ']') -or (-not $last.Obj -and $k -eq '}')) { return @{ Err = (New-LbfGoErr '' 'mismatch'); At = $Pos } }
    return @{ Err = (New-LbfInvalidChar $D $Pos 'at start of value'); At = $Pos }
}

# Records a name or value in the current object or array, as jsontext's state machine does.
function Add-LbfJsonToken {
    param([hashtable]$D, [string]$Kind, [int]$Start, [string]$Name)
    $last = Get-LbfJsonLast $D
    $needName = $last.Obj -and ($last.Len % 2) -eq 0
    if ($Kind -eq '"' -and $needName) {
        if ($null -ne $last.Ns -and -not $last.Ns.Add($Name)) {
            $e = New-LbfGoErr 'duplicate object member name' 'duplicate'
            $e.Suffix.Add((ConvertTo-LbfPointerToken $Name))
            throw (New-LbfJsonSyntaxError $D $e $Start 1)
        }
        $last.Name = $Name
    } elseif ($needName) {
        throw (New-LbfJsonSyntaxError $D (New-LbfGoErr 'object member name must be a string') $Start 1)
    }
    $last.Len++
}

# Decoder.ReadToken: @{ Kind; Start; End; Value }.
function Read-LbfJsonToken {
    param([hashtable]$D)
    $pos = Get-LbfJsonNext $D
    $k = Get-LbfJsontextKind $D $pos
    $value = $null
    switch -CaseSensitive ($k) {
        { $_ -eq 'n' -or $_ -eq 't' -or $_ -eq 'f' } {
            $lit = @{ n = 'null'; t = 'true'; f = 'false' }[$k]
            $r = Read-LbfJsonLiteral $D $pos $lit
            if ($r.ContainsKey('Err')) { throw (New-LbfJsonSyntaxError $D $r.Err $r.At 1) }
            Add-LbfJsonToken $D $k $pos ''
            $D.PrevEnd = $r.End
            return @{ Kind = $k; Start = $pos; End = $r.End }
        }
        '"' {
            $r = Read-LbfJsonString $D $pos
            if ($r.ContainsKey('Err')) { throw (New-LbfJsonSyntaxError $D $r.Err $r.At 1) }
            Add-LbfJsonToken $D $k $pos $r.Value
            $D.PrevEnd = $r.End
            return @{ Kind = $k; Start = $pos; End = $r.End; Value = $r.Value }
        }
        '0' {
            $r = Read-LbfJsonNumber $D $pos
            if ($r.ContainsKey('Err')) { throw (New-LbfJsonSyntaxError $D $r.Err $r.At 1) }
            Add-LbfJsonToken $D $k $pos ''
            $D.PrevEnd = $r.End
            return @{ Kind = $k; Start = $pos; End = $r.End }
        }
        { $_ -eq '{' -or $_ -eq '[' } {
            Add-LbfJsonToken $D $k $pos ''
            $ns = $null
            if ($k -eq '{') { $ns = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal) }
            $D.Stack.Add(@{ Obj = ($k -eq '{'); Len = 0; Name = ''; Ns = $ns })
            $D.PrevEnd = $pos + 1
            return @{ Kind = $k; Start = $pos; End = $pos + 1 }
        }
        '}' {
            $last = Get-LbfJsonLast $D
            if (-not $last.Obj) { throw (New-LbfJsonSyntaxError $D (New-LbfGoErr '' 'mismatch') $pos 1) }
            if (($last.Len % 2) -eq 1) { throw (New-LbfJsonSyntaxError $D (New-LbfGoErr 'missing value after object name') $pos 1) }
            $D.Stack.RemoveAt($D.Stack.Count - 1)
            $D.PrevEnd = $pos + 1
            return @{ Kind = $k; Start = $pos; End = $pos + 1 }
        }
        ']' {
            $last = Get-LbfJsonLast $D
            if ($last.Obj -or $D.Stack.Count -eq 1) { throw (New-LbfJsonSyntaxError $D (New-LbfGoErr '' 'mismatch') $pos 1) }
            $D.Stack.RemoveAt($D.Stack.Count - 1)
            $D.PrevEnd = $pos + 1
            return @{ Kind = $k; Start = $pos; End = $pos + 1 }
        }
    }
    throw (New-LbfJsonSyntaxError $D (New-LbfInvalidChar $D $pos 'at start of value') $pos 1)
}

# Decoder.ReadValue: a whole value, @{ Kind; Start; End; Value }.
function Read-LbfJsonValue2 {
    param([hashtable]$D)
    $pos = Get-LbfJsonNext $D
    $k = Get-LbfJsontextKind $D $pos
    $r = Read-LbfJsonWhole $D $pos
    if ($r.ContainsKey('Err')) { throw (New-LbfJsonSyntaxError $D $r.Err $r.At 1) }
    $value = $null
    if ($k -eq '"') { $value = (Read-LbfJsonString $D $pos).Value }
    Add-LbfJsonToken $D $k $pos $value
    $D.PrevEnd = $r.End
    return @{ Kind = $k; Start = $pos; End = $r.End; Value = $value }
}

function Get-LbfJsonKindName {
    param([string]$Kind)
    switch -CaseSensitive ($Kind) {
        'n' { return ' JSON null' }
        't' { return ' JSON boolean' }
        'f' { return ' JSON boolean' }
        '"' { return ' JSON string' }
        '0' { return ' JSON number' }
        '{' { return ' JSON object' }
        '[' { return ' JSON array' }
    }
    return ''
}

# json.SemanticError for a value of the wrong kind, just read.
function New-LbfJsonTypeError {
    param([hashtable]$D, [string]$Kind, [int]$Start, [string]$GoType)
    $ptr = Get-LbfJsonPointer $D -1
    $msg = 'json: cannot unmarshal' + (Get-LbfJsonKindName $Kind) + ' into Go ' + $GoType
    if ($ptr -ne '') { $msg += ' within ' + (Format-LbfPointer $ptr) }
    elseif ($Start -gt 0) { $msg += ' after offset ' + $Start }
    return New-LbfError $msg
}

# Unmarshals the next value into a Go type of $script:JsonTypes.
function Read-LbfJsonInto {
    param([hashtable]$D, [string]$Type)
    $t = $script:JsonTypes[$Type]
    switch ($t.Kind) {
        'string' {
            $v = Read-LbfJsonValue2 $D
            if ($v.Kind -eq 'n') { return '' }
            if ($v.Kind -eq '"') { return $v.Value }
            throw (New-LbfJsonTypeError $D $v.Kind $v.Start 'string')
        }
        'map' {
            $tok = Read-LbfJsonToken $D
            if ($tok.Kind -eq 'n') { return $null }
            if ($tok.Kind -ne '{') { throw (New-LbfJsonTypeError $D $tok.Kind $tok.Start $t.Go) }
            (Get-LbfJsonLast $D).Ns = $null
            $m = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
            while ((Get-LbfJsonPeekKind $D) -ne '}') {
                $key = Read-LbfJsonInto $D 'string'
                if ($m.ContainsKey($key)) {
                    $e = New-LbfGoErr 'duplicate object member name' 'duplicate'
                    throw (New-LbfJsonError2 $e.Text (Get-LbfJsonPointer $D -1) 0 $true)
                }
                $m[$key] = Read-LbfJsonInto $D 'string'
            }
            [void](Read-LbfJsonToken $D)
            return , $m
        }
        'slice' {
            $tok = Read-LbfJsonToken $D
            if ($tok.Kind -eq 'n') { return $null }
            if ($tok.Kind -ne '[') { throw (New-LbfJsonTypeError $D $tok.Kind $tok.Start $t.Go) }
            $list = New-Object System.Collections.Generic.List[hashtable]
            while ((Get-LbfJsonPeekKind $D) -ne ']') { $list.Add((Read-LbfJsonInto $D $t.Elem)) }
            [void](Read-LbfJsonToken $D)
            return , $list
        }
        'struct' {
            $tok = Read-LbfJsonToken $D
            if ($Type -eq 'provenance') {
                $out = New-LbfProvenanceValue
                $out.Instruments = $null
                $out.Properties = $null
            } else {
                $out = New-LbfInstrumentValue
            }
            if ($tok.Kind -eq 'n') {
                if ($Type -eq 'provenance') { $out.Instruments = New-Object System.Collections.Generic.List[hashtable] }
                return $out
            }
            if ($tok.Kind -ne '{') { throw (New-LbfJsonTypeError $D $tok.Kind $tok.Start $t.Go) }
            (Get-LbfJsonLast $D).Ns = $null
            $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
            while ((Get-LbfJsonPeekKind $D) -ne '}') {
                $nameTok = Read-LbfJsonValue2 $D
                $name = $nameTok.Value
                if (-not $t.Fields.ContainsKey($name)) {
                    $ptr = Get-LbfJsonPointer $D -1
                    $at = Get-LbfPointerParent $ptr
                    $msg = "unknown field $(Format-LbfQuote (Get-LbfPointerLast $ptr))"
                    if ($at -ne '') { $msg += " in $at" }
                    throw (New-LbfError $msg)
                }
                if (-not $seen.Add($name)) {
                    throw (New-LbfJsonError2 'duplicate object member name' (Get-LbfJsonPointer $D -1) 0 $true)
                }
                $f = $t.Fields[$name]
                $out[$f[0]] = Read-LbfJsonInto $D $f[1]
            }
            [void](Read-LbfJsonToken $D)
            if ($Type -eq 'provenance' -and $null -eq $out.Instruments) { $out.Instruments = New-Object System.Collections.Generic.List[hashtable] }
            return $out
        }
    }
}
