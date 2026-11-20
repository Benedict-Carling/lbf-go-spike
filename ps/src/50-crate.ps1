$script:RoCrateContext = 'https://w3id.org/ro/crate/1.3/context'
$script:RoCrateSpec = 'https://w3id.org/ro/crate/1.3'
$script:LicenseUrl = 'https://rightsstatements.org/vocab/InC/1.0/'
$script:ProfileVersion = New-Object System.Text.RegularExpressions.Regex ('/v?[0-9]+(\.[0-9]+)*\z')

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
    param([Parameter(Mandatory)][string]$InputPath, [Parameter(Mandatory)][hashtable]$Provenance, [AllowEmptyString()][string]$ProfileDir = '', [AllowEmptyString()][string]$Id = '')
    if ($Id.Length -eq 0) { $Id = New-LbfId }
    elseif (-not (Test-LbfMintedId $Id)) { throw (New-LbfError "--id $(Format-LbfQuote $Id) is not an ID from 'lbf new-id'") }
    $prov = Get-LbfProvenance $Provenance
    $prof = Get-LbfProfile $ProfileDir
    $ds = Get-LbfDatasetFiles $InputPath
    if ($ds.Files.Count -eq 0) { throw (New-LbfError "$InputPath contains no files to upload") }
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
    Assert-LbfProfile $Publication.Profile (Get-LbfPublicationView $Publication $Target $published)
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

# What profiles check: the dataset's files beside what its crate records.
function Get-LbfPublicationView {
    param([hashtable]$Publication, [hashtable]$Target, [datetime]$Published)
    $name = Get-LbfBase $Publication.Source
    $prefix = $name + '/'
    $files = New-Object System.Collections.Generic.List[object] ($Publication.Files.Count)
    $dictType = [System.Collections.Generic.Dictionary[string,object]]
    $ordinal = [StringComparer]::Ordinal
    foreach ($f in $Publication.Files) {
        $path = $f.Rel
        if ($path.StartsWith($prefix, [StringComparison]::Ordinal)) { $path = $path.Substring($prefix.Length) }
        $m = $dictType::new(2, $ordinal)
        $m['path'] = $path
        $m['size'] = [long]$f.Size
        $files.Add($m)
    }
    $prov = $Publication.Provenance
    $crate = New-LbfMap
    $crate['identifier'] = $Publication.ID
    $crate['datePublished'] = Get-LbfTimestamp $Published
    $conforms = New-Object System.Collections.Generic.List[object]
    foreach ($c in (Get-LbfConformsTo $Publication)) { $conforms.Add($c) }
    $crate['conformsTo'] = $conforms
    $props = New-LbfMap
    if ($null -ne $prov.Properties) { foreach ($k in $prov.Properties.Keys) { $props[$k] = $prov.Properties[$k] } }
    $crate['additionalProperty'] = $props
    $user = ''
    if ($null -ne $Target.User) { $user = ([string]$Target.User).Trim() }
    if ($user.Length -ne 0) { $crate['creator'] = $user }
    if (-not [string]::IsNullOrEmpty($prov.DerivedFrom)) {
        $crate['wasDerivedFrom'] = $prov.DerivedFrom
        $ins = New-Object System.Collections.Generic.List[object]
        foreach ($in in $prov.Instruments) {
            $i = New-LbfMap
            $i['name'] = $in.Name; $i['version'] = $in.Version; $i['url'] = $in.URL
            $ins.Add($i)
        }
        $crate['instrument'] = $ins
    }
    $data = New-LbfMap
    $data['name'] = $name
    $data['files'] = $files
    $view = New-LbfMap
    $view['data'] = $data
    $view['crate'] = $crate
    return , $view
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
        foreach ($k in $keys) { $list.Add((& $pv $k $props[$k])) }
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

# A profile's entity, named by its title and versioned by the end of its $id.
function Get-LbfProfileEntity {
    param([hashtable]$Profile, [string]$Uri)
    $titles = Get-LbfProfileTitles $Profile
    $name = $Uri
    $t = $null
    if ($titles.TryGetValue($Uri, [ref]$t) -and -not [string]::IsNullOrEmpty($t)) { $name = $t }
    $e = New-LbfMap
    $e['@id'] = $Uri
    $e['@type'] = [object[]]@('CreativeWork', 'Profile')
    $e['name'] = $name
    $m = $script:ProfileVersion.Match($Uri)
    if ($m.Success) { $e['version'] = $m.Value.Substring(1) }
    return , $e
}

# Go's buildCrate: RO-Crate 1.3, flattened, keys sorted, indented by four spaces.
function New-LbfCrateText {
    param([Parameter(Mandatory)][hashtable]$Publication, [Parameter(Mandatory)][hashtable]$Target, [Parameter(Mandatory)][datetime]$Published)
    $p = $Publication
    $prov = $p.Provenance
    $props = Get-LbfCrateProperties $p $Target
    $propRefs = New-Object System.Collections.Generic.List[object]
    foreach ($e in $props) { $propRefs.Add((New-LbfRef $e['@id'])) }
    $name = $prov.Name
    if ([string]::IsNullOrEmpty($name)) { $name = Get-LbfDefaultName $p.Source }
    $desc = $prov.Description
    if ([string]::IsNullOrEmpty($desc)) { $desc = Get-LbfDefaultDescription $p.Source $p.Files.Count $prov.DerivedFrom }
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
    $mark = 'lbf-' + [Guid]::NewGuid().ToString('N')
    $root['hasPart'] = $mark + '-parts'
    $rendered = Get-LbfRenderedFiles $p.Files
    $graph = New-Object System.Collections.Generic.List[object] ($p.Files.Count + 16)
    $graph.Add($root)
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
            $graph.Add((Get-LbfProfileEntity $p.Profile $uri))
        }
        $root['conformsTo'] = Get-LbfRefs $profiles
    }
    $graph.Add($mark + '-files')
    $doc = New-LbfMap
    $doc['@context'] = $script:RoCrateContext
    $doc['@graph'] = $graph
    $text = ConvertTo-LbfJson $doc -Indent '    '
    $text = $text.Replace('"' + $mark + '-parts"', $rendered.Parts)
    return $text.Replace('"' + $mark + '-files"', $rendered.Entities)
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
