$script:ProgressRedrawMs = 100
$script:ProgressReportSeconds = 30
$script:ProgressRateSeconds = 5
$script:ProgressMaxBar = 20
$script:ProgressMinName = 12

# Go's humanBytes; %.1f rounds the exact quotient half to even, as Go does.
function Format-LbfBytes {
    param([Parameter(Mandatory)][long]$N)
    if ($N -lt 1024) { return "$N B" }
    $div = [decimal]1024
    $exp = 0
    for ($m = [long][Math]::Floor($N / 1024); $m -ge 1024; $m = [long][Math]::Floor($m / 1024)) {
        $div *= 1024
        $exp++
    }
    $scaled = [decimal]$N * 10
    $q = [decimal]::Floor($scaled / $div)
    $r = $scaled - $q * $div
    if (2 * $r -gt $div -or (2 * $r -eq $div -and ($q % 2) -eq 1)) { $q += 1 }
    $whole = [decimal]::Floor($q / 10)
    $tenth = $q - $whole * 10
    return '{0}.{1} {2}iB' -f $whole.ToString($script:Invariant), $tenth.ToString($script:Invariant), 'KMGTPE'[$exp]
}

# Go's Duration.String for a whole number of milliseconds, as logDone prints it.
function Format-LbfGoDuration {
    param([Parameter(Mandatory)][long]$Milliseconds)
    $ms = $Milliseconds
    $neg = ''
    if ($ms -lt 0) { $neg = '-'; $ms = -$ms }
    if ($ms -eq 0) { return '0s' }
    if ($ms -lt 1000) { return "$neg${ms}ms" }
    $s = [long][Math]::Floor($ms / 1000)
    $frac = $ms % 1000
    $out = $neg
    $h = [long][Math]::Floor($s / 3600)
    if ($h -gt 0) { $out += "${h}h" }
    if ($s -ge 60) { $out += "$([long][Math]::Floor($s / 60) % 60)m" }
    $out += [string]($s % 60)
    if ($frac -gt 0) { $out += '.' + $frac.ToString('000').TrimEnd('0') }
    return $out + 's'
}

# Go's shortDuration(time.Duration(seconds * time.Second)).
function Format-LbfShortDuration {
    param([Parameter(Mandatory)][double]$Seconds)
    $ns = [Math]::Truncate($Seconds * 1e9)
    $s = [long][Math]::Round($ns / 1e9, [MidpointRounding]::AwayFromZero)
    if ($s -ge 3600) { return '{0}h{1:00}m' -f [long][Math]::Floor($s / 3600), ([long][Math]::Floor($s / 60) % 60) }
    if ($s -ge 60) { return '{0}m{1:00}s' -f [long][Math]::Floor($s / 60), ($s % 60) }
    return "${s}s"
}

$script:PrintableAscii = New-Object System.Text.RegularExpressions.Regex ('\A[\x20-\x7E]*\z')

# Go's printable, skipping the per-character work for plain ASCII names.
function Get-LbfShownName {
    param([AllowEmptyString()][string]$Name)
    if ($script:PrintableAscii.IsMatch($Name)) { return $Name }
    return Format-LbfPrintable $Name
}

function Get-LbfPercent {
    param([long]$N, [long]$Total)
    if ($Total -le 0) { return 100 }
    return [long][Math]::Truncate([decimal]100 * $N / $Total)
}

function Format-LbfCounts {
    param([long]$N, [long]$Total)
    return '{0,3}%  {1,10} / {2,-10}' -f (Get-LbfPercent $N $Total), (Format-LbfBytes $N), (Format-LbfBytes $Total)
}

function Format-LbfBar {
    param([long]$N, [long]$Total, [int]$Width)
    $Width = [Math]::Min($Width, $script:ProgressMaxBar)
    if ($Width -lt 4) { return '' }
    $filled = $Width
    if ($Total -gt 0) { $filled = [int][Math]::Truncate([double]$Width * [double]$N / [double]$Total) }
    $s = '=' * $filled
    if ($filled -lt $Width) { $s += '>' + (' ' * ($Width - $filled - 1)) }
    return '[' + $s + ']'
}

# Redraws in place on a terminal; elsewhere prints a line per finished file and a periodic summary.
function New-LbfProgress {
    param([int]$Files, [long]$Total, $Live = $null, [int]$Width = 0, [int]$Height = 0, [switch]$Discard)
    if ($null -eq $Live) { $Live = Test-LbfStderrLive }
    $now = [DateTime]::UtcNow
    $samples = New-Object System.Collections.Generic.List[object]
    $samples.Add(@($now, [long]0))
    return @{
        Live = [bool]$Live; Files = $Files; Total = $Total; Width = $Width; Height = $Height; Discard = [bool]$Discard
        Done = 0; Settled = [long]0
        Active = (New-Object System.Collections.Generic.List[hashtable])
        Pending = (New-Object System.Collections.Generic.List[string])
        Samples = $samples
        Frame = (New-Object System.Collections.Generic.List[int])
        LastReport = $now; LastDraw = $now
    }
}

function Start-LbfProgressBar {
    param([Parameter(Mandatory)][hashtable]$Progress, [string]$Name, [long]$Size)
    $bar = @{ Name = $Name; Size = $Size; N = [long]0 }
    $Progress.Active.Add($bar)
    return $bar
}

function Get-LbfBarTransferred {
    param([hashtable]$Bar)
    return [Math]::Min([Math]::Max([long]$Bar.N, [long]0), [long]$Bar.Size)
}

function Stop-LbfProgressBar {
    param([Parameter(Mandatory)][hashtable]$Progress, [Parameter(Mandatory)][hashtable]$Bar, [bool]$Ok)
    [void]$Progress.Active.Remove($Bar)
    if ($Ok) {
        $Progress.Done++
        $Progress.Settled += $Bar.Size
        $Progress.Pending.Add("  [$($Progress.Done)/$($Progress.Files)] $(Get-LbfShownName $Bar.Name) ($(Format-LbfBytes $Bar.Size))")
    }
}

# Draws at most every redraw interval; transfers call it from their wait loop.
function Update-LbfProgress {
    param([Parameter(Mandatory)][hashtable]$Progress)
    $now = [DateTime]::UtcNow
    if (($now - $Progress.LastDraw).TotalMilliseconds -lt $script:ProgressRedrawMs) { return }
    $Progress.LastDraw = $now
    $text = Format-LbfProgressFrame $Progress $now $false
    if ($text -ne '' -and -not $Progress.Discard) { Write-LbfErr $text }
}

function Close-LbfProgress {
    param([Parameter(Mandatory)][hashtable]$Progress)
    $text = Format-LbfProgressFrame $Progress ([DateTime]::UtcNow) $true
    if ($text -ne '' -and -not $Progress.Discard) { Write-LbfErr $text }
}

function Get-LbfProgressTransferred {
    param([hashtable]$Progress)
    $n = [long]$Progress.Settled
    foreach ($b in $Progress.Active) { $n += Get-LbfBarTransferred $b }
    return $n
}

function Get-LbfProgressRate {
    param([hashtable]$Progress, [datetime]$Now, [long]$N)
    $s = $Progress.Samples
    $s.Add(@($Now, $N))
    while ($s.Count -gt 2 -and ($Now - $s[1][0]).TotalSeconds -ge $script:ProgressRateSeconds) { $s.RemoveAt(0) }
    $first = $s[0]
    $d = ($Now - $first[0]).TotalSeconds
    if ($d -gt 0 -and $N -gt $first[1]) { return ($N - $first[1]) / $d }
    return [double]0
}

function Get-LbfProgressSummary {
    param([hashtable]$Progress, [datetime]$Now)
    $n = Get-LbfProgressTransferred $Progress
    $s = "$(Format-LbfBytes $n) / $(Format-LbfBytes $Progress.Total)"
    $r = Get-LbfProgressRate $Progress $Now $n
    if ($r -gt 0) { $s += ", $(Format-LbfBytes ([long][Math]::Truncate($r)))/s, ETA $(Format-LbfShortDuration (($Progress.Total - $n) / $r))" }
    return $s
}

function Get-LbfProgressSize {
    param([hashtable]$Progress)
    if ($Progress.Width -gt 0 -and $Progress.Height -gt 0) { return , @($Progress.Width, $Progress.Height) }
    return Get-LbfTermSize
}

# Go's progress.render.
function Format-LbfProgressFrame {
    param([Parameter(Mandatory)][hashtable]$Progress, [Parameter(Mandatory)][datetime]$Now, [bool]$Final)
    $p = $Progress
    if (-not $p.Live) {
        $s = ''
        foreach ($l in $p.Pending) { $s += $l + "`n" }
        $p.Pending.Clear()
        if (-not $Final -and ($Now - $p.LastReport).TotalSeconds -ge $script:ProgressReportSeconds) {
            $p.LastReport = $Now
            $s += "  ... $($p.Done)/$($p.Files) files, $(Get-LbfProgressSummary $p $Now)`n"
        }
        return $s
    }

    $size = Get-LbfProgressSize $p
    $width = [int]$size[0]
    $height = [int]$size[1]
    $esc = [char]27
    $b = New-Object System.Text.StringBuilder
    $up = 0
    foreach ($w in $p.Frame) { $up += [Math]::Max(1, [int][Math]::Floor(($w + $width - 1) / $width)) }
    if ($up -gt 0) { [void]$b.Append("$esc[${up}A`r") }
    foreach ($line in $p.Pending) { [void]$b.Append($line + "$esc[K`n") }
    $p.Pending.Clear()
    $p.Frame.Clear()
    if ($Final) {
        [void]$b.Append("$esc[J")
        return $b.ToString()
    }

    $width--
    $shownCount = [Math]::Min($p.Active.Count, [Math]::Max($height - 3, 0))
    $barWidth = $width - 2 - (Format-LbfCounts 0 0).Length - 2 - $script:ProgressMinName - 3
    $left = {
        param([long]$n, [long]$total)
        $s = '  '
        $fb = Format-LbfBar $n $total $barWidth
        if ($fb -ne '') { $s += $fb + ' ' }
        return $s + (Format-LbfCounts $n $total) + '  '
    }
    $line = {
        param([string]$s)
        $s = Format-LbfFit $s $width
        [void]$b.Append($s + "$esc[K`n")
        $p.Frame.Add((Get-LbfUtf8Length $s))
    }
    for ($i = 0; $i -lt $shownCount; $i++) {
        $f = $p.Active[$i]
        $l = & $left (Get-LbfBarTransferred $f) $f.Size
        & $line ($l + (Format-LbfClipLeft (Get-LbfShownName $f.Name) ($width - (Get-LbfUtf8Length $l))))
    }

    $n = Get-LbfProgressTransferred $p
    $l = & $left $n $p.Total
    $parts = New-Object System.Collections.Generic.List[string]
    $parts.Add("$($p.Done)/$($p.Files) files")
    $r = Get-LbfProgressRate $p $Now $n
    if ($r -gt 0) {
        $parts.Add('ETA ' + (Format-LbfShortDuration (($p.Total - $n) / $r)))
        $parts.Add((Format-LbfBytes ([long][Math]::Truncate($r))) + '/s')
    }
    $hidden = $p.Active.Count - $shownCount
    if ($hidden -gt 0) { $parts.Add("(+$hidden more)") }
    $trailer = $parts[0]
    for ($i = 1; $i -lt $parts.Count; $i++) {
        if ((Get-LbfUtf8Length $l) + (Get-LbfUtf8Length $trailer) + 2 + (Get-LbfUtf8Length $parts[$i]) -gt $width) { break }
        $trailer += '  ' + $parts[$i]
    }
    & $line ($l + $trailer)
    [void]$b.Append("$esc[J")
    return $b.ToString()
}
