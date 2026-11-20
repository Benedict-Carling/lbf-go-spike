$script:CrateName = 'ro-crate-metadata.json'
$script:ValidIdPattern = New-Object System.Text.RegularExpressions.Regex ('\A[A-Za-z0-9][A-Za-z0-9._-]*\z')
$script:MintedIdPattern = New-Object System.Text.RegularExpressions.Regex ('\A[0-9]{8}-[a-z]+-[a-z]+-[0-9a-f]{4}\z')
$script:OnUnix = -not $script:OnWindows
$script:PathSep = [string][IO.Path]::DirectorySeparatorChar
$script:SepPattern = '/'
if ($script:OnWindows) { $script:SepPattern = '[\\/]' }
$script:CaseInsensitiveFs = $script:OnWindows -or (Test-Path -LiteralPath '/System/Library/CoreServices')

# Rels needing a closer look: Windows-hostile characters, a dot or space at a segment end, a reserved stem, or surrogates.
$script:NameSuspect = New-Object System.Text.RegularExpressions.Regex ('[\\:*?"<>|\x00-\x1F\x7F-\x9F\uD800-\uDFFF]|[. ](/|\z)|(\A|/)(con|prn|aux|nul|com|lpt)', [System.Text.RegularExpressions.RegexOptions]::IgnoreCase)
$script:WindowsReserved = New-Object System.Text.RegularExpressions.Regex ('\A([cC][oO][nN]|[pP][rR][nN]|[aA][uU][xX]|[nN][uU][lL]|[cC][oO][mM][1-9\u00B9\u00B2\u00B3]|[lL][pP][tT][1-9\u00B9\u00B2\u00B3]|[cC][oO][nN][iI][nN]\$|[cC][oO][nN][oO][uU][tT]\$)\z')
$script:BadUtf16 = New-Object System.Text.RegularExpressions.Regex ('[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]')
$script:HighChars = New-Object System.Text.RegularExpressions.Regex ('[\uD800-\uFFFF]')
$script:ControlChars = New-Object System.Text.RegularExpressions.Regex ('[\x00-\x1F\x7F-\x9F]')

# Where Unicode case folding differs from lower(upper(c)), as cases.Fold does it.
$script:FoldExceptions = @{}
foreach ($pair in ('DF:73 73,130:69 307,131:131,149:2BC 6E,1F0:6A 30C,390:3B9 308 301,3B0:3C5 308 301,587:565 582,13F8:13F0,13F9:13F1,13FA:13F2,13FB:13F3,13FC:13F4,13FD:13F5,' +
        '1E96:68 331,1E97:74 308,1E98:77 30A,1E99:79 30A,1E9A:61 2BE,1E9E:73 73,1F50:3C5 313,1F52:3C5 313 300,1F54:3C5 313 301,1F56:3C5 313 342,1F80:1F00 3B9,1F81:1F01 3B9,' +
        '1F82:1F02 3B9,1F83:1F03 3B9,1F84:1F04 3B9,1F85:1F05 3B9,1F86:1F06 3B9,1F87:1F07 3B9,1F88:1F00 3B9,1F89:1F01 3B9,1F8A:1F02 3B9,1F8B:1F03 3B9,1F8C:1F04 3B9,1F8D:1F05 3B9,' +
        '1F8E:1F06 3B9,1F8F:1F07 3B9,1F90:1F20 3B9,1F91:1F21 3B9,1F92:1F22 3B9,1F93:1F23 3B9,1F94:1F24 3B9,1F95:1F25 3B9,1F96:1F26 3B9,1F97:1F27 3B9,1F98:1F20 3B9,1F99:1F21 3B9,' +
        '1F9A:1F22 3B9,1F9B:1F23 3B9,1F9C:1F24 3B9,1F9D:1F25 3B9,1F9E:1F26 3B9,1F9F:1F27 3B9,1FA0:1F60 3B9,1FA1:1F61 3B9,1FA2:1F62 3B9,1FA3:1F63 3B9,1FA4:1F64 3B9,1FA5:1F65 3B9,' +
        '1FA6:1F66 3B9,1FA7:1F67 3B9,1FA8:1F60 3B9,1FA9:1F61 3B9,1FAA:1F62 3B9,1FAB:1F63 3B9,1FAC:1F64 3B9,1FAD:1F65 3B9,1FAE:1F66 3B9,1FAF:1F67 3B9,1FB2:1F70 3B9,1FB3:3B1 3B9,' +
        '1FB4:3AC 3B9,1FB6:3B1 342,1FB7:3B1 342 3B9,1FBC:3B1 3B9,1FC2:1F74 3B9,1FC3:3B7 3B9,1FC4:3AE 3B9,1FC6:3B7 342,1FC7:3B7 342 3B9,1FCC:3B7 3B9,1FD2:3B9 308 300,' +
        '1FD3:3B9 308 301,1FD6:3B9 342,1FD7:3B9 308 342,1FE2:3C5 308 300,1FE3:3C5 308 301,1FE4:3C1 313,1FE6:3C5 342,1FE7:3C5 308 342,1FF2:1F7C 3B9,1FF3:3C9 3B9,1FF4:3CE 3B9,' +
        '1FF6:3C9 342,1FF7:3C9 342 3B9,1FFC:3C9 3B9,FB00:66 66,FB01:66 69,FB02:66 6C,FB03:66 66 69,FB04:66 66 6C,FB05:73 74,FB06:73 74,FB13:574 576,FB14:574 565,FB15:574 56B,' +
        'FB16:57E 576,FB17:574 56D').Split([char]',')) {
    $kv = $pair.Split([char]':')
    $to = New-Object System.Text.StringBuilder
    foreach ($h in $kv[1].Split([char]' ')) { [void]$to.Append([char][Convert]::ToInt32($h, 16)) }
    $script:FoldExceptions[[char][Convert]::ToInt32($kv[0], 16)] = $to.ToString()
}
for ($c = 0xAB70; $c -le 0xABBF; $c++) { $script:FoldExceptions[[char]$c] = [string][char]($c - 0xAB70 + 0x13A0) }
$script:FoldExceptionPattern = New-Object System.Text.RegularExpressions.Regex ('[' + (($script:FoldExceptions.Keys | ForEach-Object { '\u{0:X4}' -f [int]$_ }) -join '') + ']')

function Test-LbfValidId {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Id)
    return $script:ValidIdPattern.IsMatch($Id)
}

function Test-LbfMintedId {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Id)
    return $script:MintedIdPattern.IsMatch($Id)
}

# Go's newID: the local date, petname.Generate(2, "-") and four hex digits.
function New-LbfId {
    $b = Get-LbfRandomBytes 8
    $adj = $script:PetnameAdjectives[[int]([BitConverter]::ToUInt32($b, 0) % [uint32]$script:PetnameAdjectives.Length)]
    $name = $script:PetnameNames[[int]([BitConverter]::ToUInt32($b, 4) % [uint32]$script:PetnameNames.Length)]
    $hex = ConvertTo-LbfHex (Get-LbfRandomBytes 2)
    return '{0}-{1}-{2}-{3}' -f [DateTime]::Now.ToString('yyyyMMdd', $script:Invariant), $adj, $name, $hex
}

function Test-LbfExcluded {
    param([string]$Name)
    return $Name -ceq '.DS_Store' -or $Name.StartsWith('._', [StringComparison]::Ordinal)
}

# A path .NET Framework can open however long it is.
function Get-LbfIoPath {
    param([Parameter(Mandatory)][string]$Path)
    if (-not $script:OnWindows -or $Path.Length -lt 248 -or $Path.StartsWith('\\?\', [StringComparison]::Ordinal)) { return $Path }
    if ($Path.StartsWith('\\', [StringComparison]::Ordinal)) { return '\\?\UNC\' + $Path.Substring(2) }
    return '\\?\' + $Path
}

# ---------- Go's path/filepath, lexically ----------

function Test-LbfSep {
    param([char]$C)
    return $C -eq '/' -or ($script:OnWindows -and $C -eq '\')
}

function Get-LbfVolumeLength {
    param([string]$Path)
    if (-not $script:OnWindows) { return 0 }
    if ($Path.Length -ge 2 -and $Path[1] -eq ':' -and [char]::IsLetter($Path[0])) { return 2 }
    if ($Path.Length -ge 2 -and (Test-LbfSep $Path[0]) -and (Test-LbfSep $Path[1])) {
        $m = [regex]::Match($Path, '\A[\\/]{2}[^\\/]+[\\/][^\\/]+')
        if ($m.Success) { return $m.Length }
    }
    return 0
}

# Go's filepath.Clean.
function Get-LbfCleanPath {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Path)
    $volLen = Get-LbfVolumeLength $Path
    $vol = $Path.Substring(0, $volLen)
    $rest = $Path.Substring($volLen)
    if ($rest -eq '') {
        if ($volLen -gt 1 -and $vol[1] -ne ':') { return $vol.Replace('/', '\') }
        return $vol + '.'
    }
    $rooted = Test-LbfSep $rest[0]
    $out = New-Object System.Collections.Generic.List[string]
    foreach ($part in ($rest -split $script:SepPattern)) {
        if ($part -eq '' -or $part -eq '.') { continue }
        if ($part -eq '..') {
            if ($out.Count -gt 0 -and $out[$out.Count - 1] -ne '..') { $out.RemoveAt($out.Count - 1) }
            elseif (-not $rooted) { $out.Add('..') }
            continue
        }
        $out.Add($part)
    }
    $s = [string]::Join($script:PathSep, $out.ToArray())
    if ($rooted) { $s = $script:PathSep + $s }
    if ($s -eq '') { $s = '.' }
    if ($script:OnWindows) { $vol = $vol.Replace('/', '\') }
    return $vol + $s
}

function Get-LbfBase {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Path)
    if ($Path -eq '') { return '.' }
    $p = $Path
    while ($p.Length -gt 0 -and (Test-LbfSep $p[$p.Length - 1])) { $p = $p.Substring(0, $p.Length - 1) }
    $p = $p.Substring((Get-LbfVolumeLength $p))
    $i = $p.Length - 1
    while ($i -ge 0 -and -not (Test-LbfSep $p[$i])) { $i-- }
    $p = $p.Substring($i + 1)
    if ($p -eq '') { return $script:PathSep }
    return $p
}

function Get-LbfDir {
    param([Parameter(Mandatory)][string]$Path)
    $volLen = Get-LbfVolumeLength $Path
    $i = $Path.Length - 1
    while ($i -ge $volLen -and -not (Test-LbfSep $Path[$i])) { $i-- }
    $dir = Get-LbfCleanPath ($Path.Substring($volLen, $i + 1 - $volLen))
    if ($dir -eq '.' -and $volLen -gt 2) { return $Path.Substring(0, $volLen) }
    return $Path.Substring(0, $volLen) + $dir
}

function Join-LbfPath {
    param([string]$A, [string]$B)
    if ($A -eq '') { return Get-LbfCleanPath $B }
    return Get-LbfCleanPath ($A + $script:PathSep + $B)
}

# Go's filepath.Abs.
function Get-LbfAbsPath {
    param([Parameter(Mandatory)][string]$Path)
    if ($script:OnWindows) {
        $full = [IO.Path]::GetFullPath([IO.Path]::Combine((Get-Location -PSProvider FileSystem).ProviderPath, $Path))
        return Get-LbfCleanPath $full
    }
    if ($Path.StartsWith('/', [StringComparison]::Ordinal)) { return Get-LbfCleanPath $Path }
    return Join-LbfPath (Get-Location -PSProvider FileSystem).ProviderPath $Path
}

# ---------- links ----------

# Lstat: the entry's own attributes, without following a link; throws as .NET does when it is missing.
function Get-LbfLinkAttributes {
    param([string]$Path)
    return [IO.File]::GetAttributes((Get-LbfIoPath $Path))
}

function Get-LbfLinkInfo {
    param([string]$Path)
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    $type = $null
    $target = $null
    if ($item.PSObject.Properties['LinkType']) { $type = $item.LinkType }
    if ($item.PSObject.Properties['Target'] -and $null -ne $item.Target) { $target = [string]@($item.Target)[0] }
    if ($null -ne $target -and $target.StartsWith('\??\', [StringComparison]::Ordinal)) { $target = $target.Substring(4) }
    return @{ Type = $type; Target = $target }
}

function Test-LbfSymlink {
    param([string]$Path, [IO.FileAttributes]$Attributes)
    if (($Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) { return $false }
    return (Get-LbfLinkInfo $Path).Type -eq 'SymbolicLink'
}

function New-LbfLstatError {
    param([string]$Path, $Err)
    if ($script:OnWindows) { return New-LbfError (Format-LbfOsError 'GetFileAttributesEx' $Path $Err) }
    return New-LbfError (Format-LbfOsError 'lstat' $Path $Err)
}

# Go's filepath.EvalSymlinks: symlinks resolved, junctions left as they are, Windows names in their stored case.
function Resolve-LbfSymlinks {
    param([Parameter(Mandatory)][string]$Path)
    $volLen = Get-LbfVolumeLength $Path
    if ($volLen -lt $Path.Length -and (Test-LbfSep $Path[$volLen])) { $volLen++ }
    $vol = $Path.Substring(0, $volLen)
    $dest = $vol
    $links = 0
    $start = $volLen
    $end = $volLen
    while ($start -lt $Path.Length) {
        while ($start -lt $Path.Length -and (Test-LbfSep $Path[$start])) { $start++ }
        $end = $start
        while ($end -lt $Path.Length -and -not (Test-LbfSep $Path[$end])) { $end++ }
        if ($end -eq $start) { break }
        $part = $Path.Substring($start, $end - $start)
        if ($part -eq '.') { $start = $end; continue }
        if ($part -eq '..') {
            $r = $dest.Length - 1
            while ($r -ge $volLen -and -not (Test-LbfSep $dest[$r])) { $r-- }
            if ($r -lt $volLen -or $dest.Substring($r + 1) -eq '..') {
                if ($dest.Length -gt $volLen) { $dest += $script:PathSep }
                $dest += '..'
            } else {
                $dest = $dest.Substring(0, $r)
            }
            $start = $end
            continue
        }
        if ($dest.Length -gt (Get-LbfVolumeLength $dest) -and -not (Test-LbfSep $dest[$dest.Length - 1])) { $dest += $script:PathSep }
        $dest += $part
        try { $attr = Get-LbfLinkAttributes $dest } catch { throw (New-LbfLstatError $dest $_) }
        if (-not (Test-LbfSymlink $dest $attr)) {
            $isDir = ($attr -band [IO.FileAttributes]::Directory) -ne 0
            if ($script:OnWindows -and ($attr -band [IO.FileAttributes]::ReparsePoint) -ne 0 -and (Get-LbfLinkInfo $dest).Type -eq 'Junction') { $isDir = $false }
            if (-not $isDir -and $end -lt $Path.Length) { throw (New-LbfError 'not a directory') }
            $start = $end
            continue
        }
        $links++
        if ($links -gt 255) { throw (New-LbfError 'EvalSymlinks: too many links') }
        $link = (Get-LbfLinkInfo $dest).Target
        $Path = $link + $Path.Substring($end)
        $v = Get-LbfVolumeLength $link
        if ($v -gt 0) {
            if ($v -lt $link.Length -and (Test-LbfSep $link[$v])) { $v++ }
            $vol = $link.Substring(0, $v)
            $dest = $vol
            $end = $vol.Length
            $volLen = $v
        } elseif ($link.Length -gt 0 -and (Test-LbfSep $link[0])) {
            $dest = $link.Substring(0, 1)
            $end = 1
            $vol = $dest
            $volLen = 1
        } else {
            $r = $dest.Length - 1
            while ($r -ge $volLen -and -not (Test-LbfSep $dest[$r])) { $r-- }
            if ($r -lt $volLen) { $dest = $vol } else { $dest = $dest.Substring(0, $r) }
            $end = 0
        }
        $start = $end
    }
    $clean = Get-LbfCleanPath $dest
    if ($script:OnWindows) { return Get-LbfStoredCase $clean }
    return $clean
}

# Go's toNorm on Windows: the drive letter upper case, each name as the folder stores it.
function Get-LbfStoredCase {
    param([string]$Path)
    $volLen = Get-LbfVolumeLength $Path
    $vol = $Path.Substring(0, $volLen)
    if ($volLen -eq 2) { $vol = $vol.ToUpperInvariant() }
    $rest = $Path.Substring($volLen)
    if ($rest -eq '' -or $rest -eq '.' -or $rest -eq '\') { return $vol + $rest }
    $parts = $rest.Split([char]'\')
    $built = $vol
    $out = New-Object System.Collections.Generic.List[string]
    for ($i = 0; $i -lt $parts.Length; $i++) {
        $p = $parts[$i]
        if ($p -eq '' -and $i -eq 0) { $built += '\'; continue }
        if ($p -eq '..' -or $p -eq '.') { $out.Add($p); $built = $built.TrimEnd('\') + '\' + $p; continue }
        $parent = $built
        if ($parent -eq $vol) { $parent = $vol + '\' }
        $name = $p
        try {
            $found = @([IO.Directory]::GetFileSystemEntries((Get-LbfIoPath $parent), $p))
            if ($found.Count -gt 0) { $name = [IO.Path]::GetFileName($found[0]) }
        } catch { }
        $out.Add($name)
        if ($built.EndsWith('\', [StringComparison]::Ordinal)) { $built += $name } else { $built += '\' + $name }
    }
    $lead = ''
    if ($rest.StartsWith('\', [StringComparison]::Ordinal)) { $lead = '\' }
    return $vol + $lead + [string]::Join('\', $out.ToArray())
}

# Every link, junction included, resolved: the folder's identity, for finding loops.
function Get-LbfRealPath {
    param([string]$Path)
    $volLen = Get-LbfVolumeLength $Path
    if ($volLen -lt $Path.Length -and (Test-LbfSep $Path[$volLen])) { $volLen++ }
    $dest = $Path.Substring(0, $volLen)
    $todo = New-Object System.Collections.Generic.Queue[string]
    foreach ($p in ($Path.Substring($volLen) -split $script:SepPattern)) { if ($p -ne '') { $todo.Enqueue($p) } }
    $hops = 0
    while ($todo.Count -gt 0) {
        $part = $todo.Dequeue()
        if ($part -eq '.') { continue }
        if ($part -eq '..') { $dest = Get-LbfDir $dest; continue }
        if ($dest.Length -gt 0 -and -not (Test-LbfSep $dest[$dest.Length - 1])) { $dest += $script:PathSep }
        $dest += $part
        $attr = Get-LbfLinkAttributes $dest
        if (($attr -band [IO.FileAttributes]::ReparsePoint) -eq 0) { continue }
        $info = Get-LbfLinkInfo $dest
        if ($info.Type -ne 'SymbolicLink' -and $info.Type -ne 'Junction') { continue }
        if (++$hops -gt 255) { throw (New-LbfError 'EvalSymlinks: too many links') }
        $link = $info.Target
        $rest = New-Object System.Collections.Generic.List[string]
        foreach ($p in ($link -split $script:SepPattern)) { if ($p -ne '') { $rest.Add($p) } }
        while ($todo.Count -gt 0) { $rest.Add($todo.Dequeue()) }
        $v = Get-LbfVolumeLength $link
        if ($v -gt 0) {
            $dest = $link.Substring(0, $v) + $script:PathSep
            $rest.RemoveAt(0)
        } elseif ($link.Length -gt 0 -and (Test-LbfSep $link[0])) {
            $dest = $script:PathSep
        } else {
            $dest = Get-LbfDir $dest
        }
        foreach ($p in $rest) { $todo.Enqueue($p) }
    }
    return Get-LbfCleanPath $dest
}

# ---------- walking a dataset ----------

# Go's datasetFiles: (source, files), or a problem list.
function Get-LbfDatasetFiles {
    param([Parameter(Mandatory)][string]$InputPath)
    $abs = Get-LbfAbsPath $InputPath
    try { $resolved = Resolve-LbfSymlinks $abs } catch { throw (New-LbfError "input ${InputPath}: $(Get-LbfErrorText $_)") }
    try { $parent = Resolve-LbfSymlinks (Get-LbfDir $abs) } catch { throw (New-LbfError "input ${InputPath}: $(Get-LbfErrorText $_)") }
    $base = Get-LbfBase $abs
    $source = Join-LbfPath $parent $base

    $state = @{
        Files = (New-Object System.Collections.Generic.List[hashtable])
        Problems = (New-Object System.Collections.Generic.List[object])
        Pending = (New-Object System.Collections.Generic.List[object])
        CaseInsensitive = $script:CaseInsensitiveFs
    }
    $isDir = $false
    $isFile = $false
    $size = 0L
    $mtime = [DateTime]::MinValue
    try {
        if ([IO.Directory]::Exists((Get-LbfIoPath $resolved))) {
            $isDir = $true
        } else {
            $fi = New-Object IO.FileInfo (Get-LbfIoPath $resolved)
            if (-not $fi.Exists) { throw (New-Object IO.FileNotFoundException "Could not find file '$resolved'.", $resolved) }
            $size = $fi.Length
            $mtime = $fi.LastWriteTimeUtc
            $isFile = -not ($script:OnUnix -and (Test-LbfUnixSpecial @($resolved)).Count -gt 0)
        }
    } catch {
        if ($script:OnWindows) { throw (New-LbfError (Format-LbfOsError 'GetFileAttributesEx' $resolved $_)) }
        throw (New-LbfError (Format-LbfOsError 'stat' $resolved $_))
    }
    if ($isDir) {
        $real = $resolved
        try { $real = Get-LbfRealPath $resolved } catch { }
        Invoke-LbfWalk $state $resolved $base $real (New-Object System.Collections.Generic.List[string])
        Resolve-LbfPending $state
    } elseif ($isFile) {
        $state.Files.Add(@{ Path = $resolved; Rel = $base; Size = $size; ModTime = $mtime; SHA256 = '' })
    } else {
        $state.Problems.Add("${base}: not a regular file")
    }
    $files = Sort-LbfFiles $state.Files
    $problems = New-Object System.Collections.Generic.List[string]
    foreach ($p in $state.Problems) { $problems.Add([string]$p) }
    foreach ($p in (Get-LbfFileProblems $files)) { $problems.Add($p) }
    if ($problems.Count -gt 0) {
        throw (New-LbfProblemList "$InputPath cannot be published as it is, so nothing was uploaded:" $problems)
    }
    return @{ Source = $source; Files = $files }
}

# Sorts by UTF-8 bytes, as Go's strings.Compare does, which differs from UTF-16 order only above U+D7FF.
function Get-LbfUtf8SortKey {
    param([string]$Text)
    if (-not $script:HighChars.IsMatch($Text)) { return $Text }
    $chars = $Text.ToCharArray()
    for ($i = 0; $i -lt $chars.Length; $i++) {
        $c = [int]$chars[$i]
        if ($c -ge 0xD800 -and $c -le 0xDFFF) { $chars[$i] = [char]($c + 0x2000) }
        elseif ($c -ge 0xE000) { $chars[$i] = [char]($c - 0x800) }
    }
    return New-Object string (, $chars)
}

function Sort-LbfFiles {
    param([System.Collections.Generic.List[hashtable]]$Files)
    $arr = $Files.ToArray()
    $keys = New-Object string[] $arr.Length
    $high = $script:HighChars
    for ($i = 0; $i -lt $arr.Length; $i++) {
        $r = $arr[$i].Rel
        if ($high.IsMatch($r)) { $keys[$i] = Get-LbfUtf8SortKey $r } else { $keys[$i] = $r }
    }
    [Array]::Sort([Array]$keys, [Array]$arr, [Collections.IComparer][StringComparer]::Ordinal)
    $out = New-Object System.Collections.Generic.List[hashtable] ($arr.Length)
    $out.AddRange([hashtable[]]$arr)
    return , $out
}

function Get-LbfSortedEntries {
    param([string]$Dir)
    $entries = @(([IO.DirectoryInfo](Get-LbfIoPath $Dir)).GetFileSystemInfos())
    if ($entries.Count -le 1) { return , $entries }
    $keys = New-Object string[] $entries.Count
    for ($i = 0; $i -lt $entries.Count; $i++) { $keys[$i] = Get-LbfUtf8SortKey $entries[$i].Name }
    [Array]::Sort([Array]$keys, [Array]$entries, [Collections.IComparer][StringComparer]::Ordinal)
    return , $entries
}

function Test-LbfSamePath {
    param([string]$A, [string]$B, [bool]$CaseInsensitive)
    if ($CaseInsensitive) { return [string]::Equals($A, $B, [StringComparison]::OrdinalIgnoreCase) }
    return [string]::Equals($A, $B, [StringComparison]::Ordinal)
}

# Go's walkDataset: loops are found by each folder's real path, since .NET cannot compare file identities.
function Invoke-LbfWalk {
    param([hashtable]$State, [string]$Dir, [string]$Rel, [string]$Real, [System.Collections.Generic.List[string]]$Ancestors)
    foreach ($a in $Ancestors) {
        if (Test-LbfSamePath $a $Real $State.CaseInsensitive) {
            $State.Problems.Add("${Rel}: symlink loop back to a folder above it")
            return
        }
    }
    try {
        $entries = Get-LbfSortedEntries $Dir
    } catch {
        $State.Problems.Add("${Rel}: cannot be read ($(Get-LbfOsReason $_))")
        return
    }
    $Ancestors.Add($Real)
    try {
        foreach ($e in $entries) {
            $name = $e.Name
            $path = $Dir + $script:PathSep + $name
            $r = $Rel + '/' + $name
            $attr = $e.Attributes
            if (($attr -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Add-LbfLinkEntry $State $path $r $name $e $Ancestors $Real
                continue
            }
            if (($attr -band [IO.FileAttributes]::Directory) -ne 0) {
                Invoke-LbfWalk $State $path $r ($Real + $script:PathSep + $name) $Ancestors
                continue
            }
            if ($name -ceq '.DS_Store' -or $name.StartsWith('._', [StringComparison]::Ordinal)) { continue }
            $f = @{ Path = $path; Rel = $r; Size = $e.Length; ModTime = $e.LastWriteTimeUtc; SHA256 = '' }
            if ($script:OnUnix -and $e.Length -eq 0) {
                $State.Pending.Add(@{ At = $State.Problems.Count; File = $f })
                $State.Problems.Add($null)
                continue
            }
            $State.Files.Add($f)
        }
    } finally {
        $Ancestors.RemoveAt($Ancestors.Count - 1)
    }
}

# Whether a reparse point is a symlink, its target as stored, and whether it stands for another folder (symlink or junction).
function Get-LbfLinkFacts {
    param([string]$Path)
    try {
        $info = Get-LbfLinkInfo $Path
        $target = ''
        if ($null -ne $info.Target) { $target = [string]$info.Target }
        return @{ IsLink = ($info.Type -eq 'SymbolicLink'); Target = $target; Surrogate = ($info.Type -eq 'SymbolicLink' -or $info.Type -eq 'Junction') }
    } catch {
        return @{ IsLink = $false; Target = ''; Surrogate = $false }
    }
}

# An entry that is a link, a junction or another reparse point such as a OneDrive file: what os.Stat says of it.
function Add-LbfLinkEntry {
    param([hashtable]$State, [string]$Path, [string]$Rel, [string]$Name, $Entry, $Ancestors, [string]$ParentReal)
    $io = Get-LbfIoPath $Path
    $isLink = $false
    $target = ''
    $surrogate = $false
    $isDir = ($Entry.Attributes -band [IO.FileAttributes]::Directory) -ne 0
    if ($isDir -or $script:OnUnix) {
        $facts = Get-LbfLinkFacts $Path
        $isLink = $facts.IsLink; $target = $facts.Target; $surrogate = $facts.Surrogate
    }
    if ($isDir) {
        try {
            [void]([IO.Directory]::EnumerateFileSystemEntries($io).GetEnumerator().MoveNext())
        } catch {
            $err = Get-LbfException $_
            if ($err -is [IO.DirectoryNotFoundException] -or $err -is [IO.FileNotFoundException]) {
                if ($isLink) { $State.Problems.Add("${Rel}: broken symlink to $target") }
                else { $State.Problems.Add("${Rel}: $(Get-LbfOsReason $err)") }
                return
            }
        }
        $real = $ParentReal + $script:PathSep + $Name
        if ($surrogate) { try { $real = Get-LbfRealPath $Path } catch { } }
        Invoke-LbfWalk $State $Path $Rel $real $Ancestors
        return
    }
    if ($script:OnUnix -and $isLink) {
        switch (Get-LbfUnixType $Path) {
            'l' { $State.Problems.Add("${Rel}: broken symlink to $target"); return }
            'd' { Invoke-LbfWalk $State $Path $Rel (Get-LbfRealPath $Path) $Ancestors; return }
            'o' { if (-not (Test-LbfExcluded $Name)) { $State.Problems.Add("${Rel}: not a regular file") }; return }
        }
    }
    try {
        $fs = [IO.File]::Open($io, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]'ReadWrite, Delete')
        try { $size = $fs.Length } finally { $fs.Dispose() }
    } catch {
        $err = Get-LbfException $_
        if (-not $isDir -and -not $script:OnUnix) {
            $facts = Get-LbfLinkFacts $Path
            $isLink = $facts.IsLink; $target = $facts.Target
        }
        if ($isLink) {
            $State.Problems.Add("${Rel}: broken symlink to $target")
        } else {
            $State.Problems.Add("${Rel}: $(Get-LbfOsReason $err)")
        }
        return
    }
    if (Test-LbfExcluded $Name) { return }
    $mtime = $Entry.LastWriteTimeUtc
    $State.Files.Add(@{ Path = $Path; Rel = $Rel; Size = $size; ModTime = $mtime; SHA256 = '' })
}

# Unix only: which paths, links followed, are not regular files (pipes, devices), which .NET cannot tell apart.
function Test-LbfUnixSpecial {
    param([string[]]$Paths)
    $set = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    if (-not $script:OnUnix -or $Paths.Count -eq 0) { return , $set }
    for ($i = 0; $i -lt $Paths.Count; $i += 200) {
        $chunk = $Paths[$i..([Math]::Min($i + 199, $Paths.Count - 1))]
        $out = & find -L @chunk -maxdepth 0 '!' -type f '!' -type d -print 2>$null
        foreach ($l in @($out)) { if ($l) { [void]$set.Add([string]$l) } }
    }
    return , $set
}

# Unix only: l for a broken link, d, f, or o for anything else, links followed.
function Get-LbfUnixType {
    param([string]$Path)
    $out = & find -L $Path -maxdepth 0 '(' -type l -exec echo l ';' ')' -o '(' -type d -exec echo d ';' ')' -o '(' -type f -exec echo f ';' ')' -o -exec echo o ';' 2>$null
    $t = [string](@($out) | Select-Object -First 1)
    if ($t -eq '') { return 'l' }
    return $t
}

function Resolve-LbfPending {
    param([hashtable]$State)
    if ($State.Pending.Count -eq 0) { return }
    $paths = New-Object System.Collections.Generic.List[string]
    foreach ($p in $State.Pending) { $paths.Add($p.File.Path) }
    $special = Test-LbfUnixSpecial $paths.ToArray()
    foreach ($p in $State.Pending) {
        if ($special.Contains($p.File.Path)) { $State.Problems[$p.At] = "$($p.File.Rel): not a regular file" }
        else { $State.Files.Add($p.File) }
    }
    for ($i = $State.Problems.Count - 1; $i -ge 0; $i--) { if ($null -eq $State.Problems[$i]) { $State.Problems.RemoveAt($i) } }
}

# ---------- names ----------

# Go's nameKey: NFC, then Unicode case folding.
function Get-LbfNameKey {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    $n = $Text
    try { $n = $Text.Normalize([Text.NormalizationForm]::FormC) } catch { $n = $Text }
    $folded = $n.ToUpperInvariant().ToLowerInvariant()
    if (-not $script:FoldExceptionPattern.IsMatch($n)) { return $folded }
    $sb = New-Object System.Text.StringBuilder
    for ($i = 0; $i -lt $n.Length; $i++) {
        $c = $n[$i]
        if ($script:FoldExceptions.ContainsKey($c)) { [void]$sb.Append($script:FoldExceptions[$c]) }
        elseif ($folded.Length -eq $n.Length) { [void]$sb.Append($folded[$i]) }
        else { [void]$sb.Append([string]$c) }
    }
    return $sb.ToString()
}

function Test-LbfClashesWithCrate {
    param([string]$Rel, [bool]$Fold)
    $top = $Rel.Split([char]'/')[0]
    return $top -ceq $script:CrateName -or ($Fold -and (Get-LbfNameKey $top) -ceq (Get-LbfNameKey $script:CrateName))
}

# Go's encodingProblem: Azure names are UTF-8 and Linux allows 255 bytes per name.
function Get-LbfEncodingProblem {
    param([string]$Rel)
    if ($script:HighChars.IsMatch($Rel) -and $script:BadUtf16.IsMatch($Rel)) { return 'is not valid UTF-8, which Azure blob names must be' }
    if ($Rel.Length -le 85) { return '' }
    foreach ($seg in $Rel.Split([char]'/')) {
        if ($seg.Length -gt 85) {
            $n = $script:Utf8NoBom.GetByteCount($seg)
            if ($n -gt 255) { return "$(Format-LbfQuote $seg) is $n bytes long, but Linux allows at most 255 per name" }
        }
    }
    return ''
}

# Go's nameProblem.
function Get-LbfNameProblem {
    param([string]$Rel)
    if (-not $script:NameSuspect.IsMatch($Rel)) { return '' }
    foreach ($seg in $Rel.Split([char]'/')) {
        $stem = $seg
        $dot = $seg.IndexOf([char]'.')
        if ($dot -ge 0) { $stem = $seg.Substring(0, $dot) }
        if ($seg.Contains('\')) { return 'contains \, which Azure turns into a folder separator' }
        if ($seg.IndexOfAny([char[]]':*?"<>|') -ge 0) { return 'contains one of : * ? " < > |, which Windows cannot store' }
        if ($script:ControlChars.IsMatch($seg)) { return 'contains a control character' }
        if ($seg.EndsWith('.', [StringComparison]::Ordinal) -or $seg.EndsWith(' ', [StringComparison]::Ordinal)) { return "$(Format-LbfQuote $seg) ends in a dot or space, which Windows drops" }
        if ($script:WindowsReserved.IsMatch($stem.TrimEnd(' '))) { return "$(Format-LbfQuote $seg) is a reserved name on Windows" }
    }
    return ''
}

# Go's checkFiles.
function Get-LbfFileProblems {
    param([System.Collections.Generic.List[hashtable]]$Files)
    $problems = New-Object System.Collections.Generic.List[string]
    if ($Files.Count -gt 0 -and (Test-LbfClashesWithCrate $Files[0].Rel $true)) {
        $top = $Files[0].Rel.Split([char]'/')[0]
        $problems.Add("${top}: would be replaced by the crate lbf writes; rename it or publish its folder")
    }
    $rels = New-Object string[] $Files.Count
    $share = [IO.FileShare]'ReadWrite, Delete'
    for ($i = 0; $i -lt $Files.Count; $i++) {
        $f = $Files[$i]
        $rel = $f.Rel
        $rels[$i] = $rel
        if ($rel.Length -gt 85 -or $script:NameSuspect.IsMatch($rel) -or $script:HighChars.IsMatch($rel)) {
            $p = Get-LbfEncodingProblem $rel
            if ($p -eq '') { $p = Get-LbfNameProblem $rel }
            if ($p -ne '') { $problems.Add("${rel}: $p") }
        }
        try {
            $fs = [IO.File]::Open((Get-LbfIoPath $f.Path), [IO.FileMode]::Open, [IO.FileAccess]::Read, $share)
            $fs.Dispose()
        } catch {
            $problems.Add("$($f.Rel): cannot be read ($(Get-LbfOsReason $_))")
        }
    }
    foreach ($p in (Get-LbfSameNameProblems $rels 'macOS and Windows')) { $problems.Add($p) }
    return , $problems
}

# Go's sameNameProblems; folders whose every ancestor was already seen without a clash are skipped.
function Get-LbfSameNameProblems {
    param([string[]]$Rels, [string]$Where)
    $problems = New-Object System.Collections.Generic.List[string]
    $seen = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    $reported = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $clean = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $keys = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    foreach ($rel in $Rels) {
        $slash = $rel.LastIndexOf([char]'/')
        $from = 0
        if ($slash -gt 0 -and $clean.Contains($rel.Substring(0, $slash))) { $from = $slash }
        $i = $from
        while ($true) {
            if ($i -lt $rel.Length -and $rel[$i] -ne '/') {
                $i = $rel.IndexOf([char]'/', $i)
                if ($i -lt 0) { $i = $rel.Length }
            }
            $path = $rel.Substring(0, $i)
            $key = $null
            if (-not $keys.TryGetValue($path, [ref]$key)) {
                $key = Get-LbfNameKey $path
                if ($i -lt $rel.Length) { $keys[$path] = $key }
            }
            $other = $null
            if (-not $seen.TryGetValue($key, [ref]$other)) {
                $seen[$key] = $path
            } elseif ($other -cne $path) {
                $how = 'differ only in case'
                $a = $other; $b = $path
                try { $a = $other.Normalize([Text.NormalizationForm]::FormC); $b = $path.Normalize([Text.NormalizationForm]::FormC) } catch { }
                if ($a -ceq $b) { $how = 'are the same name in different Unicode forms' }
                $p = "$other and $path $how, so $Where cannot hold both"
                if ($reported.Add($p)) { $problems.Add($p) }
                break
            }
            if ($i -lt $rel.Length) { [void]$clean.Add($path) }
            if ($i -ge $rel.Length) { break }
            $i++
        }
    }
    return , $problems
}

# Azure's limits on a blob name, which is <id>/<rel>.
function Get-LbfBlobNameProblems {
    param([string]$Id, [System.Collections.Generic.List[hashtable]]$Files)
    $problems = New-Object System.Collections.Generic.List[string]
    foreach ($f in $Files) {
        $name = $Id + '/' + $f.Rel
        $n = $name.Length
        if ($n -gt 340) { $n = $script:Utf8NoBom.GetByteCount($name) }
        if ($n -gt 1024) {
            $problems.Add("$($f.Rel): too long for Azure, which allows 1024 characters including the dataset ID (this is $n)")
        } elseif ($name.Length -gt 254 -and $name.Split([char]'/').Length -gt 254) {
            $problems.Add("$($f.Rel): nested too deeply for Azure, which allows 254 path segments including the dataset ID")
        }
    }
    return , $problems
}

function New-LbfProblemList {
    param([string]$Heading, $Problems)
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append($Heading)
    $n = [Math]::Min($Problems.Count, 10)
    for ($i = 0; $i -lt $n; $i++) { [void]$sb.Append("`n  " + $Problems[$i]) }
    if ($Problems.Count -gt 10) { [void]$sb.Append("`n  ...and $($Problems.Count - 10) more") }
    return New-LbfError $sb.ToString()
}
