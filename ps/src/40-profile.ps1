$script:VersionFolder = New-Object System.Text.RegularExpressions.Regex ('\Av?[0-9]+(\.[0-9]+)*\z')
$script:ProcessRunCrate = 'https://w3id.org/ro/wfrun/process/0.6'
$script:Draft202012 = 'https://json-schema.org/draft/2020-12/schema'
$script:MetaSchemaDocs = @'
{"https://json-schema.org/draft/2020-12/schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/schema","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/core":true,"https://json-schema.org/draft/2020-12/vocab/applicator":true,"https://json-schema.org/draft/2020-12/vocab/unevaluated":true,"https://json-schema.org/draft/2020-12/vocab/validation":true,"https://json-schema.org/draft/2020-12/vocab/meta-data":true,"https://json-schema.org/draft/2020-12/vocab/format-annotation":true,"https://json-schema.org/draft/2020-12/vocab/content":true},"$dynamicAnchor":"meta","title":"Core and Validation specifications meta-schema","allOf":[{"$ref":"meta/core"},{"$ref":"meta/applicator"},{"$ref":"meta/unevaluated"},{"$ref":"meta/validation"},{"$ref":"meta/meta-data"},{"$ref":"meta/format-annotation"},{"$ref":"meta/content"}],"type":["object","boolean"],"$comment":"This meta-schema also defines keywords that have appeared in previous drafts in order to prevent incompatible extensions as they remain in common use.","properties":{"definitions":{"$comment":"\"definitions\" has been replaced by \"$defs\".","type":"object","additionalProperties":{"$dynamicRef":"#meta"},"deprecated":true,"default":{}},"dependencies":{"$comment":"\"dependencies\" has been split and replaced by \"dependentSchemas\" and \"dependentRequired\" in order to serve their differing semantics.","type":"object","additionalProperties":{"anyOf":[{"$dynamicRef":"#meta"},{"$ref":"meta/validation#/$defs/stringArray"}]},"deprecated":true,"default":{}},"$recursiveAnchor":{"$comment":"\"$recursiveAnchor\" has been replaced by \"$dynamicAnchor\".","$ref":"meta/core#/$defs/anchorString","deprecated":true},"$recursiveRef":{"$comment":"\"$recursiveRef\" has been replaced by \"$dynamicRef\".","$ref":"meta/core#/$defs/uriReferenceString","deprecated":true}}},"https://json-schema.org/draft/2020-12/meta/core":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/core","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/core":true},"$dynamicAnchor":"meta","title":"Core vocabulary meta-schema","type":["object","boolean"],"properties":{"$id":{"$ref":"#/$defs/uriReferenceString","$comment":"Non-empty fragments not allowed.","pattern":"^[^#]*#?$"},"$schema":{"$ref":"#/$defs/uriString"},"$ref":{"$ref":"#/$defs/uriReferenceString"},"$anchor":{"$ref":"#/$defs/anchorString"},"$dynamicRef":{"$ref":"#/$defs/uriReferenceString"},"$dynamicAnchor":{"$ref":"#/$defs/anchorString"},"$vocabulary":{"type":"object","propertyNames":{"$ref":"#/$defs/uriString"},"additionalProperties":{"type":"boolean"}},"$comment":{"type":"string"},"$defs":{"type":"object","additionalProperties":{"$dynamicRef":"#meta"}}},"$defs":{"anchorString":{"type":"string","pattern":"^[A-Za-z_][-A-Za-z0-9._]*$"},"uriString":{"type":"string","format":"uri"},"uriReferenceString":{"type":"string","format":"uri-reference"}}},"https://json-schema.org/draft/2020-12/meta/applicator":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/applicator","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/applicator":true},"$dynamicAnchor":"meta","title":"Applicator vocabulary meta-schema","type":["object","boolean"],"properties":{"prefixItems":{"$ref":"#/$defs/schemaArray"},"items":{"$dynamicRef":"#meta"},"contains":{"$dynamicRef":"#meta"},"additionalProperties":{"$dynamicRef":"#meta"},"properties":{"type":"object","additionalProperties":{"$dynamicRef":"#meta"},"default":{}},"patternProperties":{"type":"object","additionalProperties":{"$dynamicRef":"#meta"},"propertyNames":{"format":"regex"},"default":{}},"dependentSchemas":{"type":"object","additionalProperties":{"$dynamicRef":"#meta"},"default":{}},"propertyNames":{"$dynamicRef":"#meta"},"if":{"$dynamicRef":"#meta"},"then":{"$dynamicRef":"#meta"},"else":{"$dynamicRef":"#meta"},"allOf":{"$ref":"#/$defs/schemaArray"},"anyOf":{"$ref":"#/$defs/schemaArray"},"oneOf":{"$ref":"#/$defs/schemaArray"},"not":{"$dynamicRef":"#meta"}},"$defs":{"schemaArray":{"type":"array","minItems":1,"items":{"$dynamicRef":"#meta"}}}},"https://json-schema.org/draft/2020-12/meta/unevaluated":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/unevaluated","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/unevaluated":true},"$dynamicAnchor":"meta","title":"Unevaluated applicator vocabulary meta-schema","type":["object","boolean"],"properties":{"unevaluatedItems":{"$dynamicRef":"#meta"},"unevaluatedProperties":{"$dynamicRef":"#meta"}}},"https://json-schema.org/draft/2020-12/meta/validation":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/validation","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/validation":true},"$dynamicAnchor":"meta","title":"Validation vocabulary meta-schema","type":["object","boolean"],"properties":{"type":{"anyOf":[{"$ref":"#/$defs/simpleTypes"},{"type":"array","items":{"$ref":"#/$defs/simpleTypes"},"minItems":1,"uniqueItems":true}]},"const":true,"enum":{"type":"array","items":true},"multipleOf":{"type":"number","exclusiveMinimum":0},"maximum":{"type":"number"},"exclusiveMaximum":{"type":"number"},"minimum":{"type":"number"},"exclusiveMinimum":{"type":"number"},"maxLength":{"$ref":"#/$defs/nonNegativeInteger"},"minLength":{"$ref":"#/$defs/nonNegativeIntegerDefault0"},"pattern":{"type":"string","format":"regex"},"maxItems":{"$ref":"#/$defs/nonNegativeInteger"},"minItems":{"$ref":"#/$defs/nonNegativeIntegerDefault0"},"uniqueItems":{"type":"boolean","default":false},"maxContains":{"$ref":"#/$defs/nonNegativeInteger"},"minContains":{"$ref":"#/$defs/nonNegativeInteger","default":1},"maxProperties":{"$ref":"#/$defs/nonNegativeInteger"},"minProperties":{"$ref":"#/$defs/nonNegativeIntegerDefault0"},"required":{"$ref":"#/$defs/stringArray"},"dependentRequired":{"type":"object","additionalProperties":{"$ref":"#/$defs/stringArray"}}},"$defs":{"nonNegativeInteger":{"type":"integer","minimum":0},"nonNegativeIntegerDefault0":{"$ref":"#/$defs/nonNegativeInteger","default":0},"simpleTypes":{"enum":["array","boolean","integer","null","number","object","string"]},"stringArray":{"type":"array","items":{"type":"string"},"uniqueItems":true,"default":[]}}},"https://json-schema.org/draft/2020-12/meta/meta-data":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/meta-data","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/meta-data":true},"$dynamicAnchor":"meta","title":"Meta-data vocabulary meta-schema","type":["object","boolean"],"properties":{"title":{"type":"string"},"description":{"type":"string"},"default":true,"deprecated":{"type":"boolean","default":false},"readOnly":{"type":"boolean","default":false},"writeOnly":{"type":"boolean","default":false},"examples":{"type":"array","items":true}}},"https://json-schema.org/draft/2020-12/meta/format-annotation":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/format-annotation","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/format-annotation":true},"$dynamicAnchor":"meta","title":"Format vocabulary meta-schema for annotation results","type":["object","boolean"],"properties":{"format":{"type":"string"}}},"https://json-schema.org/draft/2020-12/meta/content":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://json-schema.org/draft/2020-12/meta/content","$vocabulary":{"https://json-schema.org/draft/2020-12/vocab/content":true},"$dynamicAnchor":"meta","title":"Content vocabulary meta-schema","type":["object","boolean"],"properties":{"contentEncoding":{"type":"string"},"contentMediaType":{"type":"string"},"contentSchema":{"$dynamicRef":"#meta"}}}}
'@

# ---------- profile JSON: Go's encoding/json (v1 on v2) for the head, numbers kept as written ----------

# A JSON number as written, for messages that print it, and as a value, for comparing.
function New-LbfJsonNumber {
    param([string]$Text)
    $n = $null
    $d = [decimal]0
    if ([decimal]::TryParse($Text, [Globalization.NumberStyles]::Float, $script:Invariant, [ref]$d)) { $n = $d }
    else { $n = [double]::Parse($Text, $script:Invariant) }
    return @{ LbfNum = $true; T = $Text; N = $n }
}

# json.Unmarshal's syntax check, in v1's words.
function Assert-LbfLegacyJson {
    param([byte[]]$Bytes)
    $d = New-LbfJsonDecoder $Bytes
    $d.Legacy = $true
    $pos = Skip-LbfJsonSpace $d 0
    if ($pos -ge $Bytes.Length) { throw (New-LbfError 'unexpected end of JSON input') }
    $r = Read-LbfJsonWhole $d $pos
    if ($r.ContainsKey('Err')) {
        $text = $r.Err.Text
        if ($r.Err.Kind -eq 'mismatch') { $text = (New-LbfInvalidChar $d $r.At 'at start of value').Text }
        if (Test-LbfSame $text 'unexpected EOF') { $text = 'unexpected end of JSON input' }
        throw (New-LbfError (ConvertTo-LbfLegacyJsonText $text))
    }
    $pos = Skip-LbfJsonSpace $d $r.End
    if ($pos -lt $Bytes.Length) { throw (New-LbfError (ConvertTo-LbfLegacyJsonText (New-LbfInvalidChar $d $pos 'after top-level value').Text)) }
}

function ConvertTo-LbfLegacyJsonText {
    param([string]$Text)
    $i = $Text.IndexOf(' (expecting', [StringComparison]::Ordinal)
    if ($i -ge 0 -and -not $Text.Contains(' in literal')) { $Text = $Text.Substring(0, $i) }
    return $Text.Replace('object name', 'object key').Replace('at start of value', 'looking for beginning of value').Replace('at start of string', 'looking for beginning of object key string').Replace('after object value', 'after object key:value pair').Replace('in number', 'in numeric literal')
}

# Parses JSON already checked by Assert-LbfLegacyJson: OrderedDictionary (last duplicate wins), object[], strings, numbers as New-LbfJsonNumber, bools, $null.
function ConvertFrom-LbfProfileJson {
    param([byte[]]$Bytes)
    $state = @{ B = $Bytes; P = 0 }
    $v = Read-LbfProfileValue $state
    return , $v
}

function Read-LbfProfileValue {
    param([hashtable]$S)
    $b = $S.B
    while ($S.P -lt $b.Length -and ($b[$S.P] -eq 0x20 -or $b[$S.P] -eq 0x09 -or $b[$S.P] -eq 0x0A -or $b[$S.P] -eq 0x0D)) { $S.P++ }
    $c = $b[$S.P]
    if ($c -eq 0x7B) {
        $o = New-LbfOrdered
        $S.P++
        while ($true) {
            while ($b[$S.P] -eq 0x20 -or $b[$S.P] -eq 0x09 -or $b[$S.P] -eq 0x0A -or $b[$S.P] -eq 0x0D -or $b[$S.P] -eq 0x2C) { $S.P++ }
            if ($b[$S.P] -eq 0x7D) { $S.P++; return , $o }
            $k = Read-LbfProfileValue $S
            while ($b[$S.P] -ne 0x3A) { $S.P++ }
            $S.P++
            $v = Read-LbfProfileValue $S
            if ($o.Contains($k)) { $o.Remove($k) }
            $o.Add($k, $v)
        }
    }
    if ($c -eq 0x5B) {
        $l = New-Object System.Collections.Generic.List[object]
        $S.P++
        while ($true) {
            while ($b[$S.P] -eq 0x20 -or $b[$S.P] -eq 0x09 -or $b[$S.P] -eq 0x0A -or $b[$S.P] -eq 0x0D -or $b[$S.P] -eq 0x2C) { $S.P++ }
            if ($b[$S.P] -eq 0x5D) { $S.P++; return , $l.ToArray() }
            $l.Add((Read-LbfProfileValue $S))
        }
    }
    if ($c -eq 0x22) {
        $d = @{ B = $b; Legacy = $true }
        $r = Read-LbfJsonString $d $S.P
        $S.P = $r.End
        return $r.Value
    }
    if ($c -eq 0x74) { $S.P += 4; return $true }
    if ($c -eq 0x66) { $S.P += 5; return $false }
    if ($c -eq 0x6E) { $S.P += 4; return $null }
    $start = $S.P
    while ($S.P -lt $b.Length -and (($b[$S.P] -ge 0x30 -and $b[$S.P] -le 0x39) -or $b[$S.P] -eq 0x2D -or $b[$S.P] -eq 0x2B -or $b[$S.P] -eq 0x2E -or $b[$S.P] -eq 0x45 -or $b[$S.P] -eq 0x65)) { $S.P++ }
    return New-LbfJsonNumber ([Text.Encoding]::ASCII.GetString($b, $start, $S.P - $start))
}

function Get-LbfJsonTypeName {
    param($Value)
    if ($null -eq $Value) { return 'null' }
    if ($Value -is [string]) { return 'string' }
    if ($Value -is [bool]) { return 'boolean' }
    if ($Value -is [hashtable] -and $Value.ContainsKey('LbfNum')) { return 'number' }
    if ($Value -is [long] -or $Value -is [int] -or $Value -is [double] -or $Value -is [decimal]) { return 'number' }
    if ($Value -is [System.Collections.IDictionary]) { return 'object' }
    if ($Value -is [System.Collections.IList]) { return 'array' }
    return 'invalid'
}

# Go's schemaHead: $id and $ref as json.Unmarshal reads them, names matched without regard to case.
function Get-LbfSchemaHead {
    param([byte[]]$Bytes)
    Assert-LbfLegacyJson $Bytes
    $doc = ConvertFrom-LbfProfileJson $Bytes
    $id = ''
    $ref = ''
    $err = $null
    if ($doc -is [System.Collections.IDictionary]) {
        foreach ($k in $doc.Keys) {
            $field = $null
            if ([string]::Equals($k, '$id', [StringComparison]::OrdinalIgnoreCase)) { $field = '$id' }
            elseif ([string]::Equals($k, '$ref', [StringComparison]::OrdinalIgnoreCase)) { $field = '$ref' }
            if ($null -eq $field) { continue }
            $v = $doc[$k]
            if ($null -eq $v) { continue }
            if ($v -is [string]) {
                if ($field -eq '$id') { $id = $v } else { $ref = $v }
                continue
            }
            if ($null -eq $err) {
                $err = "json: cannot unmarshal $(Get-LbfLegacyKind $v) into Go struct field .$k of type string"
            }
        }
    } elseif ($null -ne $doc) {
        $err = "json: cannot unmarshal $(Get-LbfLegacyKind $doc) into Go value of type struct { ID string " + '"json:\"$id\""; Ref string "json:\"$ref\"" }'
    }
    if ($null -ne $err) { throw (New-LbfError $err) }
    if ($id.Length -eq 0) { throw (New-LbfError "no `$id; it is recorded as the crate's conformsTo") }
    if (-not (Test-LbfProfileUrl $id)) {
        throw (New-LbfError "`$id $(Format-LbfQuote $id) is not an absolute URL such as https://example.org/profiles/name/0.1.0; it is recorded as the crate's conformsTo")
    }
    return @{ ID = $id; Ref = $ref; Doc = $doc }
}

function Get-LbfLegacyKind {
    param($Value)
    switch (Get-LbfJsonTypeName $Value) {
        'boolean' { return 'bool' }
        default { return $_ }
    }
}

# url.Parse(id) succeeding with a scheme and a host, and no fragment.
function Test-LbfProfileUrl {
    param([string]$Id)
    if ($Id.Contains('#')) { return $false }
    if (-not (Test-LbfAbsoluteUrl $Id)) { return $false }
    $m = [regex]::Match($Id, '\A[A-Za-z][A-Za-z0-9+.\-]*://([^/?#]*)')
    if (-not $m.Success) { return $false }
    $auth = $m.Groups[1].Value
    $at = $auth.LastIndexOf([char]'@')
    if ($at -ge 0) { $auth = $auth.Substring($at + 1) }
    return $auth.Length -ne 0
}

# ---------- loading profiles ----------

# Go's loadProfile: the chosen profile and every profile it builds on through $ref, bronze first.
function Get-LbfProfile {
    param([AllowEmptyString()][string]$Dir)
    $bronzeBytes = $script:Utf8NoBom.GetBytes($script:EmbeddedBronzeProfile)
    $bronze = Get-LbfSchemaHead $bronzeBytes
    $bronzeId = $bronze.ID
    $docs = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    $parents = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    $paths = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    $docs[$bronzeId] = $bronze.Doc
    $paths[$bronzeId] = $bronzeId
    $targetId = $bronzeId
    if ($Dir.Length -ne 0) {
        $Dir = Get-LbfAbsPath $Dir
        $target = Join-LbfPath $Dir 'profile.json'
        if ([IO.File]::Exists((Get-LbfIoPath $Dir))) { $target = $Dir }
        $root = Get-LbfDir (Get-LbfDir $target)
        if ($script:VersionFolder.IsMatch((Get-LbfBase (Get-LbfDir $target)))) { $root = Get-LbfDir $root }
        $candidates = New-Object System.Collections.Generic.List[string]
        foreach ($sub in (Get-LbfSortedDirs $root)) {
            $p = Join-LbfPath $sub 'profile.json'
            if (Test-LbfPathExists $p) { $candidates.Add($p) }
        }
        foreach ($sub in (Get-LbfSortedDirs $root)) {
            foreach ($sub2 in (Get-LbfSortedDirs $sub)) {
                if (-not $script:VersionFolder.IsMatch((Get-LbfBase $sub2))) { continue }
                $p = Join-LbfPath $sub2 'profile.json'
                if (Test-LbfPathExists $p) { $candidates.Add($p) }
            }
        }
        $candidates.Add($target)
        foreach ($path in $candidates) {
            $head = $null
            $err = $null
            try {
                try { $raw = [IO.File]::ReadAllBytes((Get-LbfIoPath $path)) } catch { throw (New-LbfError (Format-LbfOsError 'open' $path $_)) }
                $head = Get-LbfSchemaHead $raw
            } catch { $err = $_ }
            if ($null -ne $err -and -not (Test-LbfSame $path $target)) { continue }
            if ($null -ne $err) { throw (New-LbfError "${path}: $(Get-LbfErrorText $err)") }
            if (Test-LbfSame $head.ID $bronzeId) { throw (New-LbfError "${path}: `$id $($head.ID) is lbf's bronze profile, which only lbf defines") }
            $docs[$head.ID] = $head.Doc
            $parents[$head.ID] = $head.Ref
            $paths[$head.ID] = $path
            if (Test-LbfSame $path $target) { $targetId = $head.ID }
        }
    }
    $chain = New-Object System.Collections.Generic.List[string]
    $id = $targetId
    while ($id.Length -ne 0 -and -not (Test-LbfSame $id $bronzeId) -and -not $chain.Contains($id)) {
        $chain.Insert(0, $id)
        $next = ''
        if (-not $parents.TryGetValue($id, [ref]$next)) { $next = '' }
        $id = $next
    }
    $ids = New-Object System.Collections.Generic.List[string]
    $ids.Add($bronzeId)
    $ids.AddRange($chain)

    $compiler = New-LbfSchemaCompiler $docs
    $rules = New-Object System.Collections.Generic.List[hashtable]
    foreach ($rid in $ids) {
        $p = ''
        if (-not $paths.TryGetValue($rid, [ref]$p)) { $p = '' }
        try {
            $sch = Get-LbfCompiledSchema $compiler $rid (-not (Test-LbfSame $rid $bronzeId))
        } catch {
            throw (New-LbfError "profile $rid (${p}): $(Get-LbfErrorText $_)")
        }
        $title = ''
        $doc = $null
        if ($docs.TryGetValue($rid, [ref]$doc) -and $doc -is [System.Collections.IDictionary]) { $title = Get-LbfStructField $doc 'title' }
        $rules.Add(@{ ID = $rid; Title = $title; Schema = $sch })
    }
    return @{ ID = $targetId; Rules = $rules }
}

# json.Unmarshal into a string field: the last string under a name equal to Name as strings.EqualFold has it.
function Get-LbfStructField {
    param([System.Collections.IDictionary]$Doc, [string]$Name)
    $v = ''
    foreach ($k in $Doc.Keys) {
        if ([string]::Equals($k, $Name, [StringComparison]::OrdinalIgnoreCase) -and $Doc[$k] -is [string]) { $v = $Doc[$k] }
    }
    return $v
}

function Get-LbfSortedDirs {
    param([string]$Dir)
    $out = New-Object System.Collections.Generic.List[string]
    try { $names = [IO.Directory]::GetFileSystemEntries((Get-LbfIoPath $Dir)) } catch { return , $out.ToArray() }
    $bases = New-Object string[] $names.Length
    for ($i = 0; $i -lt $names.Length; $i++) { $bases[$i] = Get-LbfUtf8SortKey ([IO.Path]::GetFileName($names[$i])) }
    $full = New-Object string[] $names.Length
    for ($i = 0; $i -lt $names.Length; $i++) { $full[$i] = Join-LbfPath $Dir ([IO.Path]::GetFileName($names[$i])) }
    [Array]::Sort([Array]$bases, [Array]$full, [Collections.IComparer][StringComparer]::Ordinal)
    return , $full
}

function Test-LbfPathExists {
    param([string]$Path)
    $io = Get-LbfIoPath $Path
    return [IO.File]::Exists($io) -or [IO.Directory]::Exists($io)
}

function Get-LbfProfileIds {
    param([hashtable]$Profile)
    $ids = New-Object System.Collections.Generic.List[string]
    foreach ($r in $Profile.Rules) { $ids.Add($r.ID) }
    return , $ids
}

function Get-LbfProfileTitles {
    param([hashtable]$Profile)
    $t = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    $t[$script:ProcessRunCrate] = 'Process Run Crate'
    foreach ($r in $Profile.Rules) { if ($r.Title.Length -ne 0) { $t[$r.ID] = $r.Title } }
    return , $t
}

# Go's profile.validate: every rule, bronze first; the first that fails names its profile and lists each problem.
function Assert-LbfProfile {
    param([hashtable]$Profile, $View)
    foreach ($r in $Profile.Rules) {
        if (& (Get-LbfFastCheck $r.Schema) $View) { continue }
        $err = Invoke-LbfSchemaValidate $r.Schema $View
        if ($null -eq $err) { continue }
        $leaves = New-Object System.Collections.Generic.List[string]
        Get-LbfErrorLeaves $err $leaves
        $arr = $leaves.ToArray()
        $keys = New-Object string[] $arr.Length
        for ($i = 0; $i -lt $arr.Length; $i++) { $keys[$i] = Get-LbfUtf8SortKey $arr[$i] }
        [Array]::Sort([Array]$keys, [Array]$arr, [Collections.IComparer][StringComparer]::Ordinal)
        throw (New-LbfError ("dataset does not meet profile $($r.ID):`n" + [string]::Join("`n", $arr)))
    }
}

function Get-LbfErrorLeaves {
    param([hashtable]$Err, [System.Collections.Generic.List[string]]$Out)
    if ($null -eq $Err.C -or $Err.C.Count -eq 0) {
        $Out.Add('  /' + [string]::Join('/', $Err.L) + ': ' + (Format-LbfSchemaMessage $Err))
        return
    }
    foreach ($c in $Err.C) { Get-LbfErrorLeaves $c $Out }
}

# ---------- compiling schemas ----------

function New-LbfSchemaCompiler {
    param($Docs)
    $c = @{
        Docs = $Docs
        Resources = (New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
        Anchors = (New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
        Nodes = (New-Object 'System.Collections.Generic.Dictionary[string,hashtable]' ([StringComparer]::Ordinal))
        Indexed = (New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal))
        Checked = (New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal))
        Meta = $null
        IsMeta = $false
    }
    foreach ($k in $Docs.Keys) { Add-LbfSchemaResources $c $k $Docs[$k] }
    return $c
}

$script:SchemaMapKeywords = @('properties', 'patternProperties', 'dependentSchemas', '$defs', 'definitions')
$script:SchemaOneKeywords = @('items', 'contains', 'not', 'if', 'then', 'else', 'additionalProperties', 'propertyNames', 'unevaluatedProperties', 'unevaluatedItems', 'contentSchema', 'additionalItems')
$script:SchemaListKeywords = @('allOf', 'anyOf', 'oneOf', 'prefixItems')

# Indexes a document's resources ($id) and anchors so $ref can find them.
function Add-LbfSchemaResources {
    param([hashtable]$C, [string]$DocUrl, $Doc)
    if (-not $C.Indexed.Add($DocUrl)) { return }
    $todo = New-Object System.Collections.Generic.Stack[object]
    $todo.Push(@($Doc, '', $DocUrl))
    $C.Resources[$DocUrl] = @{ Doc = $DocUrl; Ptr = '' }
    while ($todo.Count -gt 0) {
        $item = $todo.Pop()
        $node = $item[0]; $ptr = $item[1]; $base = $item[2]
        if (-not ($node -is [System.Collections.IDictionary])) { continue }
        if ($ptr.Length -ne 0 -and $node.Contains('$id') -and $node['$id'] -is [string]) {
            $base = Join-LbfUrl $base (Get-LbfUrlNoFragment $node['$id'])
            if (-not $C.Resources.ContainsKey($base)) { $C.Resources[$base] = @{ Doc = $DocUrl; Ptr = $ptr } }
        }
        foreach ($a in @('$anchor', '$dynamicAnchor')) {
            if ($node.Contains($a) -and $node[$a] -is [string]) {
                $key = $base + '#' + $node[$a]
                if (-not $C.Anchors.ContainsKey($key)) { $C.Anchors[$key] = @{ Doc = $DocUrl; Ptr = $ptr } }
            }
        }
        foreach ($k in $script:SchemaMapKeywords) {
            if ($node.Contains($k) -and $node[$k] -is [System.Collections.IDictionary]) {
                foreach ($name in $node[$k].Keys) { $todo.Push(@($node[$k][$name], ($ptr + '/' + (ConvertTo-LbfPointerToken $k) + '/' + (ConvertTo-LbfPointerToken $name)), $base)) }
            }
        }
        foreach ($k in $script:SchemaOneKeywords) {
            if ($node.Contains($k)) { $todo.Push(@($node[$k], ($ptr + '/' + $k), $base)) }
        }
        foreach ($k in $script:SchemaListKeywords) {
            if ($node.Contains($k) -and $node[$k] -is [System.Collections.IList] -and -not ($node[$k] -is [string])) {
                for ($i = 0; $i -lt $node[$k].Count; $i++) { $todo.Push(@($node[$k][$i], ($ptr + '/' + $k + '/' + $i), $base)) }
            }
        }
    }
}

function Get-LbfUrlNoFragment {
    param([string]$Url)
    $i = $Url.IndexOf([char]'#')
    if ($i -ge 0) { return $Url.Substring(0, $i) }
    return $Url
}

# RFC 3986 reference resolution, enough for schema $id and $ref.
function Join-LbfUrl {
    param([string]$Base, [string]$Ref)
    if ($Ref -cmatch '\A[A-Za-z][A-Za-z0-9+.\-]*:') { return $Ref }
    $baseNoFrag = Get-LbfUrlNoFragment $Base
    if ($Ref.Length -eq 0) { return $baseNoFrag }
    if ($Ref.StartsWith('#', [StringComparison]::Ordinal)) { return $baseNoFrag + $Ref }
    $m = [regex]::Match($baseNoFrag, '\A([A-Za-z][A-Za-z0-9+.\-]*:)(//[^/?#]*)?([^?#]*)(\?[^#]*)?')
    $scheme = $m.Groups[1].Value
    $auth = $m.Groups[2].Value
    $path = $m.Groups[3].Value
    if ($Ref.StartsWith('//', [StringComparison]::Ordinal)) { return $scheme + $Ref }
    $frag = ''
    $hash = $Ref.IndexOf([char]'#')
    if ($hash -ge 0) { $frag = $Ref.Substring($hash); $Ref = $Ref.Substring(0, $hash) }
    $query = ''
    $q = $Ref.IndexOf([char]'?')
    if ($q -ge 0) { $query = $Ref.Substring($q); $Ref = $Ref.Substring(0, $q) }
    if ($Ref.Length -eq 0) { $newPath = $path; if ($query.Length -eq 0) { $query = $m.Groups[4].Value } }
    elseif ($Ref.StartsWith('/', [StringComparison]::Ordinal)) { $newPath = $Ref }
    else {
        $dir = ''
        $slash = $path.LastIndexOf([char]'/')
        if ($slash -ge 0) { $dir = $path.Substring(0, $slash + 1) } elseif ($auth.Length -ne 0) { $dir = '/' }
        $newPath = $dir + $Ref
    }
    $segs = New-Object System.Collections.Generic.List[string]
    $parts = $newPath.Split([char]'/')
    for ($i = 0; $i -lt $parts.Length; $i++) {
        $s = $parts[$i]
        if (Test-LbfSame $s '.') { if ($i -eq $parts.Length - 1) { $segs.Add('') }; continue }
        if (Test-LbfSame $s '..') {
            if ($segs.Count -gt 1) { $segs.RemoveAt($segs.Count - 1) }
            if ($i -eq $parts.Length - 1) { $segs.Add('') }
            continue
        }
        $segs.Add($s)
    }
    return $scheme + $auth + [string]::Join('/', $segs.ToArray()) + $query + $frag
}

# Finds the document and pointer a URL names.
function Resolve-LbfSchemaUrl {
    param([hashtable]$C, [string]$Url)
    $frag = ''
    $hash = $Url.IndexOf([char]'#')
    $base = $Url
    if ($hash -ge 0) { $frag = $Url.Substring($hash + 1); $base = $Url.Substring(0, $hash) }
    $frag = [Uri]::UnescapeDataString($frag)
    $res = $null
    if (-not $C.Resources.TryGetValue($base, [ref]$res)) {
        $path = $null
        if (-not ($base -cmatch '\A[A-Za-z][A-Za-z0-9+.\-]*:') -or ($script:OnWindows -and $base -cmatch '\A[A-Za-z]:')) {
            if ($base.Length -eq 0) { $path = Get-LbfAbsPath '.' } else { $path = Get-LbfAbsPath $base }
            $fileUrl = 'file://' + $path.Replace('\', '/')
            if ($script:OnWindows) { $fileUrl = 'file:///' + $path.Replace('\', '/') }
            $base = $fileUrl
            if ($hash -ge 0) { $Url = $fileUrl + '#' + $Url.Substring($hash + 1) } else { $Url = $fileUrl }
        } elseif ($base -cmatch '\A[fF][iI][lL][eE]://') {
            $path = [Uri]::UnescapeDataString($base.Substring(7))
            if ($script:OnWindows) { $path = $path.TrimStart('/').Replace('/', '\') }
        }
        if ($null -ne $path -and -not $C.Resources.TryGetValue($base, [ref]$res)) {
            $io = Get-LbfIoPath $path
            if ([IO.Directory]::Exists($io)) {
                $why = 'is a directory'
                if ($script:OnWindows) { $why = 'Incorrect function.' }
                throw (New-LbfError "failing loading $(Format-LbfQuote $base): read ${path}: $why")
            }
            try { $raw = [IO.File]::ReadAllBytes($io) } catch { throw (New-LbfError "failing loading $(Format-LbfQuote $base): $(Format-LbfOsError 'open' $path $_)") }
            try { Assert-LbfLegacyJson $raw } catch { throw (New-LbfError "failing loading $(Format-LbfQuote $base): $(Get-LbfErrorText $_)") }
            $C.Docs[$base] = ConvertFrom-LbfProfileJson $raw
            Add-LbfSchemaResources $C $base $C.Docs[$base]
            [void]$C.Resources.TryGetValue($base, [ref]$res)
        }
        if ($null -eq $res) { throw (New-LbfError "failing loading $(Format-LbfQuote $base): invalid file url: $base") }
    }
    if ($frag.Length -eq 0 -or $frag.StartsWith('/', [StringComparison]::Ordinal)) {
        return @{ Doc = $res.Doc; Ptr = $res.Ptr + $frag; Url = $Url }
    }
    $a = $null
    if ($C.Anchors.TryGetValue($base + '#' + $frag, [ref]$a)) { return @{ Doc = $a.Doc; Ptr = $a.Ptr; Url = $Url } }
    throw (New-LbfError "anchor in $(Format-LbfQuote $Url) not found in schema $(Format-LbfQuote $base)")
}

function Get-LbfJsonAtPointer {
    param($Doc, [string]$Ptr, [string]$Url)
    $v = $Doc
    if ($Ptr.Length -eq 0) { return , $v }
    foreach ($tok in $Ptr.Substring(1).Split([char]'/')) {
        $t = $tok.Replace('~1', '/').Replace('~0', '~')
        if ($v -is [System.Collections.IDictionary] -and $v.Contains($t)) { $v = $v[$t]; continue }
        $i = 0
        if ($v -is [System.Collections.IList] -and -not ($v -is [string]) -and [int]::TryParse($t, [ref]$i) -and $i -ge 0 -and $i -lt $v.Count) { $v = $v[$i]; continue }
        throw (New-LbfError "json-pointer in $(Format-LbfQuote $Url) not found")
    }
    return , $v
}

# Compiles the schema at a URL, first checking its document against the 2020-12 metaschema.
function Get-LbfCompiledSchema {
    param([hashtable]$C, [string]$Url, [bool]$Check = $true)
    $loc = Resolve-LbfSchemaUrl $C $Url
    if ($Check -and $C.Checked.Add($loc.Doc)) { Assert-LbfMetaSchema $C $loc.Doc }
    return Get-LbfSchemaNode $C $loc.Doc $loc.Ptr
}

$script:SchemaNodeKeys = @('Bool', 'Types', 'HasConst', 'Const', 'Enum', 'EnumTypes', 'Format', 'Ref', 'RefUrl', 'DynRef', 'DynRefUrl', 'Props', 'PatternProps', 'AddlBool', 'Addl',
    'PropNames', 'Required', 'MinProps', 'MaxProps', 'DepRequired', 'DepSchemas', 'PrefixItems', 'Items', 'Contains', 'MinContains', 'MaxContains', 'MinItems', 'MaxItems', 'Unique',
    'MinLen', 'MaxLen', 'Pattern', 'PatternText', 'minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum', 'multipleOf', 'allOf', 'anyOf', 'oneOf', 'not', 'if', 'then', 'else',
    'HasObj', 'HasArr', 'HasStr', 'HasNum', 'HasCond', 'Trivial', 'FastCheck', 'FastId', 'UnevalProps', 'UnevalItems', 'HasUneval',
    'AllPropsEvaluated', 'AllItemsEvaluated', 'NumItemsEvaluated', 'Dependencies')

# Every keyword's slot, so validation reads them under strict mode without checking for each.
function New-LbfSchemaNodeObject {
    param([string]$Loc)
    $n = @{ Loc = $Loc }
    foreach ($k in $script:SchemaNodeKeys) { $n[$k] = $null }
    $n.HasConst = $false
    $n.Unique = $false
    $n.Trivial = $false
    $n.HasUneval = $false
    $n.AllPropsEvaluated = $false
    $n.AllItemsEvaluated = $false
    $n.NumItemsEvaluated = 0
    return $n
}

function Get-LbfSchemaNode {
    param([hashtable]$C, [string]$DocUrl, [string]$Ptr)
    $key = $DocUrl + '#' + $Ptr
    $n = $null
    if ($C.Nodes.TryGetValue($key, [ref]$n)) { return $n }
    $raw = Get-LbfJsonAtPointer $C.Docs[$DocUrl] $Ptr $key
    $n = New-LbfSchemaNodeObject $key
    $C.Nodes[$key] = $n
    if ($raw -is [bool]) { $n.Bool = $raw; return $n }
    if (-not ($raw -is [System.Collections.IDictionary])) { throw (New-LbfError "$(Format-LbfQuote $key) is not valid against metaschema") }
    $base = Get-LbfNodeBase $C $DocUrl $Ptr
    Set-LbfSchemaNode $C $n $raw $DocUrl $Ptr $base
    $n.HasObj = $null -ne $n.Dependencies -or $null -ne $n.Props -or $null -ne $n.PatternProps -or $null -ne $n.Addl -or $null -ne $n.AddlBool -or $null -ne $n.PropNames -or $null -ne $n.Required -or
        $null -ne $n.MinProps -or $null -ne $n.MaxProps -or $null -ne $n.DepRequired -or $null -ne $n.DepSchemas
    $n.HasArr = $null -ne $n.PrefixItems -or $null -ne $n.Items -or $null -ne $n.Contains -or $null -ne $n.MinItems -or $null -ne $n.MaxItems -or $n.Unique
    $n.HasStr = $null -ne $n.MinLen -or $null -ne $n.MaxLen -or $null -ne $n.Pattern
    $n.HasNum = $null -ne $n['minimum'] -or $null -ne $n['maximum'] -or $null -ne $n['exclusiveMinimum'] -or $null -ne $n['exclusiveMaximum'] -or $null -ne $n['multipleOf']
    $n.HasCond = $null -ne $n.DynRef -or $null -ne $n['not'] -or $null -ne $n['allOf'] -or $null -ne $n['anyOf'] -or $null -ne $n['oneOf'] -or $null -ne $n['if']
    $n.HasUneval = $null -ne $n.UnevalProps -or $null -ne $n.UnevalItems
    $n.Trivial = -not ($n.HasObj -or $n.HasArr -or $n.HasStr -or $n.HasNum -or $n.HasCond -or $n.HasConst -or $n.HasUneval -or $null -ne $n.Types -or $null -ne $n.Enum -or $null -ne $n.Format -or $null -ne $n.Ref)
    return $n
}

# The base URI in force at a pointer: the nearest enclosing $id.
function Get-LbfNodeBase {
    param([hashtable]$C, [string]$DocUrl, [string]$Ptr)
    $base = $DocUrl
    $doc = $C.Docs[$DocUrl]
    $cur = $doc
    $toks = @()
    if ($Ptr.Length -eq 0) { return $base }
    $toks = $Ptr.Substring(1).Split([char]'/')
    for ($i = 0; $i -lt $toks.Length; $i++) {
        $t = $toks[$i].Replace('~1', '/').Replace('~0', '~')
        if ($cur -is [System.Collections.IDictionary]) { $cur = $cur[$t] }
        elseif ($cur -is [System.Collections.IList]) { $cur = $cur[[int]$t] }
        else { break }
        if ($cur -is [System.Collections.IDictionary] -and $cur.Contains('$id') -and $cur['$id'] -is [string]) {
            $base = Join-LbfUrl $base (Get-LbfUrlNoFragment $cur['$id'])
        }
    }
    return $base
}

$script:FormatNames = @('date-time', 'date', 'time', 'email', 'uri', 'iri', 'uri-reference', 'iri-reference', 'uuid', 'regex', 'hostname', 'ipv4', 'ipv6', 'json-pointer', 'relative-json-pointer', 'duration', 'period', 'semver', 'uri-template')
$script:UnsupportedKeywords = @('$dynamicRef', '$recursiveRef')

function Get-LbfSchemaNumber {
    param($Value)
    if ($Value -is [hashtable] -and $Value.ContainsKey('LbfNum')) { return $Value.N }
    return $Value
}

function Get-LbfSchemaInt {
    param($Value)
    return [int](Get-LbfSchemaNumber $Value)
}

function Set-LbfSchemaNode {
    param([hashtable]$C, [hashtable]$N, $Raw, [string]$DocUrl, [string]$Ptr, [string]$Base)
    if ($C.IsMeta -and $Raw.Contains('$dynamicRef')) {
        $N.DynRef = Get-LbfSchemaNode $C $script:Draft202012 ''
        $N.DynRefUrl = $script:Draft202012
    }
    foreach ($k in $script:UnsupportedKeywords) {
        if ($C.IsMeta) { break }
        if ($Raw.Contains($k)) { throw (New-LbfError "the keyword $k at $(Format-LbfQuote ($DocUrl + '#' + $Ptr)) is not supported by the PowerShell lbf; use lbf.exe or rewrite the profile without it") }
    }
    if (-not $C.IsMeta -and $Raw.Contains('$schema') -and $Raw['$schema'] -is [string] -and -not (Test-LbfSame (Get-LbfUrlNoFragment $Raw['$schema']) $script:Draft202012)) {
        throw (New-LbfError "`$schema $(Format-LbfQuote $Raw['$schema']) is not supported by the PowerShell lbf, which reads only $($script:Draft202012)")
    }
    $sub = {
        param([string]$rel)
        return Get-LbfSchemaNode $C $DocUrl ($Ptr + $rel)
    }
    if ($Raw.Contains('type')) {
        $t = $Raw['type']
        $set = @($t)
        $order = @('null', 'boolean', 'number', 'integer', 'string', 'array', 'object')
        $N.Types = [string[]]@($order | Where-Object { Test-LbfIn $_ $set })
    }
    if ($Raw.Contains('const')) { $N.HasConst = $true; $N.Const = $Raw['const'] }
    if ($Raw.Contains('enum')) {
        $N.Enum = @($Raw['enum'])
        $types = New-Object 'System.Collections.Generic.HashSet[string]'
        foreach ($e in $N.Enum) { [void]$types.Add((Get-LbfJsonTypeName $e)) }
        $N.EnumTypes = $types
    }
    if ($Raw.Contains('format') -and $Raw['format'] -is [string]) {
        $f = $Raw['format']
        if ($C.IsMeta) {
            if (Test-LbfIn $f @('regex', 'uri', 'uri-reference')) { $N.Format = $f }
        } elseif (Test-LbfIn $f $script:FormatNames) {
            if (Test-LbfIn $f @('duration', 'period', 'semver', 'uri-template', 'relative-json-pointer')) {
                throw (New-LbfError "format $(Format-LbfQuote $f) at $(Format-LbfQuote ($DocUrl + '#' + $Ptr)) is not supported by the PowerShell lbf; use lbf.exe or rewrite the profile without it")
            }
            $N.Format = $f
        }
    }
    if ($Raw.Contains('$ref')) {
        $url = Join-LbfUrl $Base $Raw['$ref']
        $loc = Resolve-LbfSchemaUrl $C $url
        if (-not (Test-LbfSame $loc.Doc $DocUrl) -and $C.Checked.Add($loc.Doc) -and $null -ne $C.Meta) { Assert-LbfMetaSchema $C $loc.Doc }
        $N.Ref = Get-LbfSchemaNode $C $loc.Doc $loc.Ptr
        $N.RefUrl = $url
    }
    if ($Raw.Contains('properties')) {
        $props = New-Object 'System.Collections.Generic.Dictionary[string,hashtable]' ([StringComparer]::Ordinal)
        foreach ($name in $Raw['properties'].Keys) { $props[$name] = & $sub ('/properties/' + (ConvertTo-LbfPointerToken $name)) }
        $N.Props = $props
    }
    if ($Raw.Contains('patternProperties')) {
        $pp = New-Object System.Collections.Generic.List[object]
        foreach ($re in $Raw['patternProperties'].Keys) { $pp.Add(@((ConvertTo-LbfNetRegex $re), (& $sub ('/patternProperties/' + (ConvertTo-LbfPointerToken $re))))) }
        $N.PatternProps = $pp
    }
    if ($Raw.Contains('additionalProperties')) {
        $a = $Raw['additionalProperties']
        if ($a -is [bool]) { $N.AddlBool = $a } else { $N.Addl = & $sub '/additionalProperties' }
    }
    if ($Raw.Contains('propertyNames')) { $N.PropNames = & $sub '/propertyNames' }
    if ($Raw.Contains('required')) { $N.Required = [string[]]@($Raw['required']) }
    if ($Raw.Contains('minProperties')) { $N.MinProps = Get-LbfSchemaInt $Raw['minProperties'] }
    if ($Raw.Contains('maxProperties')) { $N.MaxProps = Get-LbfSchemaInt $Raw['maxProperties'] }
    if ($Raw.Contains('dependencies') -and $Raw['dependencies'] -is [System.Collections.IDictionary]) {
        $deps = New-Object System.Collections.Generic.List[object]
        foreach ($k in $Raw['dependencies'].Keys) {
            $d = $Raw['dependencies'][$k]
            if ($d -is [System.Collections.IList] -and -not ($d -is [string])) { $deps.Add(@($k, [string[]]@($d), $null)) }
            else { $deps.Add(@($k, $null, (& $sub ('/dependencies/' + (ConvertTo-LbfPointerToken $k))))) }
        }
        $N.Dependencies = $deps
    }
    if ($Raw.Contains('dependentRequired')) {
        $dr = New-Object System.Collections.Generic.List[object]
        foreach ($k in $Raw['dependentRequired'].Keys) { $dr.Add(@($k, [string[]]@($Raw['dependentRequired'][$k]))) }
        $N.DepRequired = $dr
    }
    if ($Raw.Contains('dependentSchemas')) {
        $ds = New-Object System.Collections.Generic.List[object]
        foreach ($k in $Raw['dependentSchemas'].Keys) { $ds.Add(@($k, (& $sub ('/dependentSchemas/' + (ConvertTo-LbfPointerToken $k))))) }
        $N.DepSchemas = $ds
    }
    if ($Raw.Contains('prefixItems')) {
        $pi = New-Object System.Collections.Generic.List[hashtable]
        for ($i = 0; $i -lt @($Raw['prefixItems']).Count; $i++) { $pi.Add((& $sub "/prefixItems/$i")) }
        $N.PrefixItems = $pi
    }
    if ($Raw.Contains('items')) { $N.Items = & $sub '/items' }
    if ($Raw.Contains('contains')) {
        $N.Contains = & $sub '/contains'
        if ($Raw.Contains('minContains')) { $N.MinContains = Get-LbfSchemaInt $Raw['minContains'] }
        if ($Raw.Contains('maxContains')) { $N.MaxContains = Get-LbfSchemaInt $Raw['maxContains'] }
    }
    if ($Raw.Contains('minItems')) { $N.MinItems = Get-LbfSchemaInt $Raw['minItems'] }
    if ($Raw.Contains('maxItems')) { $N.MaxItems = Get-LbfSchemaInt $Raw['maxItems'] }
    if ($Raw.Contains('uniqueItems') -and $Raw['uniqueItems'] -is [bool] -and $Raw['uniqueItems']) { $N.Unique = $true }
    if ($Raw.Contains('minLength')) { $N.MinLen = Get-LbfSchemaInt $Raw['minLength'] }
    if ($Raw.Contains('maxLength')) { $N.MaxLen = Get-LbfSchemaInt $Raw['maxLength'] }
    if ($Raw.Contains('pattern')) { $N.Pattern = ConvertTo-LbfNetRegex $Raw['pattern']; $N.PatternText = $Raw['pattern'] }
    foreach ($k in @('minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum', 'multipleOf')) {
        if ($Raw.Contains($k)) { $N[$k] = Get-LbfSchemaNumber $Raw[$k] }
    }
    foreach ($k in @('allOf', 'anyOf', 'oneOf')) {
        if ($Raw.Contains($k)) {
            $l = New-Object System.Collections.Generic.List[hashtable]
            for ($i = 0; $i -lt @($Raw[$k]).Count; $i++) { $l.Add((& $sub "/$k/$i")) }
            $N[$k] = $l
        }
    }
    foreach ($k in @('not', 'if', 'then', 'else')) {
        if ($Raw.Contains($k)) { $N[$k] = & $sub "/$k" }
    }
    if ($Raw.Contains('unevaluatedProperties')) { $N.UnevalProps = & $sub '/unevaluatedProperties' }
    if ($Raw.Contains('unevaluatedItems')) { $N.UnevalItems = & $sub '/unevaluatedItems' }
    $N.AllPropsEvaluated = $null -ne $N.Addl -or $null -ne $N.AddlBool
    $N.AllItemsEvaluated = $null -ne $N.Items
    $N.NumItemsEvaluated = 0
    if ($null -ne $N.PrefixItems) { $N.NumItemsEvaluated = $N.PrefixItems.Count }
}

# Checks a profile document against the 2020-12 metaschema, as the Go compiler does before compiling it.
function Assert-LbfMetaSchema {
    param([hashtable]$C, [string]$DocUrl)
    if ($null -eq $C.Meta) {
        $metaDocs = ConvertFrom-LbfProfileJson ($script:Utf8NoBom.GetBytes($script:MetaSchemaDocs.Trim()))
        $mc = New-LbfSchemaCompiler (New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
        foreach ($k in $metaDocs.Keys) { $mc.Docs[$k] = $metaDocs[$k]; Add-LbfSchemaResources $mc $k $metaDocs[$k] }
        $mc.IsMeta = $true
        $C.Meta = Get-LbfSchemaNode $mc $script:Draft202012 ''
    }
    $doc = $C.Docs[$DocUrl]
    $err = Invoke-LbfSchemaValidate $C.Meta $doc
    if ($null -eq $err) { return }
    $sb = New-Object System.Text.StringBuilder
    Write-LbfErrorTree $err $sb 0
    throw (New-LbfError "$(Format-LbfQuote ($DocUrl + '#')) is not valid against metaschema: $($sb.ToString())")
}

# ---------- RE2 patterns in .NET ----------

$script:RegexCache = New-Object 'System.Collections.Generic.Dictionary[string,System.Text.RegularExpressions.Regex]' ([StringComparer]::Ordinal)

# A Go (RE2) pattern as a .NET regex with the same meaning for the patterns profiles use; throws Go's error for one RE2 refuses.
function ConvertTo-LbfNetRegex {
    param([string]$Pattern)
    $cached = $null
    if ($script:RegexCache.TryGetValue($Pattern, [ref]$cached)) { return $cached }
    $err = Get-LbfRe2Error $Pattern
    if ($null -ne $err) { throw (New-LbfError "error parsing regexp: $err") }
    $sb = New-Object System.Text.StringBuilder
    $inClass = $false
    $multiline = $Pattern -cmatch '\(\?[a-zA-Z]*m'
    for ($i = 0; $i -lt $Pattern.Length; $i++) {
        $c = $Pattern[$i]
        if ($c -eq '\' -and $i + 1 -lt $Pattern.Length) {
            $e = $Pattern[$i + 1]
            $i++
            $rep = $null
            switch -CaseSensitive ($e) {
                'd' { if ($inClass) { $rep = '0-9' } else { $rep = '[0-9]' } }
                'D' { if ($inClass) { $rep = '\x00-/:-\uFFFF' } else { $rep = '[^0-9]' } }
                'w' { if ($inClass) { $rep = '0-9A-Za-z_' } else { $rep = '[0-9A-Za-z_]' } }
                'W' { if ($inClass) { $rep = '\x00-/:-@\[-^`{-\uFFFF' } else { $rep = '[^0-9A-Za-z_]' } }
                's' { if ($inClass) { $rep = '\t\n\f\r ' } else { $rep = '[\t\n\f\r ]' } }
                'S' { if ($inClass) { $rep = '\x00-\x08\x0B\x0E-\x1F!-\uFFFF' } else { $rep = '[^\t\n\f\r ]' } }
                'z' { $rep = '\z' }
            }
            if ($null -eq $rep) { $rep = '\' + $e }
            [void]$sb.Append($rep)
            continue
        }
        if ($inClass) {
            if ($c -eq ']') { $inClass = $false }
            elseif ($c -eq '[' -and $i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq ':') {
                $end = $Pattern.IndexOf(':]', $i + 2, [StringComparison]::Ordinal)
                if ($end -gt 0) {
                    $name = $Pattern.Substring($i + 2, $end - $i - 2)
                    $posix = Get-LbfPosixClass $name
                    if ($null -ne $posix) { [void]$sb.Append($posix); $i = $end + 1; continue }
                }
            }
            [void]$sb.Append($c)
            continue
        }
        if ($c -eq '[') {
            $inClass = $true
            [void]$sb.Append($c)
            if ($i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq '^') { [void]$sb.Append('^'); $i++ }
            if ($i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq ']') { [void]$sb.Append('\]'); $i++ }
            continue
        }
        if ($c -eq '$' -and -not $multiline) { [void]$sb.Append('\z'); continue }
        if ($c -eq '(' -and $i + 2 -lt $Pattern.Length -and $Pattern[$i + 1] -eq '?' -and $Pattern[$i + 2] -eq 'P') { [void]$sb.Append('(?'); $i += 2; continue }
        [void]$sb.Append($c)
    }
    $re = New-Object System.Text.RegularExpressions.Regex ($sb.ToString(), [System.Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $script:RegexCache[$Pattern] = $re
    return $re
}

$script:PosixRanges = @{}
foreach ($pair in ('alnum=30-39,41-5A,61-7A;alpha=41-5A,61-7A;ascii=0-7F;blank=9-9,20-20;cntrl=0-1F,7F-7F;digit=30-39;graph=21-7E;lower=61-7A;' +
        'print=20-7E;punct=21-2F,3A-40,5B-60,7B-7E;space=9-D,20-20;upper=41-5A;word=30-39,41-5A,5F-5F,61-7A;xdigit=30-39,41-46,61-66').Split([char]';')) {
    $kv = $pair.Split([char]'=')
    $script:PosixRanges[$kv[0]] = @($kv[1].Split([char]',') | ForEach-Object { , @($_.Split([char]'-') | ForEach-Object { [Convert]::ToInt32($_, 16) }) })
}

# RE2's [:name:] or [:^name:] as the inside of a .NET class, or $null for a name RE2 does not know.
function Get-LbfPosixClass {
    param([string]$Name)
    $negate = $Name.StartsWith('^', [StringComparison]::Ordinal)
    $key = $Name
    if ($negate) { $key = $Name.Substring(1) }
    if (-not (Test-LbfIn $key @($script:PosixRanges.Keys))) { return $null }
    $ranges = $script:PosixRanges[$key]
    if ($negate) {
        $out = New-Object System.Collections.Generic.List[object]
        $next = 0
        foreach ($r in $ranges) {
            if ($r[0] -gt $next) { $out.Add(@($next, ($r[0] - 1))) }
            $next = $r[1] + 1
        }
        $out.Add(@($next, 0xFFFF))
        $ranges = $out.ToArray()
    }
    $sb = New-Object System.Text.StringBuilder
    foreach ($r in $ranges) { [void]$sb.Append(('\u{0:X4}-\u{1:X4}' -f $r[0], $r[1])) }
    return $sb.ToString()
}

# Go's regexp/syntax errors for the mistakes people make, or $null.
function Get-LbfRe2Error {
    param([string]$Pattern)
    $depth = 0
    $opens = New-Object System.Collections.Generic.Stack[int]
    $canRepeat = $false
    $lastWasRepeat = $false
    for ($i = 0; $i -lt $Pattern.Length; $i++) {
        $c = $Pattern[$i]
        switch ($c) {
            '\' {
                if ($i + 1 -ge $Pattern.Length) { return 'trailing backslash at end of expression: ``' }
                $e = $Pattern[$i + 1]
                if ($e -match '[1-9]') { return "invalid escape sequence: ``\$e``" }
                if ([char]::IsLetterOrDigit($e) -and 'afnrtvdDsSwWbBAzpPQExC0'.IndexOf($e) -lt 0) { return "invalid escape sequence: ``\$e``" }
                if ($e -eq 'p' -or $e -eq 'P') {
                    if ($i + 2 -lt $Pattern.Length -and $Pattern[$i + 2] -eq '{') {
                        $end = $Pattern.IndexOf([char]'}', $i + 3)
                        if ($end -lt 0) { return "invalid character class range: ``$($Pattern.Substring($i))``" }
                        $i = $end
                    } else { $i += 2 }
                } elseif ($e -eq 'x') {
                    $i++
                    if ($i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq '{') { $end = $Pattern.IndexOf([char]'}', $i); if ($end -lt 0) { return "invalid escape sequence: ``$($Pattern.Substring($i - 1))``" }; $i = $end }
                    else { $i += 2 }
                } else { $i++ }
                $canRepeat = $true; $lastWasRepeat = $false
            }
            '[' {
                $start = $i
                $i++
                if ($i -lt $Pattern.Length -and $Pattern[$i] -eq '^') { $i++ }
                if ($i -lt $Pattern.Length -and $Pattern[$i] -eq ']') { $i++ }
                $closed = $false
                while ($i -lt $Pattern.Length) {
                    if ($Pattern[$i] -eq '\') { $i += 2; continue }
                    if ($Pattern[$i] -eq '[' -and $i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq ':') {
                        $end = $Pattern.IndexOf(':]', $i + 2, [StringComparison]::Ordinal)
                        if ($end -gt 0) {
                            if ($null -eq (Get-LbfPosixClass $Pattern.Substring($i + 2, $end - $i - 2))) { return "invalid character class range: ``$($Pattern.Substring($i, $end + 2 - $i))``" }
                            $i = $end + 2; continue
                        }
                    }
                    if ($Pattern[$i] -eq ']') { $closed = $true; break }
                    $i++
                }
                if (-not $closed) { return "missing closing ]: ``$($Pattern.Substring($start))``" }
                $canRepeat = $true; $lastWasRepeat = $false
            }
            '(' {
                if ($i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq '?') {
                    $rest = $Pattern.Substring($i)
                    if ($rest -cmatch '\A\(\?(=|!|<=|<!|>|\()') { return "invalid or unsupported Perl syntax: ``$($Matches[0])``" }
                    if ($rest -cmatch '\A\(\?P?<[A-Za-z0-9_]+>') { $i += $Matches[0].Length - 1 }
                    elseif ($rest -cmatch '\A\(\?[imsU-]*:') { $i += $Matches[0].Length - 1 }
                    elseif ($rest -cmatch '\A\(\?[imsU-]*\)') { $i += $Matches[0].Length - 1; $canRepeat = $false; continue }
                    else { return "invalid or unsupported Perl syntax: ``$($rest.Substring(0, [Math]::Min(3, $rest.Length)))``" }
                }
                $opens.Push($i)
                $canRepeat = $false; $lastWasRepeat = $false
            }
            ')' {
                if ($opens.Count -eq 0) { return "unexpected ): ``$Pattern``" }
                [void]$opens.Pop()
                $canRepeat = $true; $lastWasRepeat = $false
            }
            '|' { $canRepeat = $false; $lastWasRepeat = $false }
            { $_ -eq '*' -or $_ -eq '+' -or $_ -eq '?' } {
                $op = [string]$c
                if ($i + 1 -lt $Pattern.Length -and $Pattern[$i + 1] -eq '?') { $op += '?'; $i++ }
                if ($lastWasRepeat) { return "invalid nested repetition operator: ``$($Pattern.Substring($i - $op.Length - 0, $op.Length + 1).TrimStart())``" }
                if (-not $canRepeat) { return "missing argument to repetition operator: ``$op``" }
                $lastWasRepeat = $true
            }
            '{' {
                $m = [regex]::Match($Pattern.Substring($i), '\A\{([0-9]+)(,([0-9]*))?\}')
                if ($m.Success) {
                    if (-not $canRepeat) { return "missing argument to repetition operator: ``$($m.Value)``" }
                    if ($lastWasRepeat) { return "invalid nested repetition operator: ``$($m.Value)``" }
                    if ([int]$m.Groups[1].Value -gt 1000 -or ($m.Groups[3].Value -ne '' -and ([int]$m.Groups[3].Value -gt 1000 -or [int]$m.Groups[3].Value -lt [int]$m.Groups[1].Value))) { return "invalid repeat count: ``$($m.Value)``" }
                    $i += $m.Length - 1
                    $lastWasRepeat = $true
                } else { $canRepeat = $true; $lastWasRepeat = $false }
            }
            default { $canRepeat = $true; $lastWasRepeat = $false }
        }
    }
    if ($opens.Count -gt 0) { return "missing closing ): ``$Pattern``" }
    return $null
}

# ---------- validating ----------

$script:BoolErr = @{ K = 'bool'; L = [string[]]@(); C = $null }

function New-LbfSchemaErr {
    param([string]$Kind, [System.Collections.Generic.List[string]]$Loc, [hashtable]$Fields)
    $e = @{ K = $Kind; L = $Loc.ToArray(); C = $null }
    if ($null -ne $Fields) { foreach ($k in $Fields.Keys) { $e[$k] = $Fields[$k] } }
    return $e
}

# Go's Schema.Validate: $null, or an error tree under a Schema root.
function Invoke-LbfSchemaValidate {
    param([hashtable]$Schema, $Value)
    $loc = New-Object System.Collections.Generic.List[string]
    $refs = New-Object System.Collections.Generic.List[hashtable]
    $err = Test-LbfSchemaValue $Schema $Value $loc $false $refs
    if ($null -eq $err) { return $null }
    $causes = New-Object System.Collections.Generic.List[hashtable]
    if ($err.K -eq 'group') { $causes.AddRange($err.C) } else { $causes.Add($err) }
    return @{ K = 'schema'; L = [string[]]@(); C = $causes; Loc = $Schema.Loc }
}

function Get-LbfNumValue {
    param($V)
    if ($V -is [hashtable]) { return $V.N }
    return $V
}

# Go's equals: JSON values compared by value, numbers by magnitude.
function Test-LbfJsonEqual {
    param($A, $B)
    $ta = Get-LbfJsonTypeName $A
    $tb = Get-LbfJsonTypeName $B
    if ($ta -ne $tb) { return $false }
    switch ($ta) {
        'null' { return $true }
        'boolean' { return $A -eq $B }
        'string' { return [string]::Equals($A, $B, [StringComparison]::Ordinal) }
        'number' { return (Compare-LbfSchemaNumber (Get-LbfNumValue $A) (Get-LbfNumValue $B)) -eq 0 }
        'array' {
            if ($A.Count -ne $B.Count) { return $false }
            for ($i = 0; $i -lt $A.Count; $i++) { if (-not (Test-LbfJsonEqual $A[$i] $B[$i])) { return $false } }
            return $true
        }
        'object' {
            if ($A.Count -ne $B.Count) { return $false }
            foreach ($k in $A.Keys) {
                if (-not (Test-LbfDictHas $B $k)) { return $false }
                if (-not (Test-LbfJsonEqual $A[$k] $B[$k])) { return $false }
            }
            return $true
        }
    }
    return $false
}

function Test-LbfDictHas {
    param($D, [string]$Key)
    if ($D -is [System.Collections.Specialized.OrderedDictionary]) { return $D.Contains($Key) }
    return $D.ContainsKey($Key)
}

function Compare-LbfSchemaNumber {
    param($A, $B)
    if ($A -is [double] -or $B -is [double]) { return ([double]$A).CompareTo([double]$B) }
    return ([decimal]$A).CompareTo([decimal]$B)
}

function Test-LbfIntegral {
    param($N)
    if ($N -is [double]) { return -not [double]::IsInfinity($N) -and [Math]::Floor($N) -eq $N }
    return [decimal]::Truncate([decimal]$N) -eq [decimal]$N
}

function Get-LbfRuneCount {
    param([string]$S)
    $n = $S.Length
    if (-not $script:HighChars.IsMatch($S)) { return $n }
    for ($i = 0; $i -lt $S.Length - 1; $i++) {
        if ([char]::IsHighSurrogate($S[$i]) -and [char]::IsLowSurrogate($S[$i + 1])) { $n--; $i++ }
    }
    return $n
}

# One schema against one value, as Go's validator.validate does it; $Bool when only valid or not matters.
function Test-LbfSchemaValue {
    param([hashtable]$S, $V, [System.Collections.Generic.List[string]]$Loc, [bool]$Bool, [System.Collections.Generic.List[hashtable]]$Refs, $U)
    if ($null -ne $S.Bool) {
        if ($S.Bool) { return $null }
        if ($Bool) { return $script:BoolErr }
        return New-LbfSchemaErr 'false' $Loc $null
    }
    if ($S.Trivial) { return $null }
    if ($null -eq $U -and $S.HasUneval) { $U = New-LbfUneval $V $S $false }
    if ($null -eq $V) { $t = 'null' }
    elseif ($V -is [string]) { $t = 'string' }
    elseif ($V -is [long] -or $V -is [int]) { $t = 'number' }
    elseif ($V -is [System.Collections.IDictionary]) { if ($V -is [hashtable] -and $V.ContainsKey('LbfNum')) { $t = 'number' } else { $t = 'object' } }
    elseif ($V -is [bool]) { $t = 'boolean' }
    elseif ($V -is [double] -or $V -is [decimal]) { $t = 'number' }
    elseif ($V -is [System.Collections.IList]) { $t = 'array' }
    else { $t = 'invalid' }
    if ($null -ne $S.Types) {
        if (-not ($S.Types -ccontains $t)) {
            if (-not ($t -eq 'number' -and ($S.Types -ccontains 'integer') -and (Test-LbfIntegral (Get-LbfNumValue $V)))) {
                if ($Bool) { return $script:BoolErr }
                return New-LbfSchemaErr 'type' $Loc @{ Got = $t; Want = $S.Types }
            }
        }
    }
    if ($S.HasConst -and -not (Test-LbfJsonEqual $V $S.Const)) {
        if ($Bool) { return $script:BoolErr }
        return New-LbfSchemaErr 'const' $Loc @{ Want = $S.Const }
    }
    if ($null -ne $S.Enum) {
        $matched = $false
        if ($S.EnumTypes.Contains($t)) {
            foreach ($e in $S.Enum) { if (Test-LbfJsonEqual $V $e) { $matched = $true; break } }
        }
        if (-not $matched) {
            if ($Bool) { return $script:BoolErr }
            return New-LbfSchemaErr 'enum' $Loc @{ Want = $S.Enum }
        }
    }
    if ($null -ne $S.Format -and $t -eq 'string') {
        $ferr = Test-LbfFormat $S.Format $V
        if ($null -ne $ferr) {
            if ($Bool) { return $script:BoolErr }
            return New-LbfSchemaErr 'format' $Loc @{ Got = $V; Want = $S.Format; Err = $ferr }
        }
    }
    $errors = $null
    if ($null -ne $S.Ref) {
        if ($null -eq $Refs) { $Refs = New-Object System.Collections.Generic.List[hashtable] }
        $e = Test-LbfSchemaRef $S.Ref $S.RefUrl '$ref' $V $Loc $Bool $Refs $U
        if ($null -ne $e) { $errors = New-Object System.Collections.Generic.List[hashtable]; $errors.Add($e) }
    }
    if ($t -eq 'object') {
        if ($S.HasObj) { $errors = Test-LbfSchemaObject $S $V $Loc $Bool $Refs $errors $U }
    } elseif ($t -eq 'string') {
        if ($S.HasStr) {
            if ($null -ne $S.MinLen -or $null -ne $S.MaxLen) {
                $n = $V.Length
                if ($n -gt 0 -and $script:HighChars.IsMatch($V)) { $n = Get-LbfRuneCount $V }
                if ($null -ne $S.MinLen -and $n -lt $S.MinLen) { $errors = Add-LbfSchemaErr $errors $Bool 'minLength' $Loc @{ Got = $n; Want = $S.MinLen } }
                if ($null -ne $S.MaxLen -and $n -gt $S.MaxLen) { $errors = Add-LbfSchemaErr $errors $Bool 'maxLength' $Loc @{ Got = $n; Want = $S.MaxLen } }
            }
            if ($null -ne $S.Pattern -and -not $S.Pattern.IsMatch($V)) { $errors = Add-LbfSchemaErr $errors $Bool 'pattern' $Loc @{ Got = $V; Want = $S.PatternText } }
        }
    } elseif ($t -eq 'number') {
        if ($S.HasNum) {
            $n = Get-LbfNumValue $V
            if ($null -ne $S['minimum'] -and (Compare-LbfSchemaNumber $n $S['minimum']) -lt 0) { $errors = Add-LbfSchemaErr $errors $Bool 'minimum' $Loc @{ Got = $n; Want = $S['minimum'] } }
            if ($null -ne $S['maximum'] -and (Compare-LbfSchemaNumber $n $S['maximum']) -gt 0) { $errors = Add-LbfSchemaErr $errors $Bool 'maximum' $Loc @{ Got = $n; Want = $S['maximum'] } }
            if ($null -ne $S['exclusiveMinimum'] -and (Compare-LbfSchemaNumber $n $S['exclusiveMinimum']) -le 0) { $errors = Add-LbfSchemaErr $errors $Bool 'exclusiveMinimum' $Loc @{ Got = $n; Want = $S['exclusiveMinimum'] } }
            if ($null -ne $S['exclusiveMaximum'] -and (Compare-LbfSchemaNumber $n $S['exclusiveMaximum']) -ge 0) { $errors = Add-LbfSchemaErr $errors $Bool 'exclusiveMaximum' $Loc @{ Got = $n; Want = $S['exclusiveMaximum'] } }
            if ($null -ne $S['multipleOf']) {
                $m = $S['multipleOf']
                if ($n -is [double] -or $m -is [double]) { $q = [double]$n / [double]$m } else { $q = [decimal]$n / [decimal]$m }
                if (-not (Test-LbfIntegral $q)) { $errors = Add-LbfSchemaErr $errors $Bool 'multipleOf' $Loc @{ Got = $n; Want = $m } }
            }
        }
    } elseif ($t -eq 'array') {
        if ($S.HasArr) { $errors = Test-LbfSchemaArray $S $V $Loc $Bool $Refs $errors $U }
    }
    if ($null -eq $errors -or $errors.Count -eq 0 -or -not $Bool) {
        if ($S.HasCond) { $errors = Test-LbfSchemaCond $S $V $Loc $Bool $Refs $errors $U }
        if ($S.HasUneval -and $null -ne $U) { $errors = Test-LbfSchemaUneval $S $V $Loc $Bool $errors $U }
    }
    if ($null -eq $errors -or $errors.Count -eq 0) { return $null }
    if ($Bool) { return $script:BoolErr }
    if ($errors.Count -eq 1) { return $errors[0] }
    $g = New-LbfSchemaErr 'group' $Loc $null
    $g.C = $errors
    return $g
}

function Add-LbfSchemaErr {
    param($Errors, [bool]$Bool, [string]$Kind, $Loc, [hashtable]$Fields)
    if ($null -eq $Errors) { $Errors = New-Object System.Collections.Generic.List[hashtable] }
    if ($Bool) { $Errors.Add($script:BoolErr) } else { $Errors.Add((New-LbfSchemaErr $Kind $Loc $Fields)) }
    return , $Errors
}

function Add-LbfSchemaCause {
    param($Errors, [hashtable]$Err)
    if ($null -eq $Errors) { $Errors = New-Object System.Collections.Generic.List[hashtable] }
    $Errors.Add($Err)
    return , $Errors
}

function Test-LbfSchemaRef {
    param([hashtable]$Target, [string]$Url, [string]$Keyword, $V, $Loc, [bool]$Bool, $Refs, $U)
    if ($null -eq $Refs) { $Refs = New-Object System.Collections.Generic.List[hashtable] }
    foreach ($r in $Refs) {
        if ([object]::ReferenceEquals($r, $Target)) {
            if ($Bool) { return $script:BoolErr }
            return New-LbfSchemaErr 'refcycle' $Loc @{ Url = $Target.Loc }
        }
    }
    $Refs.Add($Target)
    try { $e = Test-LbfSchemaSelf $Target $V $Loc $Bool $Refs $U } finally { $Refs.RemoveAt($Refs.Count - 1) }
    if ($null -eq $e) { return $null }
    if ($Bool) { return $script:BoolErr }
    $r = New-LbfSchemaErr 'ref' $Loc @{ Keyword = $Keyword; Url = $Url }
    if ($e.K -eq 'group') { $r.C = $e.C } else { $r.C = New-Object System.Collections.Generic.List[hashtable]; $r.C.Add($e) }
    return $r
}

# Which properties or items of a value no keyword has evaluated yet, as Go's unevalFrom starts them; $null when none is tracked.
function New-LbfUneval {
    param($V, [hashtable]$S, [bool]$CallerNeeds)
    if ($null -ne $S.Bool) { $all = $false; $num = 0; $wantsProps = $CallerNeeds; $wantsItems = $CallerNeeds }
    else {
        $wantsProps = -not $S.AllPropsEvaluated -and ($CallerNeeds -or $null -ne $S.UnevalProps)
        $wantsItems = -not $S.AllItemsEvaluated -and ($CallerNeeds -or $null -ne $S.UnevalItems)
        $num = $S.NumItemsEvaluated
    }
    if ($V -is [System.Collections.IDictionary] -and -not ($V -is [hashtable] -and $V.ContainsKey('LbfNum'))) {
        if (-not $wantsProps) { return $null }
        $set = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
        foreach ($k in $V.Keys) { [void]$set.Add([string]$k) }
        return @{ Props = $set; Items = $null }
    }
    if ($V -is [System.Collections.IList] -and -not ($V -is [string])) {
        if (-not $wantsItems -or $num -ge $V.Count) { return $null }
        $set = New-Object 'System.Collections.Generic.HashSet[int]'
        for ($i = $num; $i -lt $V.Count; $i++) { [void]$set.Add($i) }
        return @{ Props = $null; Items = $set }
    }
    return $null
}

function Test-LbfUnevalEmpty {
    param($U)
    return $null -eq $U -or (($null -eq $U.Props -or $U.Props.Count -eq 0) -and ($null -eq $U.Items -or $U.Items.Count -eq 0))
}

# Go's validateSelf: a subschema against the same value, whose evaluations count for the caller's when it passes.
function Test-LbfSchemaSelf {
    param([hashtable]$S, $V, $Loc, [bool]$Bool, $Refs, $U)
    if (Test-LbfUnevalEmpty $U) { return Test-LbfSchemaValue $S $V $Loc $Bool $Refs $null }
    $cu = New-LbfUneval $V $S $true
    $e = Test-LbfSchemaValue $S $V $Loc $Bool $Refs $cu
    if ($null -eq $e) {
        if ($null -ne $U.Props) {
            if ($null -eq $cu -or $null -eq $cu.Props) { $U.Props.Clear() }
            else { foreach ($k in @($U.Props)) { if (-not $cu.Props.Contains($k)) { [void]$U.Props.Remove($k) } } }
        }
        if ($null -ne $U.Items) {
            if ($null -eq $cu -or $null -eq $cu.Items) { $U.Items.Clear() }
            else { foreach ($k in @($U.Items)) { if (-not $cu.Items.Contains($k)) { [void]$U.Items.Remove($k) } } }
        }
    }
    return $e
}

# Go's unevalValidate.
function Test-LbfSchemaUneval {
    param([hashtable]$S, $V, $Loc, [bool]$Bool, $Errors, $U)
    if ($null -ne $S.UnevalProps -and $null -ne $U.Props) {
        $names = New-Object string[] $U.Props.Count
        $U.Props.CopyTo($names)
        $names = Get-LbfSortedUtf8 $names
        foreach ($name in $names) {
            $e = Test-LbfSchemaChild $S.UnevalProps $V[$name] $name $Loc $Bool
            if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
        }
        $U.Props = $null
    }
    if ($null -ne $S.UnevalItems -and $null -ne $U.Items) {
        $idx = New-Object int[] $U.Items.Count
        $U.Items.CopyTo($idx)
        [Array]::Sort($idx)
        foreach ($i in $idx) {
            $e = Test-LbfSchemaChild $S.UnevalItems $V[$i] ([string]$i) $Loc $Bool
            if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
        }
        $U.Items = $null
    }
    return , $Errors
}

function Test-LbfSchemaChild {
    param([hashtable]$S, $V, [string]$Token, $Loc, [bool]$Bool)
    $Loc.Add($Token)
    $e = Test-LbfSchemaValue $S $V $Loc $Bool $null
    $Loc.RemoveAt($Loc.Count - 1)
    return $e
}

function Test-LbfSchemaObject {
    param([hashtable]$S, $Obj, $Loc, [bool]$Bool, $Refs, $Errors, $U)
    $up = $null
    if ($null -ne $U) { $up = $U.Props }
    if ($null -ne $S.MinProps -and $Obj.Count -lt $S.MinProps) { $Errors = Add-LbfSchemaErr $Errors $Bool 'minProperties' $Loc @{ Got = $Obj.Count; Want = $S.MinProps } }
    if ($null -ne $S.MaxProps -and $Obj.Count -gt $S.MaxProps) { $Errors = Add-LbfSchemaErr $Errors $Bool 'maxProperties' $Loc @{ Got = $Obj.Count; Want = $S.MaxProps } }
    $ordered = $Obj -is [System.Collections.Specialized.OrderedDictionary]
    if ($null -ne $S.Required) {
        $missing = $null
        foreach ($r in $S.Required) {
            if ($ordered) { $has = $Obj.Contains($r) } else { $has = $Obj.ContainsKey($r) }
            if (-not $has) {
                if ($null -eq $missing) { $missing = New-Object System.Collections.Generic.List[string] }
                $missing.Add($r)
            }
        }
        if ($null -ne $missing) { $Errors = Add-LbfSchemaErr $Errors $Bool 'required' $Loc @{ Missing = $missing.ToArray() } }
    }
    if ($Bool -and $null -ne $Errors -and $Errors.Count -gt 0) { return , $Errors }
    if ($null -ne $S.Dependencies) {
        foreach ($d in $S.Dependencies) {
            if ($ordered) { $has = $Obj.Contains($d[0]) } else { $has = $Obj.ContainsKey($d[0]) }
            if (-not $has) { continue }
            if ($null -ne $d[1]) {
                $missing = New-Object System.Collections.Generic.List[string]
                foreach ($r in $d[1]) { if (-not (Test-LbfDictHas $Obj $r)) { $missing.Add($r) } }
                if ($missing.Count -gt 0) { $Errors = Add-LbfSchemaErr $Errors $Bool 'dependentRequired' $Loc @{ Prop = $d[0]; Missing = $missing.ToArray() } }
            } else {
                $e = Test-LbfSchemaSelf $d[2] $Obj $Loc $Bool $Refs $U
                if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
            }
        }
    }
    $additional = $null
    if ($null -ne $S.Props -or $null -ne $S.PatternProps -or $null -ne $S.Addl -or $null -ne $S.AddlBool) {
        foreach ($name in @($Obj.Keys)) {
            if ($Bool -and $null -ne $Errors -and $Errors.Count -gt 0) { return , $Errors }
            $evaluated = $false
            $sub = $null
            if ($null -ne $S.Props -and $S.Props.TryGetValue($name, [ref]$sub)) {
                $evaluated = $true
                $Loc.Add($name)
                $e = Test-LbfSchemaValue $sub $Obj[$name] $Loc $Bool $null
                $Loc.RemoveAt($Loc.Count - 1)
                if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
            }
            if ($null -ne $S.PatternProps) {
                foreach ($pp in $S.PatternProps) {
                    if ($pp[0].IsMatch($name)) {
                        $evaluated = $true
                        $e = Test-LbfSchemaChild $pp[1] $Obj[$name] $name $Loc $Bool
                        if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
                    }
                }
            }
            if (-not $evaluated) {
                if ($null -ne $S.AddlBool) {
                    $evaluated = $true
                    if (-not $S.AddlBool) {
                        if ($null -eq $additional) { $additional = New-Object System.Collections.Generic.List[string] }
                        $additional.Add($name)
                    }
                } elseif ($null -ne $S.Addl) {
                    $evaluated = $true
                    $e = Test-LbfSchemaChild $S.Addl $Obj[$name] $name $Loc $Bool
                    if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
                }
            }
            if ($evaluated -and $null -ne $up) { [void]$up.Remove($name) }
        }
    }
    if ($null -ne $additional) {
        $arr = Get-LbfSortedUtf8 $additional.ToArray()
        $Errors = Add-LbfSchemaErr $Errors $Bool 'additionalProperties' $Loc @{ Properties = $arr }
    }
    if ($null -ne $S.PropNames) {
        foreach ($name in @($Obj.Keys)) {
            $inner = Invoke-LbfSchemaValidate $S.PropNames $name
            if ($null -ne $inner) {
                if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr; continue }
                $inner.K = 'propertyNames'
                $inner.L = $Loc.ToArray()
                $inner.Property = $name
                $Errors = Add-LbfSchemaCause $Errors $inner
            }
        }
    }
    if ($null -ne $S.DepSchemas) {
        foreach ($d in $S.DepSchemas) {
            if (Test-LbfDictHas $Obj $d[0]) {
                $e = Test-LbfSchemaSelf $d[1] $Obj $Loc $Bool $Refs $U
                if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
            }
        }
    }
    if ($null -ne $S.DepRequired) {
        foreach ($d in $S.DepRequired) {
            if (Test-LbfDictHas $Obj $d[0]) {
                $missing = New-Object System.Collections.Generic.List[string]
                foreach ($r in $d[1]) { if (-not (Test-LbfDictHas $Obj $r)) { $missing.Add($r) } }
                if ($missing.Count -gt 0) { $Errors = Add-LbfSchemaErr $Errors $Bool 'dependentRequired' $Loc @{ Prop = $d[0]; Missing = $missing.ToArray() } }
            }
        }
    }
    return , $Errors
}

function Get-LbfJsonCanonical {
    param($V)
    switch (Get-LbfJsonTypeName $V) {
        'number' { return 'n:' + ([decimal](Get-LbfNumValue $V)).ToString($script:Invariant) }
        'string' { return 's:' + $V }
        'boolean' { return 'b:' + $V }
        'null' { return 'null' }
        'array' { $p = New-Object System.Collections.Generic.List[string]; foreach ($x in $V) { $p.Add((Get-LbfJsonCanonical $x)) }; return 'a:[' + [string]::Join(',', $p.ToArray()) + ']' }
        'object' {
            [string[]]$keys = @($V.Keys | ForEach-Object { [string]$_ })
            [Array]::Sort($keys, [StringComparer]::Ordinal)
            $p = New-Object System.Collections.Generic.List[string]
            foreach ($k in $keys) { $p.Add((ConvertTo-LbfJsonString $k) + ':' + (Get-LbfJsonCanonical $V[$k])) }
            return 'o:{' + [string]::Join(',', $p.ToArray()) + '}'
        }
    }
    return '?'
}

function Test-LbfSchemaArray {
    param([hashtable]$S, $Arr, $Loc, [bool]$Bool, $Refs, $Errors, $U)
    $count = $Arr.Count
    if ($null -ne $S.MinItems -and $count -lt $S.MinItems) { $Errors = Add-LbfSchemaErr $Errors $Bool 'minItems' $Loc @{ Got = $count; Want = $S.MinItems } }
    if ($null -ne $S.MaxItems -and $count -gt $S.MaxItems) { $Errors = Add-LbfSchemaErr $Errors $Bool 'maxItems' $Loc @{ Got = $count; Want = $S.MaxItems } }
    if ($S.Unique -and $count -gt 1) {
        $seen = New-Object 'System.Collections.Generic.Dictionary[string,int]' ([StringComparer]::Ordinal)
        for ($i = 0; $i -lt $count; $i++) {
            $key = Get-LbfJsonCanonical $Arr[$i]
            $j = 0
            if ($seen.TryGetValue($key, [ref]$j)) { $Errors = Add-LbfSchemaErr $Errors $Bool 'uniqueItems' $Loc @{ I = $j; J = $i }; break }
            $seen[$key] = $i
        }
    }
    $evaluated = 0
    if ($null -ne $S.PrefixItems) {
        $evaluated = [Math]::Min($S.PrefixItems.Count, $count)
        for ($i = 0; $i -lt $evaluated; $i++) {
            $e = Test-LbfSchemaChild $S.PrefixItems[$i] $Arr[$i] ([string]$i) $Loc $Bool
            if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
        }
    }
    if ($null -ne $S.Items) {
        $items = $S.Items
        for ($i = $evaluated; $i -lt $count; $i++) {
            $Loc.Add([string]$i)
            $e = Test-LbfSchemaValue $items $Arr[$i] $Loc $Bool $null
            $Loc.RemoveAt($Loc.Count - 1)
            if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
        }
    }
    if ($null -ne $S.Contains) {
        $cerrs = New-Object System.Collections.Generic.List[hashtable]
        $matched = New-Object System.Collections.Generic.List[int]
        $contains = $S.Contains
        for ($i = 0; $i -lt $count; $i++) {
            $Loc.Add([string]$i)
            $e = Test-LbfSchemaValue $contains $Arr[$i] $Loc $Bool $null
            $Loc.RemoveAt($Loc.Count - 1)
            if ($null -ne $e) { $cerrs.Add($e) } else { $matched.Add($i); if ($null -ne $U -and $null -ne $U.Items) { [void]$U.Items.Remove($i) } }
        }
        if ($null -ne $S.MinContains) {
            if ($matched.Count -lt $S.MinContains) {
                if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr }
                else { $x = New-LbfSchemaErr 'minContains' $Loc @{ Got = $matched.ToArray(); Want = $S.MinContains }; $x.C = $cerrs; $Errors = Add-LbfSchemaCause $Errors $x }
            }
        } elseif ($matched.Count -eq 0) {
            if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr }
            else { $x = New-LbfSchemaErr 'contains' $Loc $null; $x.C = $cerrs; $Errors = Add-LbfSchemaCause $Errors $x }
        }
        if ($null -ne $S.MaxContains -and $matched.Count -gt $S.MaxContains) { $Errors = Add-LbfSchemaErr $Errors $Bool 'maxContains' $Loc @{ Got = $matched.ToArray(); Want = $S.MaxContains } }
    }
    return , $Errors
}

function Test-LbfSchemaCond {
    param([hashtable]$S, $V, $Loc, [bool]$Bool, $Refs, $Errors, $U)
    if ($null -ne $S.DynRef) {
        if ($null -eq $Refs) { $Refs = New-Object System.Collections.Generic.List[hashtable] }
        $e = Test-LbfSchemaRef $S.DynRef $S.DynRefUrl '$dynamicRef' $V $Loc $Bool $Refs $U
        if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
    }
    if ($null -ne $S['not']) {
        if ($null -eq (Test-LbfSchemaSelf $S['not'] $V $Loc $true $Refs $U)) { $Errors = Add-LbfSchemaErr $Errors $Bool 'not' $Loc $null }
    }
    if ($null -ne $S['allOf']) {
        $errs = New-Object System.Collections.Generic.List[hashtable]
        foreach ($sub in $S['allOf']) {
            $e = Test-LbfSchemaSelf $sub $V $Loc $Bool $Refs $U
            if ($null -ne $e) { $errs.Add($e); if ($Bool) { break } }
        }
        if ($errs.Count -gt 0) {
            if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr }
            else { $x = New-LbfSchemaErr 'allOf' $Loc $null; $x.C = $errs; $Errors = Add-LbfSchemaCause $Errors $x }
        }
    }
    if ($null -ne $S['anyOf']) {
        $errs = New-Object System.Collections.Generic.List[hashtable]
        $matched = $false
        foreach ($sub in $S['anyOf']) {
            $e = Test-LbfSchemaSelf $sub $V $Loc $Bool $Refs $U
            if ($null -ne $e) { $errs.Add($e) } else { $matched = $true; if (Test-LbfUnevalEmpty $U) { break } }
        }
        if (-not $matched) {
            if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr }
            else { $x = New-LbfSchemaErr 'anyOf' $Loc $null; $x.C = $errs; $Errors = Add-LbfSchemaCause $Errors $x }
        }
    }
    if ($null -ne $S['oneOf']) {
        $errs = New-Object System.Collections.Generic.List[hashtable]
        $first = -1
        for ($i = 0; $i -lt $S['oneOf'].Count; $i++) {
            $e = Test-LbfSchemaSelf $S['oneOf'][$i] $V $Loc ($Bool -or $first -ne -1) $Refs $U
            if ($null -ne $e) { if ($first -eq -1) { $errs.Add($e) } }
            elseif ($first -eq -1) { $first = $i }
            else { $Errors = Add-LbfSchemaErr $Errors $Bool 'oneOf' $Loc @{ Matched = @($first, $i) }; break }
        }
        if ($first -eq -1) {
            if ($Bool) { $Errors = Add-LbfSchemaCause $Errors $script:BoolErr }
            else { $x = New-LbfSchemaErr 'oneOf' $Loc @{ Matched = $null }; $x.C = $errs; $Errors = Add-LbfSchemaCause $Errors $x }
        }
    }
    if ($null -ne $S['if']) {
        if ($null -eq (Test-LbfSchemaSelf $S['if'] $V $Loc $true $Refs $U)) {
            if ($null -ne $S['then']) { $e = Test-LbfSchemaSelf $S['then'] $V $Loc $Bool $Refs $U; if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e } }
        } elseif ($null -ne $S['else']) {
            $e = Test-LbfSchemaSelf $S['else'] $V $Loc $Bool $Refs $U
            if ($null -ne $e) { $Errors = Add-LbfSchemaCause $Errors $e }
        }
    }
    return , $Errors
}

# ---------- a fast yes or no ----------

# A dataset may list 100,000 files, so each schema is first checked by code generated for it; only a failure is validated again for messages.
$script:FastNodes = New-Object System.Collections.Generic.List[hashtable]
$script:FastLoc = New-Object System.Collections.Generic.List[string]

function Get-LbfFastCheck {
    param([hashtable]$N)
    if ($null -ne $N['FastCheck']) { return $N['FastCheck'] }
    $ctx = @{ Next = 0 }
    $body = Get-LbfFastBody $N '$x' $ctx 0
    $sb = [scriptblock]::Create("param(`$x)`n" + $body + "return `$true`n")
    $N['FastCheck'] = $sb
    return $sb
}

$script:FastStrings = New-Object System.Collections.Generic.List[string]

# A property name as generated code reads it, since PowerShell also ends a quoted literal at curly quotes.
function Get-LbfFastString {
    param([string]$Text)
    $script:FastStrings.Add($Text)
    return "`$script:FastStrings[$($script:FastStrings.Count - 1)]"
}

function Get-LbfFastId {
    param([hashtable]$N)
    if ($null -eq $N['FastId']) { $N['FastId'] = $script:FastNodes.Count; $script:FastNodes.Add($N) }
    return $N['FastId']
}

function Get-LbfFastBody {
    param([hashtable]$N, [string]$X, [hashtable]$Ctx, [int]$Depth)
    if ($null -ne $N.Bool) { if ($N.Bool) { return '' } else { return "return `$false`n" } }
    if ($N.Trivial) { return '' }
    $id = Get-LbfFastId $N
    $node = "`$script:FastNodes[$id]"
    $generic = "if (`$null -ne (Test-LbfSchemaValue $node $X `$script:FastLoc `$true `$null)) { return `$false }`n"
    $unsupported = $N.HasUneval -or $null -ne $N.Dependencies -or $null -ne $N.PatternProps -or $null -ne $N.PropNames -or $null -ne $N.DepRequired -or $null -ne $N.DepSchemas -or $null -ne $N.PrefixItems -or
        $N.Unique -or $null -ne $N['anyOf'] -or $null -ne $N['oneOf'] -or $null -ne $N['not'] -or $null -ne $N['if'] -or $null -ne $N.DynRef
    if ($unsupported -or $Depth -gt 24) { return $generic }
    $k = $Ctx.Next++
    $t = "`$t$k"
    $c = New-Object System.Text.StringBuilder
    [void]$c.Append("if (`$null -eq $X) { $t = 'null' } elseif ($X -is [string]) { $t = 'string' } elseif ($X -is [long] -or $X -is [int]) { $t = 'number' } ")
    [void]$c.Append("elseif ($X -is [System.Collections.IDictionary]) { if ($X -is [hashtable] -and $X.ContainsKey('LbfNum')) { $t = 'number' } else { $t = 'object' } } ")
    [void]$c.Append("elseif ($X -is [bool]) { $t = 'boolean' } elseif ($X -is [double] -or $X -is [decimal]) { $t = 'number' } elseif ($X -is [System.Collections.IList]) { $t = 'array' } else { $t = 'invalid' }`n")
    if ($null -ne $N.Types) {
        [void]$c.Append("if (-not ($node.Types -ccontains $t) -and -not ($t -eq 'number' -and ($node.Types -ccontains 'integer') -and (Test-LbfIntegral (Get-LbfNumValue $X)))) { return `$false }`n")
    }
    if ($N.HasConst) { [void]$c.Append("if (-not (Test-LbfJsonEqual $X $node.Const)) { return `$false }`n") }
    if ($null -ne $N.Enum) {
        [void]$c.Append("`$m$k = `$false; if ($node.EnumTypes.Contains($t)) { foreach (`$e$k in $node.Enum) { if (Test-LbfJsonEqual $X `$e$k) { `$m$k = `$true; break } } }; if (-not `$m$k) { return `$false }`n")
    }
    if ($null -ne $N.Format) { [void]$c.Append("if ($t -eq 'string' -and `$null -ne (Test-LbfFormat $node.Format $X)) { return `$false }`n") }
    if ($null -ne $N.Ref) { [void]$c.Append((Get-LbfFastBody $N.Ref $X $Ctx ($Depth + 1))) }
    if ($N.HasObj) {
        [void]$c.Append("if ($t -eq 'object') {`n`$o$k = $X -is [System.Collections.Specialized.OrderedDictionary]`n")
        if ($null -ne $N.MinProps) { [void]$c.Append("if ($X.Count -lt $($N.MinProps)) { return `$false }`n") }
        if ($null -ne $N.MaxProps) { [void]$c.Append("if ($X.Count -gt $($N.MaxProps)) { return `$false }`n") }
        if ($null -ne $N.Required) {
            foreach ($r in $N.Required) {
                $q = Get-LbfFastString $r
                [void]$c.Append("if (`$o$k) { `$h$k = $X.Contains($q) } else { `$h$k = $X.ContainsKey($q) }; if (-not `$h$k) { return `$false }`n")
            }
        }
        if ($null -ne $N.Props) {
            foreach ($name in $N.Props.Keys) {
                $q = Get-LbfFastString $name
                $j = $Ctx.Next++
                $sub = Get-LbfFastBody $N.Props[$name] "`$v$j" $Ctx ($Depth + 1)
                if ($sub.Length -eq 0) { continue }
                [void]$c.Append("if (`$o$k) { `$h$k = $X.Contains($q) } else { `$h$k = $X.ContainsKey($q) }`nif (`$h$k) {`n`$v$j = $X[$q]`n$sub}`n")
            }
        }
        if ($null -ne $N.AddlBool -or $null -ne $N.Addl) {
            $j = $Ctx.Next++
            $props = "`$null"
            if ($null -ne $N.Props) { $props = "$node.Props" }
            [void]$c.Append("foreach (`$n$j in @($X.Keys)) { if ($props -eq `$null -or -not $props.ContainsKey(`$n$j)) {`n")
            if ($null -ne $N.AddlBool) {
                if (-not $N.AddlBool) { [void]$c.Append("return `$false`n") }
            } else {
                [void]$c.Append("`$v$j = $X[`$n$j]`n")
                [void]$c.Append((Get-LbfFastBody $N.Addl "`$v$j" $Ctx ($Depth + 1)))
            }
            [void]$c.Append("} }`n")
        }
        [void]$c.Append("}`n")
    }
    if ($N.HasArr) {
        [void]$c.Append("if ($t -eq 'array') {`n")
        if ($null -ne $N.MinItems) { [void]$c.Append("if ($X.Count -lt $($N.MinItems)) { return `$false }`n") }
        if ($null -ne $N.MaxItems) { [void]$c.Append("if ($X.Count -gt $($N.MaxItems)) { return `$false }`n") }
        if ($null -ne $N.Items) {
            $j = $Ctx.Next++
            $sub = Get-LbfFastBody $N.Items "`$v$j" $Ctx ($Depth + 1)
            if ($sub.Length -ne 0) { [void]$c.Append("foreach (`$v$j in $X) {`n$sub}`n") }
        }
        if ($null -ne $N.Contains) {
            $j = $Ctx.Next++
            $cid = Get-LbfFastId $N.Contains
            [void]$c.Append("`$c$j = 0; `$check$j = Get-LbfFastCheck `$script:FastNodes[$cid]; foreach (`$v$j in $X) { if (& `$check$j `$v$j) { `$c$j++ } }`n")
            if ($null -ne $N.MinContains) { [void]$c.Append("if (`$c$j -lt $($N.MinContains)) { return `$false }`n") }
            else { [void]$c.Append("if (`$c$j -eq 0) { return `$false }`n") }
            if ($null -ne $N.MaxContains) { [void]$c.Append("if (`$c$j -gt $($N.MaxContains)) { return `$false }`n") }
        }
        [void]$c.Append("}`n")
    }
    if ($N.HasStr) {
        [void]$c.Append("if ($t -eq 'string') {`n")
        if ($null -ne $N.MinLen -or $null -ne $N.MaxLen) {
            [void]$c.Append("`$l$k = $X.Length; if (`$l$k -gt 0 -and `$script:HighChars.IsMatch($X)) { `$l$k = Get-LbfRuneCount $X }`n")
            if ($null -ne $N.MinLen) { [void]$c.Append("if (`$l$k -lt $($N.MinLen)) { return `$false }`n") }
            if ($null -ne $N.MaxLen) { [void]$c.Append("if (`$l$k -gt $($N.MaxLen)) { return `$false }`n") }
        }
        if ($null -ne $N.Pattern) { [void]$c.Append("if (-not $node.Pattern.IsMatch($X)) { return `$false }`n") }
        [void]$c.Append("}`n")
    }
    if ($N.HasNum) {
        [void]$c.Append("if ($t -eq 'number') {`n`$n$k = Get-LbfNumValue $X`n")
        if ($null -ne $N['minimum'] -and $N['minimum'] -is [decimal] -and $null -eq $N['maximum'] -and $null -eq $N['exclusiveMinimum'] -and $null -eq $N['exclusiveMaximum'] -and $null -eq $N['multipleOf']) {
            [void]$c.Append("if (`$n$k -is [long] -or `$n$k -is [int] -or `$n$k -is [decimal]) { if ([decimal]`$n$k -lt $node['minimum']) { return `$false } } elseif ((Compare-LbfSchemaNumber `$n$k $node['minimum']) -lt 0) { return `$false }`n")
        } elseif ($null -ne $N['minimum']) { [void]$c.Append("if ((Compare-LbfSchemaNumber `$n$k $node['minimum']) -lt 0) { return `$false }`n") }
        if ($null -ne $N['maximum']) { [void]$c.Append("if ((Compare-LbfSchemaNumber `$n$k $node['maximum']) -gt 0) { return `$false }`n") }
        if ($null -ne $N['exclusiveMinimum']) { [void]$c.Append("if ((Compare-LbfSchemaNumber `$n$k $node['exclusiveMinimum']) -le 0) { return `$false }`n") }
        if ($null -ne $N['exclusiveMaximum']) { [void]$c.Append("if ((Compare-LbfSchemaNumber `$n$k $node['exclusiveMaximum']) -ge 0) { return `$false }`n") }
        if ($null -ne $N['multipleOf']) { [void]$c.Append($generic) }
        [void]$c.Append("}`n")
    }
    if ($null -ne $N['allOf']) {
        foreach ($sub in $N['allOf']) { [void]$c.Append((Get-LbfFastBody $sub $X $Ctx ($Depth + 1))) }
    }
    return $c.ToString()
}

# ---------- messages, in x/text/message's English ----------

# kind.quote: %q, with " left alone and ' escaped, in single quotes.
function Format-LbfSchemaQuote {
    param([string]$Text)
    $q = Format-LbfQuote $Text
    $q = $q.Substring(1, $q.Length - 2).Replace('\"', '"').Replace("'", "\'")
    return "'" + $q + "'"
}

function Format-LbfSchemaDisplay {
    param($V)
    switch (Get-LbfJsonTypeName $V) {
        'string' { return Format-LbfSchemaQuote $V }
        'null' { return '<nil>' }
        'boolean' { if ($V) { return 'true' } else { return 'false' } }
        'number' { if ($V -is [hashtable]) { return $V.T }; return Format-LbfSchemaFloat $V }
        default { return 'value' }
    }
}

function Format-LbfPrinterInt {
    param([long]$N)
    return $N.ToString('#,0', $script:Invariant)
}

$script:Superscripts = @([char]0x2070, [char]0x00B9, [char]0x00B2, [char]0x00B3, [char]0x2074, [char]0x2075, [char]0x2076, [char]0x2077, [char]0x2078, [char]0x2079)

# A float as message.Printer prints %v: Go's shortest %g, grouped, with scientific notation as x10 and superscripts.
function Format-LbfPrinterFloat {
    param($N)
    $d = [double]$N
    if ($d -eq 0) { return '0' }
    $neg = $d -lt 0
    $r = [Math]::Abs($d).ToString('R', $script:Invariant)
    $m = [regex]::Match($r, '\A([0-9]+)(?:\.([0-9]+))?(?:E([+-][0-9]+))?\z')
    $digits = ($m.Groups[1].Value + $m.Groups[2].Value).TrimStart('0')
    $lead = $m.Groups[1].Value.TrimStart('0')
    $exp10 = 0
    if ($m.Groups[3].Success) { $exp10 = [int]$m.Groups[3].Value }
    if ($lead -ne '') { $dp = $lead.Length + $exp10 } else {
        $frac = $m.Groups[2].Value
        $dp = - ($frac.Length - $frac.TrimStart('0').Length) + $exp10
    }
    $digits = $digits.TrimEnd('0')
    if ($digits -eq '') { $digits = '0' }
    $exp = $dp - 1
    $sign = ''
    if ($neg) { $sign = '-' }
    if ($exp -lt -4 -or $exp -ge 6) {
        $mant = $digits.Substring(0, 1)
        if ($digits.Length -gt 1) { $mant += '.' + $digits.Substring(1) }
        $es = ''
        if ($exp -lt 0) { $es = [string][char]0x207B }
        $ed = ([Math]::Abs($exp)).ToString('00', $script:Invariant)
        foreach ($ch in $ed.ToCharArray()) { $es += $script:Superscripts[[int][string]$ch] }
        return $sign + $mant + [char]0x00D7 + '10' + $es
    }
    if ($dp -le 0) { return $sign + '0.' + ('0' * (-$dp)) + $digits }
    if ($digits.Length -le $dp) { $int = $digits + ('0' * ($dp - $digits.Length)); $frac = '' }
    else { $int = $digits.Substring(0, $dp); $frac = $digits.Substring($dp) }
    $grouped = ([decimal]::Parse($int, $script:Invariant)).ToString('#,0', $script:Invariant)
    if ($frac -ne '') { return $sign + $grouped + '.' + $frac }
    return $sign + $grouped
}

# fmt's %v of a float64, without a printer.
function Format-LbfSchemaFloat {
    param($N)
    $d = [double]$N
    if ($N -is [long] -or $N -is [int]) { return [string]$N }
    $r = $d.ToString('R', $script:Invariant)
    if ($r -match 'E') {
        $m = [regex]::Match($r, '\A(-?[0-9.]+)E([+-])([0-9]+)\z')
        return $m.Groups[1].Value + 'e' + $m.Groups[2].Value + $m.Groups[3].Value.PadLeft(2, '0')
    }
    return $r
}

function Format-LbfSchemaMessage {
    param([hashtable]$E)
    switch ($E.K) {
        'schema' { return "jsonschema validation failed with $(Format-LbfSchemaQuote $E.Loc)" }
        'group' { return 'validation failed' }
        'ref' { return 'validation failed' }
        'refcycle' { return "both $($E.Url) and $($E.Url) resolve to $(Format-LbfQuote $E.Url) causing reference cycle" }
        'false' { return 'false schema' }
        'not' { return "'not' failed" }
        'allOf' { return "'allOf' failed" }
        'anyOf' { return "'anyOf' failed" }
        'oneOf' {
            if ($null -eq $E.Matched) { return "'oneOf' failed, none matched" }
            return "'oneOf' failed, subschemas $(Format-LbfPrinterInt $E.Matched[0]), $(Format-LbfPrinterInt $E.Matched[1]) matched"
        }
        'type' { return "got $($E.Got), want $([string]::Join(' or ', $E.Want))" }
        'const' {
            $t = Get-LbfJsonTypeName $E.Want
            if ($t -eq 'array' -or $t -eq 'object') { return "'const' failed" }
            return "value must be $(Format-LbfSchemaDisplay $E.Want)"
        }
        'enum' {
            foreach ($w in $E.Want) { $t = Get-LbfJsonTypeName $w; if ($t -eq 'array' -or $t -eq 'object') { return "'enum' failed" } }
            if ($E.Want.Count -eq 1) { return "value must be $(Format-LbfSchemaDisplay $E.Want[0])" }
            $parts = New-Object System.Collections.Generic.List[string]
            foreach ($w in $E.Want) { $parts.Add((Format-LbfSchemaDisplay $w)) }
            return "value must be one of $([string]::Join(', ', $parts.ToArray()))"
        }
        'format' { return "$(Format-LbfSchemaDisplay $E.Got) is not valid $($E.Want): $($E.Err)" }
        { $_ -eq 'minProperties' -or $_ -eq 'maxProperties' -or $_ -eq 'minItems' -or $_ -eq 'maxItems' -or $_ -eq 'minLength' -or $_ -eq 'maxLength' } {
            return "$($E.K): got $(Format-LbfPrinterInt $E.Got), want $(Format-LbfPrinterInt $E.Want)"
        }
        'required' {
            if ($E.Missing.Length -eq 1) { return "missing property $(Format-LbfSchemaQuote $E.Missing[0])" }
            return "missing properties $(Join-LbfSchemaQuoted $E.Missing)"
        }
        'dependentRequired' { return "properties $(Join-LbfSchemaQuoted $E.Missing) required, if $(Format-LbfSchemaQuote $E.Prop) exists" }
        'additionalProperties' { return "additional properties $(Join-LbfSchemaQuoted $E.Properties) not allowed" }
        'propertyNames' { return "invalid propertyName $(Format-LbfSchemaQuote $E.Property)" }
        'uniqueItems' { return "items at $(Format-LbfPrinterInt $E.I) and $(Format-LbfPrinterInt $E.J) are equal" }
        'contains' { return 'no items match contains schema' }
        'minContains' {
            if ($E.Got.Length -eq 0) { return "min $(Format-LbfPrinterInt $E.Want) items required to match contains schema, but none matched" }
            return "min $(Format-LbfPrinterInt $E.Want) items required to match contains schema, but matched $(Format-LbfPrinterInt $E.Got.Length) items at $([string]::Join(' ', $E.Got))"
        }
        'maxContains' { return "max $(Format-LbfPrinterInt $E.Want) items required to match contains schema, but matched $(Format-LbfPrinterInt $E.Got.Length) items at $([string]::Join(' ', $E.Got))" }
        'pattern' { return "$(Format-LbfSchemaQuote $E.Got) does not match pattern $(Format-LbfSchemaQuote $E.Want)" }
        { $_ -eq 'minimum' -or $_ -eq 'maximum' -or $_ -eq 'exclusiveMinimum' -or $_ -eq 'exclusiveMaximum' -or $_ -eq 'multipleOf' } {
            return "$($E.K): got $(Format-LbfPrinterFloat $E.Got), want $(Format-LbfPrinterFloat $E.Want)"
        }
    }
    return 'validation failed'
}

function Join-LbfSchemaQuoted {
    param([string[]]$Items)
    $parts = New-Object System.Collections.Generic.List[string]
    foreach ($i in $Items) { $parts.Add((Format-LbfSchemaQuote $i)) }
    return [string]::Join(', ', $parts.ToArray())
}

# ValidationError.Error(): the tree, a Reference with one cause folded into it.
function Write-LbfErrorTree {
    param([hashtable]$E, [System.Text.StringBuilder]$Sb, [int]$Indent)
    $skip = $E.K -eq 'ref' -and $null -ne $E.C -and $E.C.Count -eq 1
    if (-not $skip) {
        if ($Indent -gt 0) {
            [void]$Sb.Append("`n")
            for ($i = 0; $i -lt $Indent - 1; $i++) { [void]$Sb.Append('  ') }
            [void]$Sb.Append('- ')
        }
        $Indent++
        if ($E.K -eq 'schema') { [void]$Sb.Append((Format-LbfSchemaMessage $E)) }
        else {
            $ptr = ''
            foreach ($t in $E.L) { $ptr += '/' + (ConvertTo-LbfPointerToken $t) }
            [void]$Sb.Append("at $(Format-LbfSchemaQuote $ptr): $(Format-LbfSchemaMessage $E)")
        }
    }
    if ($null -ne $E.C) { foreach ($c in $E.C) { Write-LbfErrorTree $c $Sb $Indent } }
}

# ---------- formats ----------

# A format's error text, or $null when the string is valid.
function Test-LbfFormat {
    param([string]$Name, [string]$S)
    switch -CaseSensitive ($Name) {
        'date-time' {
            if ($S.Length -lt 20) { return 'less than 20 characters long' }
            if ($S[10] -cne 't' -and $S[10] -cne 'T') { return '11th character must be t or T' }
            $e = Test-LbfGoDate $S.Substring(0, 10)
            if ($null -ne $e) { return "invalid date element: $e" }
            $e = Test-LbfFormat 'time' $S.Substring(11)
            if ($null -ne $e) { return "invalid time element: $e" }
            return $null
        }
        'date' { return Test-LbfGoDate $S }
        'time' { return Test-LbfGoTime $S }
        'email' { return Test-LbfEmail $S }
        'uuid' {
            $groups = $S.Split([char]'-')
            $sizes = @(8, 4, 4, 4, 12)
            if ($groups.Length -ne 5) { return 'must have 5 elements' }
            for ($i = 0; $i -lt 5; $i++) {
                if ($script:Utf8NoBom.GetByteCount($groups[$i]) -ne $sizes[$i]) { return "element $($i + 1) must be $($sizes[$i]) characters long" }
                foreach ($r in (Get-LbfRunes $groups[$i])) {
                    $ok = ($r -ge 0x30 -and $r -le 0x39) -or ($r -ge 0x61 -and $r -le 0x66) -or ($r -ge 0x41 -and $r -le 0x46)
                    if (-not $ok) { return "non-hex character $(Format-LbfQuoteRune ($script:Utf8NoBom.GetBytes((ConvertFrom-LbfRune $r))) 0)" }
                }
            }
            return $null
        }
        { $_ -eq 'uri' -or $_ -eq 'iri' } {
            if (-not (Test-LbfUrlParses $S)) { return "parse $(Format-LbfQuote $S): invalid URI" }
            if (-not ($S -cmatch '\A[A-Za-z][A-Za-z0-9+.\-]*:')) { return 'relative url' }
            return $null
        }
        { $_ -eq 'uri-reference' -or $_ -eq 'iri-reference' } {
            if ($S.Contains('\')) { return 'contains \' }
            if (-not (Test-LbfUrlParses $S)) { return "parse $(Format-LbfQuote $S): invalid URI" }
            return $null
        }
        'regex' {
            $e = Get-LbfRe2Error $S
            if ($null -ne $e) { return "error parsing regexp: $e" }
            return $null
        }
        'hostname' { return Test-LbfHostname $S }
        'ipv4' { return Test-LbfIPv4 $S }
        'ipv6' {
            $a = $null
            if (-not $S.Contains(':') -or -not [System.Net.IPAddress]::TryParse($S, [ref]$a) -or $S.Contains('%')) { return "ParseAddr($(Format-LbfQuote $S)): invalid IPv6 address" }
            return $null
        }
        'json-pointer' {
            if ($S.Length -eq 0) { return $null }
            if (-not $S.StartsWith('/', [StringComparison]::Ordinal)) { return 'not starting with /' }
            if ($S -match '~(?![01])') { return '~ must be followed by 0 or 1' }
            return $null
        }
    }
    return $null
}

function Test-LbfUrlParses {
    param([string]$S)
    if ($S -cmatch '[\x00-\x1F\x7F ]') { return $false }
    if ($S -cmatch '%(?![0-9A-Fa-f]{2})') { return $false }
    return $true
}

# Go's time.Parse("2006-01-02", s) error, or $null.
function Test-LbfGoDate {
    param([string]$S)
    $q = Format-LbfGoTimeQuote $S
    $bad = { param($rest, $elem) return "parsing time $q as `"2006-01-02`": cannot parse $(Format-LbfGoTimeQuote $rest) as `"$elem`"" }
    $v = $S
    if ($v.Length -lt 4 -or -not ($v.Substring(0, 4) -match '\A[0-9]{4}\z')) { return & $bad $v '2006' }
    $year = [int]$v.Substring(0, 4); $v = $v.Substring(4)
    if (-not $v.StartsWith('-', [StringComparison]::Ordinal)) { return & $bad $v '-' }
    $v = $v.Substring(1)
    if (-not ($v -match '\A[0-9]{2}')) { return & $bad $v '01' }
    $month = [int]$v.Substring(0, 2); $v = $v.Substring(2)
    if ($month -lt 1 -or $month -gt 12) { return "parsing time ${q}: month out of range" }
    if (-not $v.StartsWith('-', [StringComparison]::Ordinal)) { return & $bad $v '-' }
    $v = $v.Substring(1)
    if (-not ($v -match '\A[0-9]{2}')) { return & $bad $v '02' }
    $day = [int]$v.Substring(0, 2); $v = $v.Substring(2)
    if ($v.Length -gt 0) { return "parsing time ${q}: extra text: $(Format-LbfGoTimeQuote $v)" }
    if ($day -lt 1 -or $day -gt [DateTime]::DaysInMonth([Math]::Max($year, 1), $month)) { return "parsing time ${q}: day out of range" }
    return $null
}

function Format-LbfGoTimeQuote {
    param([string]$S)
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('"')
    foreach ($b in $script:Utf8NoBom.GetBytes($S)) {
        if ($b -ge 0x80 -or $b -lt 0x20) { [void]$sb.Append('\x' + ([int]$b).ToString('x2')) }
        else {
            if ($b -eq 0x22 -or $b -eq 0x5C) { [void]$sb.Append('\') }
            [void]$sb.Append([char]$b)
        }
    }
    [void]$sb.Append('"')
    return $sb.ToString()
}

function Test-LbfGoTime {
    param([string]$S)
    if ($S.Length -lt 9) { return 'less than 9 characters long' }
    if ($S[2] -ne ':' -or $S[5] -ne ':') { return 'missing colon in correct place' }
    $hms = $S.Substring(0, 8).Split([char]':')
    $nums = @()
    foreach ($tok in $hms) {
        $n = 0
        if (-not [int]::TryParse($tok, [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$n)) { return 'invalid hour/min/sec' }
        if ($n -lt 0) { return 'non-positive hour/min/sec' }
        $nums += $n
    }
    $h = $nums[0]; $m = $nums[1]; $sec = $nums[2]
    if ($h -gt 23 -or $m -gt 59 -or $sec -gt 60) { return 'hour/min/sec out of range' }
    $rest = $S.Substring(8)
    if ($rest.StartsWith('.', [StringComparison]::Ordinal)) {
        $r2 = $rest.Substring(1)
        $nd = 0
        while ($nd -lt $r2.Length -and $r2[$nd] -ge '0' -and $r2[$nd] -le '9') { $nd++ }
        if ($nd -eq 0) { return 'no digits in second fraction' }
        $rest = $r2.Substring($nd)
    }
    if (-not (Test-LbfIn $rest @('z', 'Z'))) {
        if ($rest.Length -ne 6) { return 'offset must be 6 characters long' }
        $sign = 0
        if ($rest[0] -eq '+') { $sign = -1 } elseif ($rest[0] -eq '-') { $sign = 1 } else { return 'offset must begin with plus/minus' }
        $rest = $rest.Substring(1)
        if ($rest[2] -ne ':') { return 'missing colon in offset in correct place' }
        $z = $rest.Split([char]':')
        $zh = 0; $zm = 0
        if (-not [int]::TryParse($z[0], [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$zh) -or -not [int]::TryParse($z[1], [Globalization.NumberStyles]::AllowLeadingSign, $script:Invariant, [ref]$zm)) { return 'invalid hour/min in offset' }
        if ($zh -lt 0 -or $zm -lt 0) { return 'non-positive hour/min in offset' }
        if ($zh -gt 23 -or $zm -gt 59) { return 'hour/min in offset out of range' }
        $hm = ($h * 60 + $m) + $sign * ($zh * 60 + $zm)
        if ($hm -lt 0) { $hm += 24 * 60 }
        $h = [Math]::Floor($hm / 60); $m = $hm % 60
    }
    if ($sec -ge 60 -and ($h -ne 23 -or $m -ne 59)) { return 'invalid leap second' }
    return $null
}

function Test-LbfIPv4 {
    param([string]$S)
    $groups = $S.Split([char]'.')
    if ($groups.Length -ne 4) { return 'expected four decimals' }
    foreach ($g in $groups) {
        if ($g.Length -gt 1 -and $g[0] -eq '0') { return 'leading zeros' }
        $n = 0
        if (-not ($g -match '\A[0-9]+\z') -or -not [int]::TryParse($g, [ref]$n) -or $n -gt 255) { return 'decimal must be between 0 and 255' }
    }
    return $null
}

function Test-LbfHostname {
    param([string]$S)
    $s = $S
    if ($s.EndsWith('.', [StringComparison]::Ordinal)) { $s = $s.Substring(0, $s.Length - 1) }
    if ($script:Utf8NoBom.GetByteCount($s) -gt 253) { return 'more than 253 characters long' }
    foreach ($l in $s.Split([char]'.')) {
        $n = $script:Utf8NoBom.GetByteCount($l)
        if ($n -lt 1 -or $n -gt 63) { return 'label must be 1 to 63 characters long' }
        if ($l.StartsWith('-', [StringComparison]::Ordinal)) { return 'label starts with hyphen' }
        if ($l.EndsWith('-', [StringComparison]::Ordinal)) { return 'label ends with hyphen' }
        foreach ($r in (Get-LbfRunes $l)) {
            $ok = ($r -ge 0x61 -and $r -le 0x7A) -or ($r -ge 0x41 -and $r -le 0x5A) -or ($r -ge 0x30 -and $r -le 0x39) -or $r -eq 0x2D
            if (-not $ok) { return "invalid character $(Format-LbfQuoteRune ($script:Utf8NoBom.GetBytes((ConvertFrom-LbfRune $r))) 0)" }
        }
    }
    return $null
}

function Test-LbfEmail {
    param([string]$S)
    if ($script:Utf8NoBom.GetByteCount($S) -gt 254) { return 'more than 255 characters long' }
    $at = $S.LastIndexOf([char]'@')
    if ($at -lt 0) { return 'missing @' }
    $local = $S.Substring(0, $at)
    $domain = $S.Substring($at + 1)
    if ($script:Utf8NoBom.GetByteCount($local) -gt 64) { return 'local part more than 64 characters long' }
    if ($local.Length -gt 1 -and $local.StartsWith('"', [StringComparison]::Ordinal) -and $local.EndsWith('"', [StringComparison]::Ordinal)) {
        $inner = $local.Substring(1, $local.Length - 2)
        if ($inner.Contains('\') -or $inner.Contains('"')) { return 'backslash and quote are not allowed within quoted local part' }
    } else {
        if ($local.StartsWith('.', [StringComparison]::Ordinal)) { return 'starts with dot' }
        if ($local.EndsWith('.', [StringComparison]::Ordinal)) { return 'ends with dot' }
        if ($local.Contains('..')) { return 'consecutive dots' }
        foreach ($r in (Get-LbfRunes $local)) {
            $ok = ($r -ge 0x61 -and $r -le 0x7A) -or ($r -ge 0x41 -and $r -le 0x5A) -or ($r -ge 0x30 -and $r -le 0x39) -or ($r -lt 0x80 -and ".!#$%&'*+-/=?^_``{|}~".IndexOf([char]$r) -ge 0)
            if (-not $ok) { return "invalid character $(Format-LbfQuoteRune ($script:Utf8NoBom.GetBytes((ConvertFrom-LbfRune $r))) 0)" }
        }
    }
    if ($domain.StartsWith('[', [StringComparison]::Ordinal) -and $domain.EndsWith(']', [StringComparison]::Ordinal)) {
        $ip = $domain.Substring(1, $domain.Length - 2)
        if ($ip.StartsWith('IPv6:', [StringComparison]::Ordinal)) {
            $e = Test-LbfFormat 'ipv6' $ip.Substring(5)
            if ($null -ne $e) { return "invalid ipv6 address: $e" }
            return $null
        }
        $e = Test-LbfIPv4 $ip
        if ($null -ne $e) { return "invalid ipv4 address: $e" }
        return $null
    }
    $e = Test-LbfHostname $domain
    if ($null -ne $e) { return "invalid domain: $e" }
    return $null
}
