$script:ParallelFiles = 8
$script:BlockSize = 8MB
$script:BlockWorkers = 4
$script:MaxBlocks = 50000
$script:CrateName = 'ro-crate-metadata.json'
$script:RoCrateContexts = @('https://w3id.org/ro/crate/1.3/context', 'https://w3id.org/ro/crate/1.2/context')
$script:LbfOwnProperties = @('subscription_name', 'subscription_id', 'source_path')
$script:CrateFrameBase = 'https://crate.invalid/'
$script:XferRandom = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$script:SasEnvKeys = @('AZURE_ACCOUNT', 'AZURE_CONTAINER', 'AZURE_SAS', 'AZURE_SAS_EXPIRY', 'AZURE_SAS_PERMISSIONS', 'USER_IDENTITY', 'SUBSCRIPTION_NAME', 'SUBSCRIPTION_ID')

# ---------- sas-env files ----------

function Write-LbfSasEnv {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][hashtable]$Target)
    $expiry = $Target.Expiry
    if ($expiry -is [DateTimeOffset]) { $expiry = $expiry.UtcDateTime }
    $values = @{
        AZURE_ACCOUNT = [string]$Target.Account; AZURE_CONTAINER = [string]$Target.Container; AZURE_SAS = [string]$Target.SAS
        AZURE_SAS_EXPIRY = (Format-LbfTimestamp $expiry); AZURE_SAS_PERMISSIONS = [string]$Target.Permissions
        USER_IDENTITY = [string]$Target.User; SUBSCRIPTION_NAME = [string]$Target.SubscriptionName; SUBSCRIPTION_ID = [string]$Target.SubscriptionID
    }
    $sb = New-Object System.Text.StringBuilder
    foreach ($k in $script:SasEnvKeys) {
        if ($values[$k].IndexOfAny([char[]]@([char]39, [char]10, [char]13)) -ge 0) { throw (New-LbfError "$k contains a character the credential file cannot hold") }
        [void]$sb.Append("$k='$($values[$k])'`n")
    }
    $full = Get-LbfFullPath $Path
    try { Write-LbfTextFile $full $sb.ToString() } catch { throw (New-LbfError (Format-LbfOsError 'open' $Path $_)) }
    if (-not $script:OnWindows) {
        try { & chmod 600 $full 2>$null } catch { }
    }
}

# Go's time.Parse(time.RFC3339, s), or $null.
function ConvertFrom-LbfRfc3339 {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $m = [regex]::Match($Text, '^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})([.,][0-9]+)?(Z|([+-])([0-9]{2}):([0-9]{2}))\z')
    if (-not $m.Success) { return $null }
    $y = [int]$m.Groups[1].Value; $mo = [int]$m.Groups[2].Value; $d = [int]$m.Groups[3].Value
    $h = [int]$m.Groups[4].Value; $mi = [int]$m.Groups[5].Value; $s = [int]$m.Groups[6].Value
    if ($mo -lt 1 -or $mo -gt 12 -or $d -lt 1 -or $y -lt 1 -or $d -gt [DateTime]::DaysInMonth($y, $mo) -or $h -gt 23 -or $mi -gt 59 -or $s -gt 59) { return $null }
    $offset = [TimeSpan]::Zero
    if ($m.Groups[8].Value -ne 'Z') {
        $oh = [int]$m.Groups[10].Value; $om = [int]$m.Groups[11].Value
        if ($oh -gt 14 -or $om -gt 59) { return $null }
        $offset = New-Object TimeSpan $oh, $om, 0
        if ($m.Groups[9].Value -eq '-') { $offset = $offset.Negate() }
    }
    try {
        $t = New-Object DateTimeOffset $y, $mo, $d, $h, $mi, $s, $offset
        if ($m.Groups[7].Success) {
            $frac = $m.Groups[7].Value.Substring(1).PadRight(7, '0').Substring(0, 7)
            $t = $t.AddTicks([long]$frac)
        }
        return $t
    } catch { return $null }
}

function Read-LbfSasEnv {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$Mode, [Parameter(Mandatory)][AllowEmptyString()][string]$Container)
    $full = Get-LbfFullPath $Path
    if ([IO.Directory]::Exists($full)) {
        if ($script:OnWindows) { throw (New-LbfError "read ${Path}: Incorrect function.") }
        throw (New-LbfError "read ${Path}: is a directory")
    }
    try { $raw = Read-LbfTextFile $full } catch { throw (New-LbfError (Format-LbfOsError 'open' $Path $_)) }
    $remedy = "mint a fresh one with: lbf mint-sas --mode $Mode"

    $values = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    foreach ($line in ($raw.Replace("`r`n", "`n") -split "`n")) {
        if ($line.Trim().Length -eq 0) { continue }
        $m = [regex]::Match($line, "^([A-Z_]+)='([^']*)'[ \t]*\z")
        if (-not $m.Success) { throw (New-LbfError "${Path}: not a KEY='value' line: $(Format-LbfQuote $line -MaxRunes 60); $remedy") }
        $k = $m.Groups[1].Value
        if (-not (Test-LbfIn $k $script:SasEnvKeys)) { throw (New-LbfError "${Path}: unexpected key $k; $remedy") }
        if ($values.ContainsKey($k)) { throw (New-LbfError "${Path}: $k appears more than once; $remedy") }
        $values[$k] = $m.Groups[2].Value
    }
    foreach ($k in $script:SasEnvKeys) {
        if (-not $values.ContainsKey($k) -or $values[$k].Length -eq 0) { throw (New-LbfError "$Path is missing a value for $k; $remedy") }
    }
    if ($values['AZURE_ACCOUNT'] -cnotmatch '^[a-z0-9]{3,24}\z') {
        throw (New-LbfError "$Path has an invalid storage account name $(Format-LbfQuote $values['AZURE_ACCOUNT']); $remedy")
    }
    $expiry = ConvertFrom-LbfRfc3339 $values['AZURE_SAS_EXPIRY']
    if ($null -eq $expiry) { throw (New-LbfError "$Path has an unreadable AZURE_SAS_EXPIRY; $remedy") }
    if ([DateTimeOffset]::UtcNow -gt $expiry) { throw (New-LbfError "the SAS in $Path expired at $($values['AZURE_SAS_EXPIRY']); $remedy") }
    if ((Test-LbfSame $Mode 'upload') -and $values['AZURE_SAS_PERMISSIONS'].IndexOf([char]'w') -lt 0) {
        throw (New-LbfError "$Path grants $(Format-LbfQuote $values['AZURE_SAS_PERMISSIONS']) and cannot be used to upload; re-mint with --mode upload")
    }
    if (-not (Test-LbfSame $values['AZURE_CONTAINER'] $Container)) {
        throw (New-LbfError "$Path was minted for container $(Format-LbfQuote $values['AZURE_CONTAINER']), not $(Format-LbfQuote $Container); pass --container $($values['AZURE_CONTAINER']), or mint a file for $Container")
    }
    Write-LbfLog "[sas] using $Path -> $($values['AZURE_ACCOUNT'])/$($values['AZURE_CONTAINER']) (permissions=$($values['AZURE_SAS_PERMISSIONS']), expires $($values['AZURE_SAS_EXPIRY']))`n"
    return New-LbfTarget -Account $values['AZURE_ACCOUNT'] -Container $values['AZURE_CONTAINER'] -SAS $values['AZURE_SAS'] -Expiry $expiry `
        -Permissions $values['AZURE_SAS_PERMISSIONS'] -User $values['USER_IDENTITY'] -SubscriptionName $values['SUBSCRIPTION_NAME'] `
        -SubscriptionID $values['SUBSCRIPTION_ID'] -Endpoint ([string]$env:LBF_BLOB_ENDPOINT)
}

# ---------- Small helpers ----------

# Go's taken: the ID holds other files or another crate, so lbf exits with 3.
function New-LbfTakenError {
    param([Parameter(Mandatory)][string]$Heading, [string[]]$Problems = @())
    $Heading += "; publish without --id, or with a new ID from 'lbf new-id'"
    if ($Problems.Count -gt 0) { $Heading += ':' }
    return New-LbfError (New-LbfProblemList $Heading $Problems).Message -ExitCode $script:ExitIDTaken
}

function New-LbfSha256 {
    if ($PSVersionTable.PSEdition -eq 'Desktop') { return New-Object System.Security.Cryptography.SHA256CryptoServiceProvider }
    return [System.Security.Cryptography.SHA256]::Create()
}

function Get-LbfFileSha256 {
    param([Parameter(Mandatory)][string]$Path)
    $sha = New-LbfSha256
    try {
        $fs = New-Object System.IO.FileStream ((Get-LbfIoPath $Path), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite, 1048576)
        try { $hash = $sha.ComputeHash($fs) } finally { $fs.Dispose() }
    } finally { $sha.Dispose() }
    return ConvertTo-LbfHex $hash
}

function Get-LbfGoos {
    if ($script:OnWindows) { return 'windows' }
    try {
        if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) { return 'darwin' }
    } catch { }
    return 'linux'
}

function Join-LbfRel {
    param([Parameter(Mandatory)][string]$Root, [Parameter(Mandatory)][AllowEmptyString()][string]$Rel)
    $r = $Rel
    if ($script:OnWindows) { $r = $r.Replace('/', '\') }
    return [IO.Path]::Combine($Root, $r)
}

function Format-LbfDone {
    param([Parameter(Mandatory)][System.Diagnostics.Stopwatch]$Started, [long]$Total)
    $ms = [long][Math]::Round($Started.Elapsed.TotalMilliseconds, [MidpointRounding]::AwayFromZero)
    $sec = $Started.Elapsed.TotalSeconds
    $rate = [long]0
    if ($sec -gt 0) { $rate = [long][Math]::Truncate($Total / $sec) }
    Write-LbfLog "Done in $(Format-LbfGoDuration $ms) ($(Format-LbfBytes $rate)/s)`n"
}

function New-LbfCanceledError {
    $e = New-LbfError 'context canceled'
    $e.Data['lbfCanceled'] = $true
    return $e
}

# ---------- Crates as stored ----------

function Get-LbfJsonList {
    param($Value)
    if ($null -eq $Value) { return , @() }
    if ($Value -is [object[]]) { return , $Value }
    return , @($Value)
}

function Get-LbfCrateRefId {
    param($Value)
    if ($Value -is [System.Collections.IDictionary] -and $Value.Contains('@id') -and $Value['@id'] -is [string]) { return $Value['@id'] }
    return ''
}

function Get-LbfCrateJsonKind {
    param($Value)
    if ($Value -is [System.Collections.IDictionary]) { return 'object' }
    if ($Value -is [object[]]) { return 'array' }
    if ($Value -is [string]) { return 'string' }
    if ($Value -is [bool]) { return 'bool' }
    return 'number'
}

# The crate's own contentSize as Go reads it: strconv.ParseInt(fmt.Sprint(v)).
function ConvertFrom-LbfContentSize {
    param($Value)
    if ($Value -is [string]) {
        if ($Value -notmatch '^[+-]?[0-9]+\z') { return $null }
        $n = 0L
        if ([long]::TryParse($Value, [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$n)) { return $n }
        return $null
    }
    if ($Value -is [long] -or $Value -is [int]) {
        if ([Math]::Abs([double]$Value) -lt 1e6) { return [long]$Value }
        return $null
    }
    if ($Value -is [double]) {
        if ([Math]::Floor($Value) -eq $Value -and [Math]::Abs($Value) -lt 1e6) { return [long]$Value }
        return $null
    }
    return $null
}

# Go's readCrate: the files a crate records and what is left of it for reading what it states.
function Read-LbfStoredCrate {
    param([Parameter(Mandatory)][byte[]]$Raw)
    $crate = $script:CrateName
    $text = $script:Utf8NoBom.GetString($Raw)
    try {
        $doc = ConvertFrom-LbfJson $text -LastWins
    } catch {
        $ex = Get-LbfException $_
        $detail = $ex.Message
        if ($ex.Data.Contains('jsonDetail')) { $detail = $ex.Data['jsonDetail'] }
        throw (New-LbfError "its $crate is not valid JSON: $detail")
    }
    $context = $null
    $graph = $null
    if ($null -ne $doc) {
        if (-not ($doc -is [System.Collections.IDictionary])) {
            throw (New-LbfError ("its $crate is not valid JSON: json: cannot unmarshal $(Get-LbfCrateJsonKind $doc) into Go value of type " + 'struct { Context interface {} "json:\"@context\""; Graph []map[string]interface {} "json:\"@graph\"" }'))
        }
        foreach ($k in @($doc.Keys)) {
            if ([string]::Equals($k, '@context', [StringComparison]::OrdinalIgnoreCase)) { $context = $doc[$k] }
            elseif ([string]::Equals($k, '@graph', [StringComparison]::OrdinalIgnoreCase)) {
                $g = $doc[$k]
                if ($null -ne $g -and -not ($g -is [object[]])) {
                    throw (New-LbfError "its $crate is not valid JSON: json: cannot unmarshal $(Get-LbfCrateJsonKind $g) into Go struct field .@graph of type []map[string]interface {}")
                }
                $i = 0
                foreach ($e in (Get-LbfJsonList $g)) {
                    if ($null -ne $e -and -not ($e -is [System.Collections.IDictionary])) {
                        throw (New-LbfError "its $crate is not valid JSON: json: cannot unmarshal $(Get-LbfCrateJsonKind $e) into .$k.$i of type map[string]interface {}")
                    }
                    $i++
                }
                $graph = $g
            }
        }
    }
    $graph = Get-LbfJsonList $graph
    $entities = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($e in $graph) {
        if ($null -ne $e -and $e.Contains('@id') -and $e['@id'] -is [string]) { $entities[$e['@id']] = $e }
    }
    $isFile = New-Object 'System.Collections.Generic.Dictionary[string,bool]' ([StringComparer]::Ordinal)
    $files = New-Object System.Collections.Generic.List[hashtable]
    $root = $null
    if ($entities.ContainsKey('./')) { $root = $entities['./'] }
    $parts = @()
    if ($null -ne $root -and $root.Contains('hasPart')) { $parts = Get-LbfJsonList $root['hasPart'] }
    foreach ($part in $parts) {
        $id = Get-LbfCrateRefId $part
        $entity = $null
        if ($entities.ContainsKey($id)) { $entity = $entities[$id] }
        $isFile[$id] = $null -ne $entity -and $entity.Contains('@type') -and $entity['@type'] -is [string] -and (Test-LbfSame $entity['@type'] 'File')
        $rel = ConvertFrom-LbfQueryEscape $id -Path
        if ([string]::IsNullOrEmpty($rel)) { throw (New-LbfError "its $crate lists an unreadable file $(Format-LbfQuote $id)") }
        $size = $null
        if ($null -ne $entity -and $entity.Contains('contentSize')) { $size = ConvertFrom-LbfContentSize $entity['contentSize'] }
        if ($null -eq $size) { throw (New-LbfError "its $crate gives no size for $rel") }
        $sum = ''
        if ($null -ne $entity -and $entity.Contains('sha256') -and $entity['sha256'] -is [string]) { $sum = $entity['sha256'] }
        $files.Add(@{ Rel = $rel; Size = [long]$size; SHA256 = $sum })
    }
    if ($files.Count -eq 0) { throw (New-LbfError "its $crate lists no files") }
    if ($null -ne $root -and $root.Contains('hasPart')) { $root.Remove('hasPart') }
    $stated = New-Object System.Collections.Generic.List[object]
    foreach ($e in $graph) {
        $id = Get-LbfCrateRefId $e
        if (-not ($isFile.ContainsKey($id) -and $isFile[$id])) { $stated.Add($e) }
    }
    return @{ Raw = $Raw; Files = $files; Context = $context; Graph = $stated }
}

function Get-LbfCrateByPath {
    param([Parameter(Mandatory)][hashtable]$Crate)
    $byPath = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($f in $Crate.Files) { $byPath[$f.Rel] = $f }
    return , $byPath
}

# Go's storedCrate.differences.
function Get-LbfCrateDifferences {
    param([Parameter(Mandatory)][hashtable]$Crate, [Parameter(Mandatory)]$Stored, [bool]$Sums)
    $problems = New-Object System.Collections.Generic.List[string]
    $listed = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    [void]$listed.Add($script:CrateName)
    foreach ($f in $Crate.Files) {
        [void]$listed.Add($f.Rel)
        if (-not $Stored.ContainsKey($f.Rel)) { $problems.Add("$($f.Rel): listed in the crate but not stored"); continue }
        $s = $Stored[$f.Rel]
        if ($s.Size -ne $f.Size) { $problems.Add("$($f.Rel): stored as $($s.Size) bytes, but the crate records $($f.Size)") }
        elseif ($Sums -and -not (Test-LbfSame $s.SHA256 $f.SHA256)) { $problems.Add("$($f.Rel): stored with sha256 $(Format-LbfQuote $s.SHA256), but the crate records $(Format-LbfQuote $f.SHA256)") }
    }
    foreach ($name in (Get-LbfSortedUtf8 @($Stored.Keys))) {
        if (-not $listed.Contains($name)) { $problems.Add("${name}: stored but not listed in the crate") }
    }
    return , $problems.ToArray()
}

# Compaction drops a list of one and a value object's wrapper, so a stated string may arrive either way.
function Get-LbfStatedString {
    param($Node, [string]$Key)
    if (-not ($Node -is [System.Collections.IDictionary]) -or -not $Node.Contains($Key)) { return '' }
    $v = $Node[$Key]
    if ($v -is [object[]] -and $v.Count -eq 1) { $v = $v[0] }
    if ($v -is [System.Collections.IDictionary] -and $v.Contains('@value') -and $v.Count -eq 1) { $v = $v['@value'] }
    if ($v -is [string]) { return $v }
    return ''
}

# The values of a property as JSON-LD sees them: duplicates merged, references resolved to the entities they name.
function Get-LbfStatedNodes {
    param($Node, [string]$Key, $Entities)
    $out = New-Object System.Collections.Generic.List[object]
    if (-not ($Node -is [System.Collections.IDictionary]) -or -not $Node.Contains($Key)) { return , $out }
    $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($v in (Get-LbfJsonList $Node[$Key])) {
        if ($v -is [System.Collections.IDictionary]) {
            $id = Get-LbfCrateRefId $v
            if ($id.Length -ne 0) {
                if (-not $seen.Add('@' + $id)) { continue }
                if ($Entities.ContainsKey($id)) { $out.Add($Entities[$id]) } else { $out.Add($v) }
                continue
            }
            $out.Add($v)
            continue
        }
        if ($null -ne $v -and -not $seen.Add('=' + [string]$v)) { continue }
        $out.Add($v)
    }
    return , $out
}

# Go refuses a crate whose context it cannot load, since lbf reads only the contexts of the crates it writes.
function Test-LbfCrateContext {
    param($Context)
    $known = $false
    foreach ($c in (Get-LbfJsonList $Context)) {
        if ($c -is [string]) {
            $uri = $c
            if (-not [Uri]::IsWellFormedUriString($uri, [UriKind]::Absolute)) {
                try { $uri = (New-Object Uri ((New-Object Uri $script:CrateFrameBase), $c)).AbsoluteUri } catch { }
            }
            if (-not (Test-LbfIn $uri $script:RoCrateContexts)) {
                throw (New-LbfError "framing the crate: loading remote context failed: dereferencing a URL did not result in a valid JSON-LD context ($uri): loading document failed: the crate uses the context $uri, but lbf reads only the RO-Crate 1.2 and 1.3 contexts of the crates it writes; this crate was not written by lbf")
            }
            $known = $true
        }
    }
    return $known
}

# Go's storedCrate.statement, reading the graph by @id as framing it would: Provenance and ConformsTo.
function Get-LbfCrateStatement {
    param([Parameter(Mandatory)][hashtable]$Crate)
    $known = Test-LbfCrateContext $Crate.Context
    $prov = New-LbfProvenanceValue
    $conformsTo = New-Object System.Collections.Generic.List[string]
    if (-not $known) { return @{ Provenance = $prov; ConformsTo = $conformsTo } }
    $entities = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($e in $Crate.Graph) {
        if ($e -is [System.Collections.IDictionary] -and $e.Contains('@id') -and $e['@id'] -is [string]) { $entities[$e['@id']] = $e }
    }
    $root = $null
    if ($entities.ContainsKey('./')) { $root = $entities['./'] }

    $prov.Name = Get-LbfStatedString $root 'name'
    $prov.Description = Get-LbfStatedString $root 'description'
    $indexed = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($pv in (Get-LbfStatedNodes $root 'additionalProperty' $entities)) {
        if (-not ($pv -is [System.Collections.IDictionary])) { continue }
        $key = Get-LbfStatedString $pv 'name'
        if ($key.Length -eq 0 -and -not $pv.Contains('name')) { $key = '@none' }
        if ($indexed.ContainsKey($key)) { $indexed[$key] = $null } else { $indexed[$key] = $pv }
    }
    foreach ($name in $indexed.Keys) {
        if (-not (Test-LbfIn $name $script:LbfOwnProperties)) { $prov.Properties[$name] = Get-LbfStatedString $indexed[$name] 'value' }
    }
    $parents = New-Object System.Collections.Generic.List[object]
    foreach ($p in (Get-LbfStatedNodes $root 'isBasedOn' $entities)) { $parents.Add($p) }
    foreach ($p in (Get-LbfStatedNodes $root 'wasDerivedFrom' $entities)) { $parents.Add($p) }
    foreach ($p in $parents) { $prov.DerivedFrom = Get-LbfStatedString $p 'identifier' }
    foreach ($run in $Crate.Graph) {
        if (-not ($run -is [System.Collections.IDictionary]) -or -not $run.Contains('result')) { continue }
        $toRoot = $false
        foreach ($r in (Get-LbfJsonList $run['result'])) { if (Test-LbfSame (Get-LbfCrateRefId $r) './') { $toRoot = $true } }
        if (-not $toRoot) { continue }
        foreach ($in in (Get-LbfStatedNodes $run 'instrument' $entities)) {
            $prov.Instruments.Add(@{ Name = (Get-LbfStatedString $in 'name'); Version = (Get-LbfStatedString $in 'version'); URL = (Get-LbfStatedString $in 'url') })
        }
    }
    $id = Get-LbfStatedString $root 'identifier'
    $source = ''
    if ($indexed.ContainsKey('source_path')) { $source = Get-LbfStatedString $indexed['source_path'] 'value' }
    if ((Test-LbfSame $prov.Name $id) -or (Test-LbfSame $prov.Name (Get-LbfBase $source))) { $prov.Name = '' }
    $defaults = @("Dataset $id", "Bronze-layer dataset $id", (Get-LbfDefaultDescription $source $Crate.Files.Count $prov.DerivedFrom))
    if (Test-LbfIn $prov.Description $defaults) { $prov.Description = '' }
    foreach ($c in (Get-LbfStatedNodes $root 'conformsTo' $entities)) {
        if ($c -is [System.Collections.IDictionary] -and $c.Contains('@id') -and $c['@id'] -is [string]) { $conformsTo.Add($c['@id']) } else { $conformsTo.Add('') }
    }
    return @{ Provenance = $prov; ConformsTo = $conformsTo }
}

# Go's storedCrate.differsFrom: a name or description lbf filled in only has to match when given.
function Compare-LbfCrateWithPublication {
    param([Parameter(Mandatory)][hashtable]$Crate, [Parameter(Mandatory)][hashtable]$Publication)
    $st = Get-LbfCrateStatement $Crate
    $stated = $st.Provenance
    $mine = $Publication.Provenance
    if ([string]::IsNullOrEmpty($mine.Name)) { $stated.Name = '' }
    if ([string]::IsNullOrEmpty($mine.Description)) { $stated.Description = '' }
    $problems = New-Object System.Collections.Generic.List[string]
    if (-not (Test-LbfSameProvenance $stated $mine)) { $problems.Add('its name, description, parent, instruments or properties differ from these') }
    $a = Get-LbfSortedUtf8 $st.ConformsTo.ToArray()
    $b = Get-LbfSortedUtf8 (Get-LbfPublicationConformsTo $Publication)
    $same = $a.Count -eq $b.Count
    if ($same) { for ($i = 0; $i -lt $a.Count; $i++) { if (-not (Test-LbfSame $a[$i] $b[$i])) { $same = $false; break } } }
    if (-not $same) { $problems.Add('it was checked against other profiles') }
    return , $problems.ToArray()
}

# ---------- What the publication area provides ----------

function Get-LbfPublicationConformsTo {
    param([Parameter(Mandatory)][hashtable]$Publication)
    $ids = Get-LbfConformsTo -Publication $Publication
    return , ([string[]]$ids.ToArray())
}

function Invoke-LbfPublicationValidate {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target)
    return Test-LbfPublication -Publication $Publication -Target $Target
}

function Get-LbfPublicationCrateBytes {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)]$Published)
    $text = New-LbfCrateText -Publication $Publication -Target $Target -Published $Published
    return , $script:Utf8NoBom.GetBytes($text)
}

# ---------- Running transfers ----------

function New-LbfJob {
    param([Parameter(Mandatory)][string]$Kind, [Parameter(Mandatory)][string]$Name, [long]$Size, [hashtable]$Data = @{})
    $j = @{ Kind = $Kind; Name = $Name; Size = $Size; Bar = $null; Error = $null; Ops = [System.Collections.Generic.List[object]]::new(); Tasks = [System.Collections.Generic.List[object]]::new(); Cts = $null; Stage = '' }
    foreach ($k in $Data.Keys) { $j[$k] = $Data[$k] }
    return $j
}

function Start-LbfJob {
    param([hashtable]$Job)
    $Job.Cts = [System.Threading.CancellationTokenSource]::CreateLinkedTokenSource((Get-LbfCancelToken))
    switch ($Job.Kind) {
        'put' { Start-LbfPutJob $Job }
        'hash' { Start-LbfHashJob $Job }
        'get' { Start-LbfGetJob $Job }
    }
}

function Step-LbfJob {
    param([hashtable]$Job)
    switch ($Job.Kind) {
        'put' { return Step-LbfPutJob $Job }
        'hash' { return Step-LbfHashJob $Job }
        'get' { return Step-LbfGetJob $Job }
    }
    return $true
}

# Cancels whatever a job still has running and releases its files.
function Stop-LbfJob {
    param([hashtable]$Job)
    if ($null -ne $Job.Cts) { try { $Job.Cts.Cancel() } catch { } }
    foreach ($op in $Job.Ops.ToArray()) { Stop-LbfBlobOp $op }
    $Job.Ops.Clear()
    foreach ($t in $Job.Tasks.ToArray()) { try { [void]$t.Wait(5000) } catch { } }
    $Job.Tasks.Clear()
    foreach ($k in @('Fs', 'Sha', 'Crypto')) {
        if ($Job.ContainsKey($k) -and $null -ne $Job[$k]) { try { $Job[$k].Dispose() } catch { }; $Job[$k] = $null }
    }
    if ($Job.ContainsKey('Ranges')) {
        foreach ($r in @($Job.Ranges)) { Stop-LbfRange $r }
        $Job.Ranges.Clear()
    }
    if ($Job.ContainsKey('Pool')) { $Job.Pool.Clear() }
    if ($null -ne $Job.Cts) { try { $Job.Cts.Dispose() } catch { }; $Job.Cts = $null }
}

# Go's transferAll: up to eight files at once, and none started after one fails.
function Invoke-LbfTransferAll {
    param([Parameter(Mandatory)][hashtable]$Progress, [Parameter(Mandatory)][AllowEmptyCollection()][System.Collections.Generic.List[hashtable]]$Jobs)
    $running = New-Object System.Collections.Generic.List[hashtable]
    $next = 0
    $failure = $null
    $tasks = New-Object System.Collections.Generic.List[object]
    try {
        while ($true) {
            while ($null -eq $failure -and -not (Test-LbfInterrupted) -and $running.Count -lt $script:ParallelFiles -and $next -lt $Jobs.Count) {
                $j = $Jobs[$next]
                $next++
                $j.Bar = Start-LbfProgressBar $Progress $j.Name $j.Size
                $running.Add($j)
                try { Start-LbfJob $j } catch { $j.Error = $_ }
            }
            if ($running.Count -eq 0) { break }
            foreach ($j in @($running)) {
                $done = $false
                if ($null -eq $j.Error) {
                    try { $done = Step-LbfJob $j } catch { $j.Error = $_ }
                }
                if ($null -ne $j.Error -or $done) {
                    [void]$running.Remove($j)
                    Stop-LbfJob $j
                    Stop-LbfProgressBar $Progress $j.Bar ($null -eq $j.Error)
                    if ($null -ne $j.Error -and $null -eq $failure) {
                        $failure = $j.Error
                        foreach ($o in @($running)) {
                            Stop-LbfJob $o
                            Stop-LbfProgressBar $Progress $o.Bar $false
                        }
                        $running.Clear()
                        break
                    }
                }
            }
            Update-LbfProgress $Progress
            if ($running.Count -gt 0) {
                $tasks.Clear()
                $ops = New-Object System.Collections.Generic.List[object]
                foreach ($j in $running) {
                    foreach ($op in $j.Ops) { Add-LbfOpTask $tasks $op; $ops.Add($op) }
                    foreach ($t in $j.Tasks) { $tasks.Add($t) }
                    if ($j.ContainsKey('Ranges')) { foreach ($r in $j.Ranges) { if ($null -ne $r.Op) { Add-LbfOpTask $tasks $r.Op; $ops.Add($r.Op) }; if ($null -ne $r.Task) { $tasks.Add($r.Task) } } }
                }
                Wait-LbfTasks $tasks (Get-LbfOpWaitMs $ops.ToArray() 100)
            }
        }
    } finally {
        foreach ($j in $running) { Stop-LbfJob $j }
        Close-LbfProgress $Progress
    }
    if ($null -ne $failure) { throw $failure }
    if (Test-LbfInterrupted) { throw (New-LbfCanceledError) }
}

# ---------- Publish ----------

function Get-LbfUploaderMetadata {
    param([hashtable]$Target)
    $u = ([string]$Target.User).Trim()
    if ($u.Length -eq 0) { return $null }
    foreach ($c in $u.ToCharArray()) { if ([int]$c -lt 0x20 -or [int]$c -gt 0x7E) { return $null } }
    return $u
}

function Start-LbfPutJob {
    param([hashtable]$Job)
    $f = $Job.File
    try { $Job.Fs = [System.IO.FileStream]::new((Get-LbfIoPath $f.Path), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite, 65536) }
    catch { throw (New-LbfError (Format-LbfOsError 'open' $f.Path $_)) }
    $Job.Sha = New-LbfSha256
    $Job.Read = [long]0
    $Job.Off = [long]0
    $Job.Eof = $false
    $Job.Ids = [System.Collections.Generic.List[string]]::new()
    $Job.Chunk = [long][Math]::Max($script:BlockSize, [Math]::Ceiling($f.Size / $script:MaxBlocks))
    $attempt = [byte[]]::new(8)
    $script:XferRandom.GetBytes($attempt)
    $Job.Attempt = ConvertTo-LbfHex $attempt
    $Job.Pool = [System.Collections.Generic.Stack[object]]::new()
    $Job.BlockError = $null
    $Job.Stage = 'blocks'
}

function Read-LbfFull {
    param([System.IO.Stream]$Stream, [byte[]]$Buffer, [int]$Count)
    $got = 0
    try {
        while ($got -lt $Count) {
            $n = $Stream.Read($Buffer, $got, $Count - $got)
            if ($n -le 0) { break }
            $got += $n
        }
    } catch { }
    return $got
}

function Step-LbfPutJob {
    param([hashtable]$Job)
    $f = $Job.File
    $b = $Job.Blobs
    switch ($Job.Stage) {
        'blocks' {
            foreach ($op in $Job.Ops.ToArray()) {
                if (-not (Step-LbfBlobOp $op)) { continue }
                [void]$Job.Ops.Remove($op)
                try { [void](Complete-LbfBlobOp $op @(201)) } catch { if ($null -eq $Job.BlockError) { $Job.BlockError = $_ } }
                Close-LbfBlobResult $op.Result
                if ($op.Request.Body.Length -eq $Job.Chunk) { $Job.Pool.Push($op.Request.Body) }
            }
            if ($null -ne $Job.BlockError) {
                foreach ($op in $Job.Ops.ToArray()) { Stop-LbfBlobOp $op }
                $Job.Ops.Clear()
                throw (New-LbfWrappedError "uploading $($f.Rel): " $Job.BlockError)
            }
            while ($Job.Ops.Count -lt $script:BlockWorkers -and -not $Job.Eof -and $Job.Off -lt $f.Size -and -not $Job.Cts.IsCancellationRequested) {
                $n = [int][Math]::Min($Job.Chunk, $f.Size - $Job.Off)
                if ($n -eq $Job.Chunk -and $Job.Pool.Count -gt 0) { $buf = $Job.Pool.Pop() } else { $buf = [byte[]]::new($n) }
                $got = Read-LbfFull $Job.Fs $buf $n
                if ($got -gt 0) { [void]$Job.Sha.TransformBlock($buf, 0, $got, $null, 0) }
                $Job.Read += $got
                $Job.Bar.N = $Job.Read
                if ($got -lt $n) { $Job.Eof = $true; break }
                $blockId = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes(('{0}-{1:D8}' -f $Job.Attempt, $Job.Ids.Count)))
                $Job.Ids.Add($blockId)
                $req = New-LbfBlobRequest -Target $b.Target -Blob "$($b.Id)/$($f.Rel)" -Method 'PUT' -Params @('comp', 'block', 'blockid', $blockId) -Body $buf -Count $n -ContentType 'application/octet-stream'
                $op = New-LbfBlobOp $req $Job.Cts.Token
                $Job.Ops.Add($op)
                [void](Step-LbfBlobOp $op)
                $Job.Off += $n
            }
            if ($Job.Ops.Count -gt 0) { return $false }
            if ($Job.Cts.IsCancellationRequested) { throw (New-LbfCanceledError) }
            Test-LbfUnchanged $f $Job.Read
            [void]$Job.Sha.TransformFinalBlock((New-Object byte[] 0), 0, 0)
            $Job.Sum = ConvertTo-LbfHex $Job.Sha.Hash
            $Job.Fs.Dispose(); $Job.Fs = $null
            $xml = New-Object System.Text.StringBuilder '<BlockList>'
            foreach ($id in $Job.Ids) { [void]$xml.Append("<Latest>$id</Latest>") }
            [void]$xml.Append('</BlockList>')
            $headers = @{ 'If-None-Match' = '*'; 'x-ms-meta-sha256' = $Job.Sum }
            if (-not $b.ContainsKey('Uploader')) { $b.Uploader = Get-LbfUploaderMetadata $b.Target }
            if ($null -ne $b.Uploader) { $headers['x-ms-meta-uploader'] = $b.Uploader }
            $req = New-LbfBlobRequest -Target $b.Target -Blob "$($b.Id)/$($f.Rel)" -Method 'PUT' -Params @('comp', 'blocklist') -Headers $headers -Body ($script:Utf8NoBom.GetBytes($xml.ToString())) -ContentType 'application/xml'
            $Job.Ops.Add((New-LbfBlobOp $req $Job.Cts.Token))
            $Job.Stage = 'commit'
            return $false
        }
        'commit' {
            $op = $Job.Ops[0]
            if (-not (Step-LbfBlobOp $op)) { return $false }
            $Job.Ops.Clear()
            try {
                Close-LbfBlobResult (Complete-LbfBlobOp $op @(201))
            } catch {
                if (-not (Test-LbfAzCode $_ @('BlobAlreadyExists', 'ConditionNotMet'))) { throw (New-LbfWrappedError "uploading $($f.Rel): " $_) }
                $Job.Ops.Add((New-LbfBlobOp (New-LbfBlobRequest -Target $b.Target -Blob "$($b.Id)/$($f.Rel)" -Method 'HEAD') $Job.Cts.Token))
                $Job.Stage = 'stored'
                return $false
            }
            $f.SHA256 = $Job.Sum
            return $true
        }
        'stored' {
            $op = $Job.Ops[0]
            if (-not (Step-LbfBlobOp $op)) { return $false }
            $Job.Ops.Clear()
            try { $props = ConvertFrom-LbfPropertiesResult (Complete-LbfBlobOp $op @(200)) } catch { throw (New-LbfWrappedError "uploading $($f.Rel): " $_) }
            if ($props.Size -ne $f.Size -or -not (Test-LbfSame (Get-LbfMetadataValue $props.Metadata 'sha256') $Job.Sum)) {
                throw (New-LbfError "$($b.Id)/$($f.Rel) changed while this publish was writing it; is another publish of the same ID running?" -ExitCode $script:ExitIDTaken)
            }
            $f.SHA256 = $Job.Sum
            return $true
        }
    }
    return $true
}

# Go's unchanged: the file still has the size and time it was found with, and all of it was read.
function Test-LbfUnchanged {
    param([hashtable]$File, [long]$Read)
    $ok = $true
    try {
        $info = [System.IO.FileInfo]::new((Get-LbfIoPath $File.Path))
        if (-not $info.Exists -or $Read -ne $File.Size -or $info.Length -ne $File.Size) { $ok = $false }
        elseif ($File.ContainsKey('ModTime') -and $null -ne $File.ModTime) {
            $mt = $File.ModTime
            if ($mt -is [DateTimeOffset]) { $mt = $mt.UtcDateTime }
            if ($info.LastWriteTimeUtc.Ticks -ne ([datetime]$mt).ToUniversalTime().Ticks) { $ok = $false }
        }
    } catch { $ok = $false }
    if (-not $ok) { throw (New-LbfError "$($File.Rel) changed while it was being uploaded; wait until whatever writes it has finished, then publish again") }
}

function Start-LbfHashJob {
    param([hashtable]$Job)
    $f = $Job.File
    try { $Job.Fs = New-Object System.IO.FileStream ((Get-LbfIoPath $f.Path), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite, 1048576, $true) }
    catch { throw (New-LbfError (Format-LbfOsError 'open' $f.Path $_)) }
    $Job.Sha = New-LbfSha256
    $Job.Crypto = New-Object System.Security.Cryptography.CryptoStream ([IO.Stream]::Null), $Job.Sha, ([System.Security.Cryptography.CryptoStreamMode]::Write)
    $Job.Tasks.Add($Job.Fs.CopyToAsync($Job.Crypto, 1048576, $Job.Cts.Token))
}

function Step-LbfHashJob {
    param([hashtable]$Job)
    $f = $Job.File
    $t = $Job.Tasks[0]
    if (-not $t.IsCompleted) {
        try { $Job.Bar.N = $Job.Fs.Position } catch { }
        return $false
    }
    $Job.Tasks.Clear()
    if ($t.IsCanceled -or $Job.Cts.IsCancellationRequested) { throw (New-LbfCanceledError) }
    if ($t.IsFaulted) { throw (New-LbfError (Format-LbfOsError 'read' $f.Path $t.Exception)) }
    $read = $Job.Fs.Position
    $Job.Bar.N = $read
    $Job.Fs.Dispose(); $Job.Fs = $null
    $Job.Crypto.FlushFinalBlock()
    $sum = ConvertTo-LbfHex $Job.Sha.Hash
    Test-LbfUnchanged $f ([Math]::Min($read, $f.Size))
    if (-not (Test-LbfSame $sum $Job.Recorded)) {
        $Job.Problems.Add("$($f.Rel): its content differs from what is stored")
        return $true
    }
    $f.SHA256 = $sum
    return $true
}

# Go's compareRecorded: hashes each local file already recorded; returns ToSend (indexes) and sorted Problems.
function Compare-LbfRecorded {
    param([Parameter(Mandatory)]$Files, [Parameter(Mandatory)]$Recorded)
    $toSend = New-Object System.Collections.Generic.List[int]
    $problems = New-Object System.Collections.Generic.List[string]
    $jobs = New-Object System.Collections.Generic.List[hashtable]
    $local = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $total = [long]0
    for ($i = 0; $i -lt $Files.Count; $i++) {
        $f = $Files[$i]
        [void]$local.Add($f.Rel)
        if (-not $Recorded.ContainsKey($f.Rel)) { $toSend.Add($i); continue }
        $r = $Recorded[$f.Rel]
        if ($r.Size -ne $f.Size) { $problems.Add("$($f.Rel): $($f.Size) bytes here, $($r.Size) stored"); continue }
        if ([string]::IsNullOrEmpty($r.SHA256)) { $problems.Add("$($f.Rel): stored without a sha256 (published before lbf recorded them), so it cannot be confirmed to match"); continue }
        $total += $f.Size
        $jobs.Add((New-LbfJob 'hash' $f.Rel $f.Size @{ File = $f; Recorded = $r.SHA256; Problems = $problems }))
    }
    foreach ($rel in (Get-LbfSortedUtf8 @($Recorded.Keys))) {
        if (-not $local.Contains($rel) -and -not (Test-LbfSame $rel $script:CrateName)) { $problems.Add("${rel}: stored, but not here") }
    }
    if ($jobs.Count -gt 0) {
        Write-LbfLog "Checking $($jobs.Count) files ($(Format-LbfBytes $total)) against what is stored`n"
        Invoke-LbfTransferAll (New-LbfProgress $jobs.Count $total) $jobs
    }
    return @{ ToSend = $toSend; Problems = (Get-LbfSortedUtf8 $problems.ToArray()) }
}

function Send-LbfCrate {
    param([hashtable]$Blobs, [byte[]]$Crate)
    $name = "$($Blobs.Id)/$($script:CrateName)"
    $headers = @{ 'If-None-Match' = '*'; 'x-ms-blob-type' = 'BlockBlob'; 'x-ms-blob-content-type' = 'application/json' }
    try {
        Close-LbfBlobResult (Invoke-LbfBlobRequest (New-LbfBlobRequest -Target $Blobs.Target -Blob $name -Method 'PUT' -Headers $headers -Body $Crate -ContentType 'application/octet-stream') -Ok @(201))
    } catch {
        if (-not (Test-LbfAzCode $_ @('BlobAlreadyExists', 'ConditionNotMet'))) { throw (New-LbfWrappedError "uploading $($script:CrateName): " $_) }
        try { $stored = Read-LbfBlob $Blobs.Target $name } catch { throw (New-LbfWrappedError "reading the crate already stored as ${name}: " $_) }
        if (-not (Test-LbfBytesEqual $stored $Crate)) {
            $e = New-LbfError 'another publish of the same ID stored its crate first'
            $e.Data['lbfAnotherCrate'] = $true
            throw $e
        }
    }
}

function Test-LbfBytesEqual {
    param([byte[]]$A, [byte[]]$B)
    if ($A.Length -ne $B.Length) { return $false }
    for ($i = 0; $i -lt $A.Length; $i++) { if ($A[$i] -ne $B[$i]) { return $false } }
    return $true
}

# The crate can land even though its response never arrives.
function Get-LbfInterruptedPublishError {
    param([hashtable]$Blobs)
    $id = $Blobs.Id
    $cts = New-Object System.Threading.CancellationTokenSource
    $cts.CancelAfter(10000)
    try {
        $landed = Test-LbfBlobExists $Blobs.Target "$id/$($script:CrateName)" $cts.Token
    } catch {
        return New-LbfError "interrupted before lbf could tell whether $id was published; run the same publish with --id $id to finish or check it"
    } finally { $cts.Dispose() }
    if ($landed) { return New-LbfError "interrupted, but $id was published; run the same publish with --id $id to check what landed" }
    return New-LbfError "interrupted; $id was left without a crate, so fetch will refuse it. Run the same publish with --id $id to finish it"
}

# A rerun of a publish that already finished succeeds only if it would have published the same thing.
function Confirm-LbfPublished {
    param([hashtable]$Blobs, [hashtable]$Publication, $Stored)
    $id = $Publication.ID
    Set-LbfStatus "Reading the crate of $id"
    $raw = $null
    $err = $null
    try { $raw = Read-LbfBlob $Blobs.Target "$id/$($script:CrateName)" } catch { $err = $_ }
    Set-LbfStatus ''
    if ($null -ne $err) { throw (New-LbfWrappedError "reading the crate of ${id}: " $err) }
    try { $crate = Read-LbfStoredCrate $raw } catch { throw (New-LbfWrappedError "dataset ${id}: " $_) }
    $problems = Get-LbfCrateDifferences $crate $Stored $true
    if ($problems.Count -gt 0) { throw (New-LbfTakenError "$id is published, but what is stored differs from its crate, so fetch will refuse it" $problems) }
    try {
        $list = New-Object System.Collections.Generic.List[string]
        foreach ($p in (Compare-LbfCrateWithPublication $crate $Publication)) { $list.Add($p) }
    } catch {
        if (Test-LbfErrorFlag $_ 'lbf') {
            throw (New-LbfTakenError "$id is already published, but lbf cannot read what its crate states ($(Get-LbfErrorText $_)), so this publish cannot be checked against it")
        }
        throw
    }
    $cmp = Compare-LbfRecorded $Publication.Files (Get-LbfCrateByPath $crate)
    foreach ($n in $cmp.ToSend) { $list.Add("$($Publication.Files[$n].Rel): not in it") }
    foreach ($p in $cmp.Problems) { $list.Add($p) }
    if ($list.Count -gt 0) { throw (New-LbfTakenError "$id is already published, differently, and a dataset never changes" $list.ToArray()) }
    Write-LbfLog "$id is already published with exactly these files; nothing was uploaded`n"
}

# Another publish of the same ID could have added files before this one's crate landed.
function Test-LbfLanded {
    param([hashtable]$Blobs, [byte[]]$Raw)
    try { $crate = Read-LbfStoredCrate $Raw } catch { throw (New-LbfWrappedError "dataset $($Blobs.Id): " $_) }
    $stored = Get-LbfStoredFiles $Blobs.Target $Blobs.Id
    $problems = Get-LbfCrateDifferences $crate $stored $true
    if ($problems.Count -gt 0) { throw (New-LbfTakenError "$($Blobs.Id) was published, but something else changed its files meanwhile, so fetch will refuse it" $problems) }
}

# Go's upload.
function Invoke-LbfUpload {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][hashtable]$Publication)
    $published = Invoke-LbfPublicationValidate $Publication $Target
    $id = $Publication.ID
    $b = @{ Target = $Target; Id = $id }
    Initialize-LbfBlobAccess $Target
    Enter-LbfInterruptible
    try {
        Set-LbfStatus "Checking write access to $($Target.Account)/$($Target.Container)"
        $err = $null
        $stored = $null
        try {
            Test-LbfWriteAccess $Target
            $stored = Get-LbfStoredFiles $Target $id
        } catch { $err = $_ }
        Set-LbfStatus ''
        if ($null -ne $err) { throw $err }
        if ($stored.ContainsKey($script:CrateName)) {
            Confirm-LbfPublished $b $Publication $stored
            return
        }
        if ($stored.Count -gt 0) { Write-LbfLog "Resuming ${id}: an earlier attempt stored $($stored.Count) of its $($Publication.Files.Count) files`n" }
        $cmp = Compare-LbfRecorded $Publication.Files $stored
        if ($cmp.Problems.Count -gt 0) {
            throw (New-LbfTakenError "an earlier attempt at $id stored different files, and lbf never changes a stored file, so nothing was uploaded" $cmp.Problems)
        }

        $total = [long]0
        $jobs = New-Object System.Collections.Generic.List[hashtable]
        foreach ($n in $cmp.ToSend) {
            $f = $Publication.Files[$n]
            $total += $f.Size
            $jobs.Add((New-LbfJob 'put' $f.Rel $f.Size @{ File = $f; Blobs = $b }))
        }
        Write-LbfLog "Uploading $($jobs.Count) files ($(Format-LbfBytes $total)) from $($Publication.Source) as $id`n"

        $started = [System.Diagnostics.Stopwatch]::StartNew()
        $crate = $null
        $err = $null
        try {
            Invoke-LbfTransferAll (New-LbfProgress $jobs.Count $total) $jobs
            $crate = Get-LbfPublicationCrateBytes $Publication $Target $published
            Send-LbfCrate $b $crate
        } catch { $err = $_ }
        if (Test-LbfInterrupted) { throw (Get-LbfInterruptedPublishError $b) }
        if ($null -ne $err) {
            if (Test-LbfErrorFlag $err 'lbfAnotherCrate') {
                throw (New-LbfError "${id}: $(Get-LbfErrorText $err). Run the same publish with --id $id to check it records these files")
            }
            if (Test-LbfExitCode $err $script:ExitIDTaken) {
                throw (New-LbfError "$(Get-LbfErrorText $err)`n$id was left without a crate, so fetch will refuse it. Publish without --id, or with a new ID from 'lbf new-id'" -ExitCode $script:ExitIDTaken)
            }
            throw (New-LbfWrappedError '' $err "`n$id was left without a crate, so fetch will refuse it. Run the same publish with --id $id to finish it")
        }
        Test-LbfLanded $b $crate
        Format-LbfDone $started $total
    } finally {
        Exit-LbfInterruptible
    }
}

# ---------- Fetch ----------

function Stop-LbfRange {
    param([hashtable]$Range)
    if ($null -ne $Range.Op) { Stop-LbfBlobOp $Range.Op; $Range.Op = $null }
    if ($null -ne $Range.Task) { try { [void]$Range.Task.Wait(5000) } catch { }; $Range.Task = $null }
    if ($null -ne $Range.Fs) { try { $Range.Fs.Dispose() } catch { }; $Range.Fs = $null }
    if ($null -ne $Range.Response) { try { $Range.Response.Dispose() } catch { }; $Range.Response = $null }
}

# Go's createIn: the file and its folders inside root, refusing names that would land outside it.
function New-LbfFileIn {
    param([Parameter(Mandatory)][string]$Root, [Parameter(Mandatory)][string]$Rel)
    $sep = [string][IO.Path]::DirectorySeparatorChar
    $name = $Rel
    if ($script:OnWindows) { $name = $name.Replace('/', '\') }
    $segments = New-Object System.Collections.Generic.List[string]
    $absolute = $Rel.StartsWith('/', [StringComparison]::Ordinal) -or ($script:OnWindows -and ($Rel -cmatch '^[A-Za-z]:' -or $Rel.StartsWith('\', [StringComparison]::Ordinal)))
    $parts = $Rel -split '[/]'
    if ($script:OnWindows) { $parts = $Rel -split '[/\\]' }
    for ($i = 0; $i -lt $parts.Count - 1; $i++) {
        $s = $parts[$i]
        if (Test-LbfIn $s '', '.') { continue }
        if ((Test-LbfSame $s '..') -and $segments.Count -gt 0 -and -not (Test-LbfSame $segments[$segments.Count - 1] '..')) { $segments.RemoveAt($segments.Count - 1); continue }
        $segments.Add($s)
    }
    $dir = $segments -join $sep
    if ($absolute) { $dir = $sep + $dir }
    if ($dir.Length -eq 0) { $dir = '.' }
    if ($absolute -or $segments.Contains('..')) { throw (New-LbfError "blob ${Rel}: mkdirat ${dir}: path escapes from parent") }
    $last = $parts[$parts.Count - 1]
    if (Test-LbfIn $last '..', '.', '') { throw (New-LbfError "blob ${Rel}: openat ${name}: path escapes from parent") }
    $dirFull = $Root
    if ($segments.Count -gt 0) { $dirFull = [IO.Path]::Combine($Root, $dir) }
    $full = [IO.Path]::Combine($dirFull, $last)
    try {
        [void][IO.Directory]::CreateDirectory((Get-LbfIoPath $dirFull))
    } catch { throw (New-LbfError "blob ${Rel}: mkdirat ${dir}: $(Get-LbfOsReason $_)") }
    try {
        $fs = New-Object System.IO.FileStream ((Get-LbfIoPath $full), [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::ReadWrite)
        $fs.Dispose()
    } catch { throw (New-LbfError "blob ${Rel}: openat ${name}: $(Get-LbfOsReason $_)") }
    return $full
}

function Get-LbfHeaderValue {
    param($Response, [string]$Name)
    $vals = $null
    if ($Response.Headers.TryGetValues($Name, [ref]$vals)) { foreach ($v in $vals) { return $v } }
    if ($null -ne $Response.Content -and $Response.Content.Headers.TryGetValues($Name, [ref]$vals)) { foreach ($v in $vals) { return $v } }
    return $null
}

function New-LbfRangeOp {
    param([hashtable]$Job, [long]$Offset, [long]$Count)
    $headers = @{ 'x-ms-range' = "bytes=$Offset-$($Offset + $Count - 1)" }
    if ($null -ne $Job.ETag) { $headers['If-Match'] = $Job.ETag }
    $req = New-LbfBlobRequest -Target $Job.Blobs.Target -Blob "$($Job.Blobs.Id)/$($Job.File.Rel)" -Headers $headers -Stream
    return New-LbfBlobOp $req $Job.Cts.Token
}

function Start-LbfGetJob {
    param([hashtable]$Job)
    $Job.Path = New-LbfFileIn $Job.Dir $Job.File.Rel
    $Job.Ranges = New-Object System.Collections.Generic.List[hashtable]
    $Job.Downloaded = [long]0
    $Job.ETag = $null
    $Job.Pending = New-Object System.Collections.Generic.Queue[object]
    $Job.Ranges.Add(@{ Offset = [long]0; Count = [long]$script:BlockSize; First = $true; Op = (New-LbfRangeOp $Job 0 $script:BlockSize); Task = $null; Fs = $null; Response = $null; Tries = 0 })
    $Job.Stage = 'ranges'
}

# Advances one ranged read; true once its bytes are in the file.
function Step-LbfRange {
    param([hashtable]$Job, [hashtable]$Range)
    if ($null -ne $Range.Op) {
        if (-not (Step-LbfBlobOp $Range.Op)) { return $false }
        $op = $Range.Op
        $Range.Op = $null
        try {
            $r = Complete-LbfBlobOp $op @(200, 206)
        } catch {
            if ($Range.First -and (Test-LbfAzCode $_ @('InvalidRange'))) { $Range.Count = 0; $Job.Total = [long]0; return $true }
            throw (New-LbfWrappedError "downloading $($Job.Blobs.Id)/$($Job.File.Rel): " $_)
        }
        $resp = $r.Response
        if ($Range.First) {
            $cr = Get-LbfHeaderValue $resp 'Content-Range'
            $len = $resp.Content.Headers.ContentLength
            if ($null -ne $cr) {
                $m = [regex]::Match($cr, '^bytes ([0-9]+)-([0-9]+)/([0-9]+)')
                if (-not $m.Success -or [long]$m.Groups[3].Value -le 0) {
                    $resp.Dispose()
                    throw (New-LbfError "downloading $($Job.Blobs.Id)/$($Job.File.Rel): unable to parse total size from Content-Range header: $cr")
                }
                $Job.Total = [long]$m.Groups[3].Value
                $Range.Count = [long]$m.Groups[2].Value - [long]$m.Groups[1].Value + 1
            } elseif ($null -ne $len) {
                $Job.Total = [long]$len
                $Range.Count = [long]$len
            } else {
                $resp.Dispose()
                throw (New-LbfError "downloading $($Job.Blobs.Id)/$($Job.File.Rel): response contained no content headers; this may indicate a 304 Not Modified due to access conditions")
            }
            $etag = Get-LbfHeaderValue $resp 'ETag'
            if ($null -ne $etag) { $Job.ETag = $etag }
            if ($Range.Count -le 0) { $resp.Dispose(); $Range.Count = 0; return $true }
            # As Go's DownloadFile truncates first; .NET Framework otherwise lets one range's write shrink the file under another's.
            $pre = New-Object System.IO.FileStream ((Get-LbfIoPath $Job.Path), [IO.FileMode]::Open, [IO.FileAccess]::Write, [IO.FileShare]::ReadWrite)
            try { $pre.SetLength($Job.Total) } finally { $pre.Dispose() }
        }
        $Range.Response = $resp
        $Range.Fs = New-Object System.IO.FileStream ((Get-LbfIoPath $Job.Path), [IO.FileMode]::Open, [IO.FileAccess]::Write, [IO.FileShare]::ReadWrite, 65536, $true)
        [void]$Range.Fs.Seek($Range.Offset, [IO.SeekOrigin]::Begin)
        $Range.Task = $resp.Content.CopyToAsync($Range.Fs)
        return $false
    }
    if ($null -eq $Range.Task) { return $true }
    if (-not $Range.Task.IsCompleted) { return $false }
    $t = $Range.Task
    $Range.Task = $null
    $written = [long]0
    try { $Range.Fs.Flush() } catch { }
    try { $written = $Range.Fs.Position - $Range.Offset } catch { }
    $Range.Fs.Dispose(); $Range.Fs = $null
    $Range.Response.Dispose(); $Range.Response = $null
    if ($t.IsFaulted -or $t.IsCanceled -or $written -ne $Range.Count) {
        if ($Job.Cts.IsCancellationRequested) { throw (New-LbfCanceledError) }
        if ($Range.Tries -lt 3) {
            $Range.Tries++
            $Range.Op = New-LbfRangeOp $Job $Range.Offset $Range.Count
            return $false
        }
        $reason = 'unexpected EOF'
        if ($t.IsFaulted) { $reason = Get-LbfNetReason $t.Exception $null }
        throw (New-LbfError "downloading $($Job.Blobs.Id)/$($Job.File.Rel): $reason")
    }
    return $true
}

function Step-LbfGetJob {
    param([hashtable]$Job)
    $f = $Job.File
    switch ($Job.Stage) {
        'ranges' {
            $inFlight = [long]0
            foreach ($r in @($Job.Ranges)) {
                $done = $false
                try { $done = Step-LbfRange $Job $r } catch {
                    foreach ($o in @($Job.Ranges)) { Stop-LbfRange $o }
                    $Job.Ranges.Clear()
                    throw
                }
                if ($done) {
                    [void]$Job.Ranges.Remove($r)
                    $Job.Downloaded += $r.Count
                    if ($r.First -and $Job.Total -gt $r.Count) {
                        for ($off = $r.Count; $off -lt $Job.Total; $off += $script:BlockSize) {
                            $Job.Pending.Enqueue(@($off, [Math]::Min([long]$script:BlockSize, $Job.Total - $off)))
                        }
                    }
                } elseif ($null -ne $r.Fs) {
                    try { $inFlight += [Math]::Max([long]0, $r.Fs.Position - $r.Offset) } catch { }
                }
            }
            while ($Job.Ranges.Count -lt $script:BlockWorkers -and $Job.Pending.Count -gt 0) {
                $p = $Job.Pending.Dequeue()
                $r = @{ Offset = [long]$p[0]; Count = [long]$p[1]; First = $false; Op = (New-LbfRangeOp $Job $p[0] $p[1]); Task = $null; Fs = $null; Response = $null; Tries = 0 }
                $Job.Ranges.Add($r)
                [void](Step-LbfBlobOp $r.Op)
            }
            $Job.Bar.N = $Job.Downloaded + $inFlight
            if ($Job.Ranges.Count -gt 0) { return $false }
            $info = New-Object System.IO.FileInfo (Get-LbfIoPath $Job.Path)
            if ($info.Length -ne $Job.Downloaded) {
                $fs = New-Object System.IO.FileStream ((Get-LbfIoPath $Job.Path), [IO.FileMode]::Open, [IO.FileAccess]::Write, [IO.FileShare]::ReadWrite)
                try { $fs.SetLength($Job.Downloaded) } finally { $fs.Dispose() }
            }
            $info.Refresh()
            if ($info.Length -ne $f.Size) { throw (New-LbfError "$($f.Rel) is $($info.Length) bytes, not $($f.Size) after download; fetch again, and if it recurs the stored copy differs from what was published") }
            if ([string]::IsNullOrEmpty($f.SHA256)) { return $true }
            $Job.Fs = New-Object System.IO.FileStream ((Get-LbfIoPath $Job.Path), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite, 1048576, $true)
            $Job.Sha = New-LbfSha256
            $Job.Crypto = New-Object System.Security.Cryptography.CryptoStream ([IO.Stream]::Null), $Job.Sha, ([System.Security.Cryptography.CryptoStreamMode]::Write)
            $Job.Tasks.Add($Job.Fs.CopyToAsync($Job.Crypto, 1048576, $Job.Cts.Token))
            $Job.Stage = 'verify'
            return $false
        }
        'verify' {
            $t = $Job.Tasks[0]
            if (-not $t.IsCompleted) { return $false }
            $Job.Tasks.Clear()
            if ($t.IsCanceled -or $Job.Cts.IsCancellationRequested) { throw (New-LbfCanceledError) }
            if ($t.IsFaulted) { throw (New-LbfError (Format-LbfOsError 'read' $f.Rel $t.Exception)) }
            $Job.Fs.Dispose(); $Job.Fs = $null
            $Job.Crypto.FlushFinalBlock()
            if (-not (Test-LbfSame (ConvertTo-LbfHex $Job.Sha.Hash) $f.SHA256)) {
                throw (New-LbfError "$($f.Rel) has the wrong sha256 after download; fetch again, and if it recurs the stored copy differs from what was published")
            }
            return $true
        }
    }
    return $true
}

# Go's checkFile: why the local copy of a file is wrong, or ''.
function Get-LbfFileProblem {
    param([Parameter(Mandatory)][string]$Root, [Parameter(Mandatory)][hashtable]$File)
    $path = Get-LbfIoPath (Join-LbfRel $Root $File.Rel)
    if (-not [IO.File]::Exists($path)) {
        if ([IO.Directory]::Exists($path)) { return "$($File.Rel) is a folder, not $($File.Size) bytes" }
        return "$($File.Rel) is missing"
    }
    $size = (New-Object System.IO.FileInfo $path).Length
    if ($size -ne $File.Size) { return "$($File.Rel) is $size bytes, not $($File.Size)" }
    if ([string]::IsNullOrEmpty($File.SHA256)) { return '' }
    try { $sum = Get-LbfFileSha256 $path } catch { throw (New-LbfError (Format-LbfOsError 'read' $File.Rel $_)) }
    if (-not (Test-LbfSame $sum $File.SHA256)) { return "$($File.Rel) has the wrong sha256" }
    return ''
}

# Go's verifyDir: why root does not hold exactly these files and crate, or ''.
function Get-LbfDirProblem {
    param([Parameter(Mandatory)][string]$Root, [Parameter(Mandatory)][hashtable]$Crate)
    if (-not [IO.Directory]::Exists($Root)) {
        if ([IO.File]::Exists($Root)) { throw (New-LbfError "open ${Root}: not a directory") }
        throw (New-LbfError (Format-LbfOsError 'open' $Root (New-Object System.IO.DirectoryNotFoundException)))
    }
    $want = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    [void]$want.Add($script:CrateName)
    foreach ($f in $Crate.Files) {
        [void]$want.Add($f.Rel)
        $problem = Get-LbfFileProblem $Root $f
        if ($problem.Length -ne 0) { return $problem }
    }
    $local = $null
    try { $local = [IO.File]::ReadAllBytes((Get-LbfIoPath (Join-LbfRel $Root $script:CrateName))) } catch { $local = $null }
    if ($null -eq $local -or -not (Test-LbfBytesEqual $local $Crate.Raw)) { return "$($script:CrateName) differs" }
    return Find-LbfExtraFile $Root '' $want
}

# fs.WalkDir order: names sorted, each folder entered where it sorts.
function Find-LbfExtraFile {
    param([string]$Dir, [string]$Prefix, $Want)
    $entries = @([IO.Directory]::GetFileSystemEntries((Get-LbfIoPath $Dir)))
    $names = New-Object System.Collections.Generic.List[string]
    foreach ($e in $entries) { $names.Add([IO.Path]::GetFileName($e)) }
    foreach ($n in (Get-LbfSortedUtf8 $names.ToArray())) {
        $path = [IO.Path]::Combine($Dir, $n)
        $rel = $n
        if ($Prefix.Length -ne 0) { $rel = "$Prefix/$n" }
        $attrs = [IO.File]::GetAttributes((Get-LbfIoPath $path))
        $isDir = ($attrs -band [IO.FileAttributes]::Directory) -and -not ($attrs -band [IO.FileAttributes]::ReparsePoint)
        if ($isDir) {
            $found = Find-LbfExtraFile $path $rel $Want
            if ($found.Length -ne 0) { return $found }
        } elseif (-not $Want.Contains($rel)) {
            return "$rel is not part of the dataset"
        }
    }
    return ''
}

function Assert-LbfExisting {
    param([string]$Root, [string]$Id, [hashtable]$Crate)
    $problem = Get-LbfDirProblem $Root $Crate
    if ($problem.Length -ne 0) { throw (New-LbfError "$Root already exists but is not dataset $Id ($problem); move it aside or fetch with another --out") }
}

# The folder that was published, which every file sits under; root itself if they do not share one.
function Get-LbfDataDir {
    param([string]$Root, $Files)
    $top = ''
    foreach ($f in $Files) {
        $slash = $f.Rel.IndexOf([char]'/')
        if ($slash -lt 0) { return $Root }
        $first = $f.Rel.Substring(0, $slash)
        if ($top.Length -ne 0 -and -not (Test-LbfSame $first $top)) { return $Root }
        $top = $first
    }
    if ($top.Length -eq 0) { return $Root }
    return Join-LbfRel $Root $top
}

function Get-LbfStalePartials {
    param([string]$Root, [double]$Hours)
    $stale = New-Object System.Collections.Generic.List[string]
    $parent = [IO.Path]::GetDirectoryName($Root)
    $prefix = [IO.Path]::GetFileName($Root) + '.partial-'
    $names = New-Object System.Collections.Generic.List[string]
    try { foreach ($e in [IO.Directory]::GetFileSystemEntries($parent)) { $n = [IO.Path]::GetFileName($e); if ($n.StartsWith($prefix, [StringComparison]::Ordinal)) { $names.Add($n) } } } catch { }
    foreach ($n in (Get-LbfSortedUtf8 $names.ToArray())) {
        $p = [IO.Path]::Combine($parent, $n)
        if ([IO.Directory]::Exists($p) -and ([DateTime]::UtcNow - [IO.Directory]::GetLastWriteTimeUtc($p)).TotalHours -gt $Hours) { $stale.Add($p) }
    }
    return , $stale.ToArray()
}

# Go's os.MkdirAll, which names the nearest existing ancestor that is not a folder.
function New-LbfDirectoryAll {
    param([Parameter(Mandatory)][string]$Path)
    $p = $Path
    while (-not [string]::IsNullOrEmpty($p)) {
        if ([IO.Directory]::Exists($p)) { break }
        if ([IO.File]::Exists($p)) { throw (New-LbfError "mkdir ${p}: $(Get-LbfNotDirReason)") }
        $p = [IO.Path]::GetDirectoryName($p)
    }
    try { [void][IO.Directory]::CreateDirectory($Path) } catch { throw (New-LbfError (Format-LbfOsError 'mkdir' $Path $_)) }
}

# Each fetch downloads into a folder of its own, so several fetches of one dataset into one place never touch each other's.
function New-LbfPartial {
    param([string]$Root)
    $parent = [IO.Path]::GetDirectoryName($Root)
    New-LbfDirectoryAll $parent
    $stale = Get-LbfStalePartials $Root 24
    if ($stale.Count -gt 0) {
        Write-LbfLog "$($stale.Count) download folders of $([IO.Path]::GetFileName($Root)) are over a day old ($($stale -join ', ')); fetches that were killed leave them, and they can be deleted once no fetch of it is running`n"
    }
    $alphabet = 'abcdefghijklmnopqrstuvwxyz234567'
    while ($true) {
        $suffix = ''
        foreach ($b in (Get-LbfRandomBytes 8)) { $suffix += $alphabet[$b % 32] }
        $partial = "$Root.partial-$suffix"
        if ([IO.Directory]::Exists($partial) -or [IO.File]::Exists($partial)) { continue }
        try { [void][IO.Directory]::CreateDirectory($partial) } catch { throw (New-LbfError (Format-LbfOsError 'mkdir' $partial $_)) }
        return $partial
    }
}

function Test-LbfAnyPathExists {
    param([string]$Path)
    if ([IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path)) { return $true }
    try { return $null -ne (Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue) } catch { return $false }
}

# Go's localNameProblems: names this computer cannot hold.
function Get-LbfLocalNameProblems {
    param($Files, [string]$Goos)
    $problems = New-Object System.Collections.Generic.List[string]
    $fold = $Goos -eq 'windows' -or $Goos -eq 'darwin'
    $rels = New-Object System.Collections.Generic.List[string]
    foreach ($f in $Files) {
        $rels.Add($f.Rel)
        if ($Goos -eq 'windows') {
            $p = Get-LbfNameProblem $f.Rel
            if ($p.Length -ne 0) { $problems.Add("$($f.Rel): $p") }
        }
        if (Test-LbfClashesWithCrate $f.Rel $fold) { $problems.Add("$($f.Rel): would be replaced by the dataset's crate, which is saved beside it") }
    }
    if ($fold) { foreach ($p in (Get-LbfSameNameProblems $rels.ToArray() 'this computer')) { $problems.Add($p) } }
    return , $problems.ToArray()
}

# What the crate states, for --json; a crate lbf cannot read is still fetched, without it.
function Get-LbfStatedBy {
    param([string]$Id, [hashtable]$Crate)
    try {
        $st = Get-LbfCrateStatement $Crate
    } catch {
        Write-LbfLog "[fetch] cannot read what the crate of $Id states: $(Get-LbfErrorText $_)`n"
        return $null
    }
    return ConvertTo-LbfProvenanceValue $st.Provenance
}

# Go's download.
function Invoke-LbfDownload {
    param([Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][string]$Id, [Parameter(Mandatory)][string]$OutDir)
    Initialize-LbfBlobAccess $Target
    Enter-LbfInterruptible
    try {
        Set-LbfStatus "Listing $Id"
        $stored = Get-LbfStoredFiles $Target $Id
        if ($stored.Count -eq 0) { throw (New-LbfError "no dataset $Id in $(Get-LbfContainerUrl $Target)") }
        if (-not $stored.ContainsKey($script:CrateName)) {
            throw (New-LbfError "dataset $Id has no $($script:CrateName), so its upload never finished; nothing was downloaded. Whoever published it can finish it by running the same publish with --id $Id")
        }
        Set-LbfStatus "Reading the crate of $Id"
        try { $raw = Read-LbfBlob $Target "$Id/$($script:CrateName)" } catch { throw (New-LbfWrappedError "downloading the crate of ${Id}: " $_) }
        Set-LbfStatus ''
        try { $crate = Read-LbfStoredCrate $raw } catch { throw (New-LbfWrappedError "dataset ${Id}: " $_) }
        $files = $crate.Files
        $problems = Get-LbfCrateDifferences $crate $stored $false
        if ($problems.Count -gt 0) { throw (New-LbfProblemList "dataset $Id does not match its crate, so nothing was downloaded:" $problems) }
        $problems = Get-LbfLocalNameProblems $files (Get-LbfGoos)
        if ($problems.Count -gt 0) { throw (New-LbfProblemList "dataset $Id cannot be saved on this computer, so nothing was downloaded:" $problems) }

        $root = [IO.Path]::GetFullPath((Join-LbfRel (Get-LbfFullPath $OutDir) $Id))
        if (Test-LbfAnyPathExists $root) {
            Set-LbfStatus "Checking the existing $root"
            Assert-LbfExisting $root $Id $crate
            Write-LbfLog "$root is already here and matches its crate`n"
            return @{ Path = $root; DataPath = (Get-LbfDataDir $root $files); Stated = (Get-LbfStatedBy $Id $crate) }
        }

        $total = [long]0
        $hashed = 0
        foreach ($f in $files) { $total += $f.Size; if (-not [string]::IsNullOrEmpty($f.SHA256)) { $hashed++ } }
        Write-LbfLog "Downloading $($files.Count) files ($(Format-LbfBytes $total)) to $root`n"
        if ($hashed -lt $files.Count) {
            Write-LbfLog "[verify] $($files.Count - $hashed) of $($files.Count) files have no checksum in the crate (published before lbf recorded them); only their sizes are checked`n"
        }

        $partial = New-LbfPartial $root
        $renamed = $false
        try {
            $started = [System.Diagnostics.Stopwatch]::StartNew()
            $jobs = New-Object System.Collections.Generic.List[hashtable]
            $b = @{ Target = $Target; Id = $Id }
            foreach ($f in $files) { $jobs.Add((New-LbfJob 'get' $f.Rel $f.Size @{ File = $f; Blobs = $b; Dir = $partial })) }
            $err = $null
            try {
                Invoke-LbfTransferAll (New-LbfProgress $jobs.Count $total) $jobs
                [IO.File]::WriteAllBytes((Get-LbfIoPath (Join-LbfRel $partial $script:CrateName)), $raw)
            } catch { $err = $_ }
            if (Test-LbfInterrupted) { throw (New-LbfError "interrupted; nothing was saved as $root, and the next fetch starts again") }
            if ($null -ne $err) { throw $err }
            try {
                [IO.Directory]::Move((Get-LbfIoPath $partial), (Get-LbfIoPath $root))
                $renamed = $true
            } catch {
                $moveErr = $_
                if (-not (Test-LbfAnyPathExists $root)) { throw (New-LbfError "rename $partial ${root}: $(Get-LbfOsReason $moveErr)") }
                Assert-LbfExisting $root $Id $crate
                Write-LbfLog "$root was saved meanwhile by another fetch, and matches its crate`n"
            }
            Format-LbfDone $started $total
            return @{ Path = $root; DataPath = (Get-LbfDataDir $root $files); Stated = (Get-LbfStatedBy $Id $crate) }
        } finally {
            if (-not $renamed) { try { [IO.Directory]::Delete((Get-LbfIoPath $partial), $true) } catch { } }
        }
    } finally {
        Set-LbfStatus ''
        Exit-LbfInterruptible
    }
}
