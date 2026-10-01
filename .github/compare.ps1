param([string]$Src, [string]$Got)

$fail = $false
$expected = Get-ChildItem -Recurse -File -LiteralPath $Src | Where-Object { $_.Name -ne '.DS_Store' }
foreach ($f in $expected) {
    $rel = $f.FullName.Substring($Src.Length).TrimStart('\')
    $other = Join-Path $Got $rel
    if (-not (Test-Path -LiteralPath $other)) { "MISSING   $rel"; $fail = $true; continue }
    if ((Get-FileHash -LiteralPath $f.FullName).Hash -ne (Get-FileHash -LiteralPath $other).Hash) {
        "DIFFERENT $rel"; $fail = $true
    } else {
        "OK        $rel"
    }
}
$actual = @(Get-ChildItem -Recurse -File -LiteralPath $Got).Count
if ($actual -ne $expected.Count) { "File count: expected $($expected.Count), got $actual"; $fail = $true }
if ($fail) { exit 1 }
"All $($expected.Count) files match"
