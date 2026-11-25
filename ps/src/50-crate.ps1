$script:RoCrateContext = 'https://w3id.org/ro/crate/1.3/context'
$script:RoCrateSpec = 'https://w3id.org/ro/crate/1.3'
$script:LicenseUrl = 'https://rightsstatements.org/vocab/InC/1.0/'
$script:RoleSchema = 'http://www.w3.org/ns/dx/prof/role/schema'
$script:DialectNames = @{
    'https://json-schema.org/draft/2020-12/schema' = 'JSON Schema 2020-12'
    'https://json-schema.org/draft/2019-09/schema' = 'JSON Schema 2019-09'
    'http://json-schema.org/draft-07/schema#' = 'JSON Schema draft-07'
}

# A fixed table, not the OS's, so a crate is the same wherever it is written.
$script:MediaTypes = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
foreach ($pair in @(
        '.csv=text/csv', '.tsv=text/tab-separated-values', '.txt=text/plain', '.md=text/markdown',
        '.json=application/json', '.xml=application/xml', '.pdf=application/pdf',
        '.tif=image/tiff', '.tiff=image/tiff', '.png=image/png', '.jpg=image/jpeg', '.jpeg=image/jpeg',
        '.zip=application/zip', '.gz=application/gzip', '.parquet=application/vnd.apache.parquet',
        '.h5=application/x-hdf5', '.hdf5=application/x-hdf5', '.fcs=application/vnd.isac.fcs')) {
    $kv = $pair.Split([char]'=')
    $script:MediaTypes[$kv[0]] = $kv[1]
}

# Go's preparePublication: everything that can fail without Azure, so a bad dataset never reaches sign-in or upload.
function New-LbfPublication {
    param([Parameter(Mandatory)][string]$InputPath, [Parameter(Mandatory)][hashtable]$Provenance, [AllowEmptyCollection()][string[]]$ProfileDirs = @(), [AllowEmptyString()][string]$Id = '')
    if ($Id.Length -eq 0) { $Id = New-LbfId }
    elseif (-not (Test-LbfMintedId $Id)) { throw (New-LbfError "--id $(Format-LbfQuote $Id) is not an ID from 'lbf new-id'") }
    $prov = Get-LbfProvenance $Provenance
    $prof = Get-LbfProfiles $ProfileDirs
    $ds = Get-LbfDatasetFiles $InputPath
    if ($ds.Files.Count -eq 0) { throw (New-LbfError "$InputPath contains no files to upload") }
    $top = $ds.Files[0].Rel.Split([char]'/')[0]
    if (Test-LbfSame (Get-LbfNameKey $top) (Get-LbfNameKey $script:ProfilesDir)) {
        throw (New-LbfError "$InputPath is named $top, which lbf keeps for the schemas a dataset was checked against; publish it under another name")
    }
    $ds.Files.AddRange((Get-LbfProfileFiles $prof))
    $problems = Get-LbfBlobNameProblems $Id $ds.Files
    if ($problems.Count -gt 0) { throw (New-LbfProblemList "$InputPath cannot be published as it is, so nothing was uploaded:" $problems) }
    return @{ ID = $Id; Source = $ds.Source; Files = $ds.Files; Provenance = $prov; Profile = $prof }
}

# Go's publication.crate: checked against the profiles, then built.
function Get-LbfCrate {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target)
    $published = Test-LbfPublication -Publication $Publication -Target $Target
    return New-LbfCrateText -Publication $Publication -Target $Target -Published $published
}

# Go's publication.validate: the time it was published, once it meets every profile.
function Test-LbfPublication {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target)
    $published = [DateTime]::UtcNow
    $view = Get-LbfFramedRoot $Publication $Target $published
    Assert-LbfProfile $Publication.Profile $view (Get-LbfProfileRecord $Publication.Profile $Publication.Provenance.Properties)
    Write-LbfLog "[profile] meets $([string]::Join(', ', (Get-LbfProfileIds $Publication.Profile).ToArray()))`n"
    return $published
}

function Get-LbfTimestamp {
    param([datetime]$Time)
    return $Time.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $script:Invariant)
}

# What lbf names a dataset when the publisher does not.
function Get-LbfDefaultName {
    param([string]$Source)
    return Get-LbfBase $Source
}

function Get-LbfDefaultDescription {
    param([string]$Source, [int]$Files, [string]$DerivedFrom)
    $noun = 'files'
    if ($Files -eq 1) { $noun = 'file' }
    $d = "Dataset of $Files $noun from $(Get-LbfBase $Source)"
    if ($DerivedFrom.Length -ne 0) { $d += ', derived from ' + $DerivedFrom }
    return $d
}

# The profiles a crate declares: Process Run Crate when lbf records how it was made, then bronze up to the chosen one.
function Get-LbfConformsTo {
    param([hashtable]$Publication)
    $ids = New-Object System.Collections.Generic.List[string]
    if ($null -eq $Publication.Profile) { return , $ids }
    if (-not [string]::IsNullOrEmpty($Publication.Provenance.DerivedFrom)) { $ids.Add($script:ProcessRunCrate) }
    $ids.AddRange((Get-LbfProfileIds $Publication.Profile))
    return , $ids
}

function New-LbfRef {
    param([string]$Id)
    $m = New-LbfMap
    $m['@id'] = $Id
    return , $m
}

# RO-Crate 1.3 writes one value on its own, not as a list of one.
function Get-LbfRefs {
    param([System.Collections.Generic.List[object]]$Refs)
    if ($Refs.Count -eq 1) { return , $Refs[0] }
    return , $Refs
}

function New-LbfEntity {
    param([hashtable]$Pairs)
    $m = New-LbfMap
    foreach ($k in $Pairs.Keys) { $m[$k] = $Pairs[$k] }
    return , $m
}

$script:FileIdPlain = New-Object System.Text.RegularExpressions.Regex ('\A[A-Za-z0-9_.~/-]*\z')
$script:JsonPlain = New-Object System.Text.RegularExpressions.Regex ('[\x00-\x1F"\\<>&\u2028\u2029\uD800-\uDFFF]')

# Percent-encodes like Python's urllib.parse.quote, as ro-crate-py does.
function Get-LbfFileId {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Rel)
    if ($script:FileIdPlain.IsMatch($Rel)) { return $Rel }
    $sb = New-Object System.Text.StringBuilder
    foreach ($b in $script:Utf8NoBom.GetBytes($Rel)) {
        if (($b -ge 0x61 -and $b -le 0x7A) -or ($b -ge 0x41 -and $b -le 0x5A) -or ($b -ge 0x30 -and $b -le 0x39) -or $b -eq 0x5F -or $b -eq 0x2E -or $b -eq 0x2D -or $b -eq 0x7E -or $b -eq 0x2F) {
            [void]$sb.Append([char]$b)
        } else {
            [void]$sb.Append('%').Append(([int]$b).ToString('X2'))
        }
    }
    return $sb.ToString()
}

function Get-LbfParentId {
    param([string]$Id)
    return '#source-' + $Id
}

# lbf's own properties, then the publisher's by name.
function Get-LbfCrateProperties {
    param([hashtable]$Publication, [hashtable]$Target)
    $list = New-Object System.Collections.Generic.List[object]
    $pv = {
        param($name, $value)
        $e = New-LbfMap
        $e['@id'] = '#' + (Get-LbfFileId $name)
        $e['@type'] = 'PropertyValue'
        $e['name'] = $name
        $e['value'] = $value
        return , $e
    }
    $list.Add((& $pv 'subscription_name' (Get-LbfOrUnknown $Target.SubscriptionName)))
    $list.Add((& $pv 'subscription_id' (Get-LbfOrUnknown $Target.SubscriptionID)))
    $list.Add((& $pv 'source_path' $Publication.Source.Replace([string][IO.Path]::DirectorySeparatorChar, '/')))
    $props = $Publication.Provenance.Properties
    if ($null -ne $props -and $props.Count -gt 0) {
        $keys = New-Object string[] $props.Count
        $props.Keys.CopyTo($keys, 0)
        $sortKeys = New-Object string[] $keys.Length
        for ($i = 0; $i -lt $keys.Length; $i++) { $sortKeys[$i] = Get-LbfUtf8SortKey $keys[$i] }
        [Array]::Sort([Array]$sortKeys, [Array]$keys, [Collections.IComparer][StringComparer]::Ordinal)
        $kinds = Get-LbfPropertyKinds $Publication.Profile
        foreach ($k in $keys) {
            $kind = $null
            [void]$kinds.TryGetValue($k, [ref]$kind)
            $e = & $pv $k (ConvertTo-LbfTypedValue $kind $props[$k])
            if ($null -ne $kind -and $kind.UnitCode.Length -ne 0) { $e['unitCode'] = $kind.UnitCode }
            if ($null -ne $kind -and $kind.UnitText.Length -ne 0) { $e['unitText'] = $kind.UnitText }
            $list.Add($e)
        }
    }
    return , $list
}

# The parent, the instruments and the run that made the dataset from one with them, as its publisher stated.
function Get-LbfRunEntities {
    param([hashtable]$Publication, [bool]$ByUploader)
    $prov = $Publication.Provenance
    $parent = Get-LbfParentId $prov.DerivedFrom
    $graph = New-Object System.Collections.Generic.List[object]
    $graph.Add((New-LbfEntity @{ '@id' = $parent; '@type' = 'Dataset'; 'identifier' = $prov.DerivedFrom; 'name' = $prov.DerivedFrom }))
    $tools = New-Object System.Collections.Generic.List[object]
    $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($in in $prov.Instruments) {
        $tools.Add((New-LbfRef $in.URL))
        if ($seen.Add($in.URL)) {
            $graph.Add((New-LbfEntity @{ '@id' = $in.URL; '@type' = 'SoftwareApplication'; 'name' = $in.Name; 'version' = $in.Version; 'url' = $in.URL }))
        }
    }
    $first = $prov.Instruments[0]
    $run = New-LbfMap
    $run['@id'] = '#run'
    $run['@type'] = 'CreateAction'
    $run['name'] = "Dataset $($Publication.ID) produced by $($first.Name)"
    $run['description'] = "$($first.Name) $($first.Version) made dataset $($Publication.ID) from $($prov.DerivedFrom), as its publisher stated to lbf"
    $run['instrument'] = Get-LbfRefs $tools
    $run['object'] = New-LbfRef $parent
    $run['result'] = New-LbfRef './'
    if ($ByUploader) { $run['agent'] = New-LbfRef '#uploader' }
    $graph.Add($run)
    return , $graph
}

# Go's crateEntities: a profile's entity, what it builds on, and the schema file the dataset was checked against.
function Get-LbfProfileEntities {
    param([hashtable]$Profile, [string]$Uri)
    $e = New-LbfMap
    $e['@id'] = $Uri
    $e['@type'] = [object[]]@('CreativeWork', 'Profile')
    $e['name'] = $Uri
    $out = New-Object System.Collections.Generic.List[object]
    $out.Add($e)
    if (Test-LbfSame $Uri $script:ProcessRunCrate) {
        $e['name'] = 'Process Run Crate'
        $e['version'] = '0.6'
        return , $out
    }
    $r = $null
    foreach ($x in $Profile.Rules) { if (Test-LbfSame $x.ID $Uri) { $r = $x; break } }
    $name = $r.Title
    if ($name.Length -eq 0) { $name = $Uri }
    $e['name'] = $name
    if (-not [string]::IsNullOrEmpty($r.Version)) {
        $v = $r.Version
        if ($v.StartsWith('v', [StringComparison]::Ordinal)) { $v = $v.Substring(1) }
        $e['version'] = $v
    }
    if ($r.Parent.Length -ne 0) { $e['isProfileOf'] = New-LbfRef $r.Parent }
    if ($null -eq $r.Raw) { return , $out }
    $descriptor = '#profile-' + (Get-LbfFileId ($r.Name + '-' + $r.Version))
    $e['hasResource'] = New-LbfRef $descriptor
    $d = New-LbfMap
    $d['@id'] = $descriptor
    $d['@type'] = [object[]]@('CreativeWork', 'ResourceDescriptor')
    $d['name'] = 'The schema of ' + $name
    $d['hasRole'] = New-LbfRef $script:RoleSchema
    $d['hasArtifact'] = New-LbfRef (Get-LbfFileId (Get-LbfRuleFile $r))
    $out.Add($d)
    return , $out
}

function Test-LbfHasContent {
    param([hashtable]$File)
    return $File.ContainsKey('Content') -and $null -ne $File.Content
}

# Go's fileEntity, for the files the crate's text does not write directly.
function Get-LbfFileEntity {
    param([hashtable]$File)
    $rel = $File.Rel
    $slash = $rel.LastIndexOf([char]'/')
    $base = $rel
    if ($slash -ge 0) { $base = $rel.Substring($slash + 1) }
    $e = New-LbfMap
    $e['@id'] = Get-LbfFileId $rel
    $e['@type'] = 'File'
    $e['name'] = $base
    $e['contentSize'] = ([long]$File.Size).ToString($script:Invariant)
    $dot = $base.LastIndexOf([char]'.')
    $fmt = $null
    if ($dot -ge 0 -and $script:MediaTypes.TryGetValue($base.Substring($dot).ToLowerInvariant(), [ref]$fmt)) { $e['encodingFormat'] = $fmt }
    if (Test-LbfProfileFile $rel) {
        $e['encodingFormat'] = 'application/schema+json'
        $dialect = $script:Draft202012
        if ($File.ContainsKey('Dialect') -and $null -ne $File.Dialect) { $dialect = $File.Dialect }
        $e['conformsTo'] = New-LbfRef $dialect
        $e['description'] = "A profile's schema, exactly as this dataset was checked against it"
    }
    if (-not [string]::IsNullOrEmpty($File.SHA256)) { $e['sha256'] = $File.SHA256 }
    return , $e
}

# Go's buildCrate without its files: the root, which has no hasPart yet, and every other entity in order.
function New-LbfCrateGraph {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][datetime]$Published)
    $p = $Publication
    $prov = $p.Provenance
    $props = Get-LbfCrateProperties $p $Target
    $propRefs = New-Object System.Collections.Generic.List[object]
    foreach ($e in $props) { $propRefs.Add((New-LbfRef $e['@id'])) }
    $name = $prov.Name
    if ([string]::IsNullOrEmpty($name)) { $name = Get-LbfDefaultName $p.Source }
    $desc = $prov.Description
    if ([string]::IsNullOrEmpty($desc)) { $desc = Get-LbfDefaultDescription $p.Source (Get-LbfDataFileCount $p.Files) $prov.DerivedFrom }
    $root = New-LbfMap
    $root['@id'] = './'
    $root['@type'] = 'Dataset'
    $root['identifier'] = $p.ID
    $root['name'] = $name
    $root['description'] = $desc
    $root['datePublished'] = Get-LbfTimestamp $Published
    $root['license'] = New-LbfRef $script:LicenseUrl
    $root['distribution'] = New-LbfRef '#blob-location'
    $root['additionalProperty'] = Get-LbfRefs $propRefs
    $graph = New-Object System.Collections.Generic.List[object]
    $graph.Add((New-LbfEntity @{ '@id' = $script:CrateName; '@type' = 'CreativeWork'; 'about' = (New-LbfRef './'); 'conformsTo' = (New-LbfRef $script:RoCrateSpec) }))
    $graph.Add((New-LbfEntity @{ '@id' = '#blob-location'; '@type' = 'DataDownload'; 'name' = "Where $($p.ID) is stored"; 'contentUrl' = ((Get-LbfContainerUrl $Target) + '/' + $p.ID) }))
    $graph.Add((New-LbfEntity @{ '@id' = $script:LicenseUrl; '@type' = 'CreativeWork'; 'name' = 'In Copyright'
                'description' = "This item is protected by copyright and/or related rights; uses beyond those the law permits need the rights-holders' permission." }))
    $uploader = ''
    if ($null -ne $Target.User) { $uploader = ([string]$Target.User).Trim() }
    if ($uploader.Length -ne 0) {
        $root['creator'] = New-LbfRef '#uploader'
        $graph.Add((New-LbfEntity @{ '@id' = '#uploader'; '@type' = 'Person'; 'name' = $uploader }))
    }
    $graph.AddRange($props)
    if (-not [string]::IsNullOrEmpty($prov.DerivedFrom)) {
        $root['isBasedOn'] = New-LbfRef (Get-LbfParentId $prov.DerivedFrom)
        $root['mentions'] = New-LbfRef '#run'
        $graph.AddRange((Get-LbfRunEntities $p ($uploader.Length -ne 0)))
    }
    $conformsTo = Get-LbfConformsTo $p
    if ($conformsTo.Count -gt 0) {
        $profiles = New-Object System.Collections.Generic.List[object]
        foreach ($uri in $conformsTo) {
            $profiles.Add((New-LbfRef $uri))
            $graph.AddRange((Get-LbfProfileEntities $p.Profile $uri))
        }
        $root['conformsTo'] = Get-LbfRefs $profiles
        if ($p.Profile.Rules.Count -gt 0) {
            $graph.Add((New-LbfEntity @{ '@id' = $script:RoleSchema; '@type' = 'DefinedTerm'; 'name' = 'Schema'; 'description' = "A schema the profile's data must meet" }))
            $dialects = New-Object System.Collections.Generic.List[string]
            foreach ($r in $p.Profile.Rules) {
                if ($null -eq $r.Raw) { continue }
                $d = Get-LbfSchemaDialect $r.Doc
                if ($dialects.Contains($d)) { continue }
                $dialects.Add($d)
                $dn = $d
                foreach ($k in $script:DialectNames.Keys) { if (Test-LbfSame $k $d) { $dn = $script:DialectNames[$k] } }
                $graph.Add((New-LbfEntity @{ '@id' = $d; '@type' = 'CreativeWork'; 'name' = $dn }))
            }
        }
    }
    return @{ Root = $root; Graph = $graph }
}

# The files published from the folder, as opposed to the schemas the dataset carries beside them.
function Get-LbfDataFileCount {
    param($Files)
    $n = 0
    foreach ($f in $Files) { if (-not (Test-LbfProfileFile $f.Rel)) { $n++ } }
    return $n
}

# Go's buildCrate: RO-Crate 1.3, flattened, keys sorted, indented by four spaces.
function New-LbfCrateText {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][datetime]$Published)
    $g = New-LbfCrateGraph $Publication $Target $Published
    $mark = 'lbf-' + [Guid]::NewGuid().ToString('N')
    $g.Root['hasPart'] = $mark + '-parts'
    $rendered = Get-LbfRenderedFiles $Publication.Files
    $graph = New-Object System.Collections.Generic.List[object] ($g.Graph.Count + 2)
    $graph.Add($g.Root)
    $graph.AddRange($g.Graph)
    $graph.Add($mark + '-files')
    $doc = New-LbfMap
    $doc['@context'] = $script:RoCrateContext
    $doc['@graph'] = $graph
    $text = ConvertTo-LbfJson $doc -Indent '    '
    $text = $text.Replace('"' + $mark + '-parts"', $rendered.Parts)
    return $text.Replace('"' + $mark + '-files"', $rendered.Entities)
}

# The RO-Crate context's prefixes, which compaction writes an @id with.
$script:CuriePrefixes = @(
    @('pcdm', 'http://pcdm.org/models#'), @('bibo', 'http://purl.org/ontology/bibo/'), @('cc', 'http://creativecommons.org/ns#'),
    @('dct', 'http://purl.org/dc/terms/'), @('foaf', 'http://xmlns.com/foaf/0.1/'), @('prof', 'http://www.w3.org/ns/dx/prof/'),
    @('profrole', 'http://www.w3.org/ns/dx/prof/role/'), @('rdf', 'http://www.w3.org/1999/02/22-rdf-syntax-ns#'), @('rdfa', 'http://www.w3.org/ns/rdfa#'),
    @('rdfs', 'http://www.w3.org/2000/01/rdf-schema#'), @('schema', 'http://schema.org/'), @('frapo', 'http://purl.org/cerif/frapo/'),
    @('rel', 'https://www.w3.org/ns/iana/link-relations/relation#'), @('pav', 'http://purl.org/pav/'), @('prov', 'http://www.w3.org/ns/prov#'),
    @('wfdesc', 'http://purl.org/ro/wfdesc#'), @('wfprov', 'http://purl.org/ro/wfprov#'), @('roterms', 'http://purl.org/ro/roterms#'),
    @('relation', 'http://www.iana.org/assignments/relation/'), @('wf4ever', 'http://purl.org/ro/wf4ever#'), @('vann', 'http://purl.org/vocab/vann/'),
    @('geosparql', 'http://www.opengis.net/ont/geosparql#'))

# An @id as JSON-LD compaction writes it: the shortest compact IRI a context prefix gives, if any.
function Get-LbfCompactId {
    param([string]$Id)
    $best = $null
    foreach ($pair in $script:CuriePrefixes) {
        if ($Id.Length -gt $pair[1].Length -and $Id.StartsWith($pair[1], [StringComparison]::Ordinal)) {
            $c = $pair[0] + ':' + $Id.Substring($pair[1].Length)
            if ($null -eq $best -or $c.Length -lt $best.Length -or ($c.Length -eq $best.Length -and [string]::CompareOrdinal($c, $best) -lt 0)) { $best = $c }
        }
    }
    if ($null -eq $best) { return $Id }
    return $best
}

$script:FramedSets = @('hasPart', 'conformsTo', 'isBasedOn', 'mentions', 'instrument', 'object', 'hasResource')

# A node as bronze's frame embeds it: what it refers to embedded in turn, except what encloses it, and additionalProperty indexed by name.
function ConvertTo-LbfFramedNode {
    param([System.Collections.IDictionary]$Node, $ById, [System.Collections.Generic.List[string]]$Stack)
    $out = New-LbfMap
    foreach ($k in @($Node.Keys)) {
        $v = $Node[$k]
        if ($k -ceq '@id') { $out[$k] = Get-LbfCompactId $v; continue }
        if ($k -ceq '@type') { $out[$k] = $v; continue }
        $items = New-Object System.Collections.Generic.List[object]
        if ($v -is [System.Collections.IList] -and $v -isnot [string]) { foreach ($x in $v) { $items.Add($x) } } else { $items.Add($v) }
        $framed = New-Object System.Collections.Generic.List[object]
        $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
        foreach ($x in $items) {
            if ($x -is [System.Collections.IDictionary] -and (Test-LbfDictHas $x '@id')) {
                $id = [string]$x['@id']
                if (-not $seen.Add($id)) { continue }
                if ($Stack.Contains($id) -or -not $ById.ContainsKey($id)) { $framed.Add((New-LbfRef (Get-LbfCompactId $id))); continue }
                $Stack.Add($id)
                $framed.Add((ConvertTo-LbfFramedNode $ById[$id] $ById $Stack))
                $Stack.RemoveAt($Stack.Count - 1)
                continue
            }
            $framed.Add($x)
        }
        if ($k -ceq 'additionalProperty') {
            $index = New-LbfMap
            foreach ($pv in $framed) {
                if ($pv -is [System.Collections.IDictionary] -and (Test-LbfDictHas $pv 'name')) {
                    $name = [string]$pv['name']
                    [void]$pv.Remove('name')
                    $index[$name] = $pv
                }
            }
            $out[$k] = $index
            continue
        }
        if ($script:FramedSets -ccontains $k) { $out[$k] = $framed }
        elseif ($framed.Count -eq 1) { $out[$k] = $framed[0] }
        else { $out[$k] = $framed }
    }
    return , $out
}

# Go's framedRoot: the crate's root as bronze's frame lays it out, with the published files, not the schemas, as hasPart.
function Get-LbfFramedRoot {
    param([hashtable]$Publication, [hashtable]$Target, [datetime]$Published)
    $g = New-LbfCrateGraph $Publication $Target $Published
    $byId = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($e in $g.Graph) { $byId[[string]$e['@id']] = $e }
    $parts = New-Object System.Collections.Generic.List[object] ($Publication.Files.Count)
    foreach ($f in $Publication.Files) {
        if (Test-LbfProfileFile $f.Rel) { $e = Get-LbfFileEntity $f; $byId[[string]$e['@id']] = $e }
        else { $parts.Add((Get-LbfFileEntity $f)) }
    }
    $stack = New-Object System.Collections.Generic.List[string]
    $stack.Add('./')
    $view = ConvertTo-LbfFramedNode $g.Root $byId $stack
    $view['hasPart'] = $parts
    return , $view
}

function ConvertTo-LbfCrateString {
    param([string]$Text)
    if ($script:JsonPlain.IsMatch($Text)) { return ConvertTo-LbfJsonString $Text }
    return '"' + $Text + '"'
}

# The crate's hasPart and file entities as MarshalIndent writes them, built directly since a dataset may hold 100,000 files.
function Get-LbfRenderedFiles {
    param($Files)
    $parts = New-Object System.Text.StringBuilder
    $ents = New-Object System.Text.StringBuilder
    $n = $Files.Count
    $i = 0
    foreach ($f in $Files) {
        $rel = $f.Rel
        if ($script:FileIdPlain.IsMatch($rel)) { $id = $rel } else { $id = Get-LbfFileId $rel }
        $slash = $rel.LastIndexOf([char]'/')
        if ($slash -ge 0) { $base = $rel.Substring($slash + 1) } else { $base = $rel }
        if ($n -eq 1) {
            [void]$parts.Append("{`n                `"@id`": `"").Append($id).Append("`"`n            }")
        } else {
            if ($i -gt 0) { [void]$parts.Append(',') }
            [void]$parts.Append("`n                {`n                    `"@id`": `"").Append($id).Append("`"`n                }")
        }
        if ($i -gt 0) { [void]$ents.Append(",`n        ") }
        if (Test-LbfProfileFile $rel) {
            Write-LbfJsonValue $ents (Get-LbfFileEntity $f) '    ' '        '
            $i++
            continue
        }
        [void]$ents.Append("{`n            `"@id`": `"").Append($id).Append("`",`n            `"@type`": `"File`",`n            `"contentSize`": `"").Append(([long]$f.Size).ToString($script:Invariant)).Append('"')
        $dot = $base.LastIndexOf([char]'.')
        if ($dot -ge 0) {
            $fmt = $null
            if ($script:MediaTypes.TryGetValue($base.Substring($dot).ToLowerInvariant(), [ref]$fmt)) { [void]$ents.Append(",`n            `"encodingFormat`": `"").Append($fmt).Append('"') }
        }
        if ($script:JsonPlain.IsMatch($base)) { $q = ConvertTo-LbfJsonString $base } else { $q = '"' + $base + '"' }
        [void]$ents.Append(",`n            `"name`": ").Append($q)
        if (-not [string]::IsNullOrEmpty($f.SHA256)) { [void]$ents.Append(",`n            `"sha256`": ").Append((ConvertTo-LbfCrateString $f.SHA256)) }
        [void]$ents.Append("`n        }")
        $i++
    }
    if ($n -ne 1) {
        if ($n -eq 0) { [void]$parts.Append('[]') }
        else { $p = '[' + $parts.ToString() + "`n            ]"; $parts = New-Object System.Text.StringBuilder; [void]$parts.Append($p) }
    }
    return @{ Parts = $parts.ToString(); Entities = $ents.ToString() }
}
