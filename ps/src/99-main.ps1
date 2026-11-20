# The arguments as an exe would receive them: PowerShell turns 0012 into 12, a,b into an array, and drops --.
function Get-LbfTypedArgs {
    param($Invocation, [object[]]$Raw)
    if ($null -eq $Invocation -or [string]::IsNullOrEmpty($Invocation.Line)) { return $null }
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput($Invocation.Line, [ref]$tokens, [ref]$errors)
    $commands = $ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true)
    $command = $null
    foreach ($c in $commands) {
        if ($c.Extent.StartLineNumber -eq 1 -and $c.Extent.StartColumnNumber -eq $Invocation.OffsetInLine) { $command = $c; break }
    }
    if ($null -eq $command) { return $null }
    $elements = @($command.CommandElements)
    $out = New-Object System.Collections.Generic.List[string]
    $r = 0
    $sawEnd = $false
    for ($i = 1; $i -lt $elements.Count; $i++) {
        $e = $elements[$i]
        if ($e -is [System.Management.Automation.Language.CommandParameterAst]) {
            if (-not $sawEnd -and $e.Extent.Text -eq '--' -and $null -eq $e.Argument) { $sawEnd = $true; $out.Add('--'); continue }
            if ($null -ne $e.Argument) {
                if ($r + 1 -ge $Raw.Count) { return $null }
                $value = Get-LbfElementText $e.Argument
                if ($null -eq $value) { $value = ConvertTo-LbfArgText $Raw[$r + 1] }
                $out.Add([string]$Raw[$r] + $value)
                $r += 2
                continue
            }
        }
        if ($r -ge $Raw.Count) { return $null }
        if ($Raw[$r] -is [string]) { $out.Add($Raw[$r]) }
        else {
            $t = Get-LbfElementText $e
            if ($null -eq $t) { $t = ConvertTo-LbfArgText $Raw[$r] }
            $out.Add($t)
        }
        $r++
    }
    if ($r -ne $Raw.Count) { return $null }
    return , $out.ToArray()
}

function Get-LbfElementText {
    param($Element)
    if ($Element -is [System.Management.Automation.Language.StringConstantExpressionAst]) { return $Element.Value }
    if ($Element -is [System.Management.Automation.Language.ConstantExpressionAst]) { return $Element.Extent.Text }
    if ($Element -is [System.Management.Automation.Language.ArrayLiteralAst]) {
        $parts = New-Object System.Collections.Generic.List[string]
        foreach ($e in $Element.Elements) {
            $t = Get-LbfElementText $e
            if ($null -eq $t) { return $null }
            $parts.Add($t)
        }
        return $parts -join ','
    }
    return $null
}

function ConvertTo-LbfArgText {
    param($Value)
    if ($null -eq $Value) { return '' }
    if ($Value -is [string]) { return $Value }
    if ($Value -is [System.Collections.IEnumerable]) { return (@($Value | ForEach-Object { [string]$_ }) -join ',') }
    return [string]$Value
}

function ConvertTo-LbfArgs {
    param([object[]]$Raw, $Invocation)
    if ($null -eq $Raw) { $Raw = @() }
    $typed = $null
    try { $typed = Get-LbfTypedArgs $Invocation $Raw } catch { $typed = $null }
    if ($null -ne $typed) { return , $typed }
    $out = New-Object System.Collections.Generic.List[string]
    foreach ($v in $Raw) { $out.Add((ConvertTo-LbfArgText $v)) }
    return , $out.ToArray()
}

function Invoke-LbfMain {
    param([object[]]$Raw, $Invocation)
    $exit = 0
    $savedOut = $null
    try { $savedOut = [Console]::OutputEncoding; [Console]::OutputEncoding = $script:Utf8NoBom } catch { $savedOut = $null }
    try {
        $arguments = ConvertTo-LbfArgs $Raw $Invocation
        $update = $null
        try { $update = Start-LbfUpdateCheck $arguments } catch { $update = $null }
        try {
            Invoke-Lbf $arguments
        } catch {
            $msg = [regex]::Replace((Get-LbfErrorText $_), 'sig=[^&\s"]+', 'sig=REDACTED')
            Write-LbfLog "error: $msg`n"
            if ($env:LBF_TEST_TRACE) { Write-LbfErr ($_.ScriptStackTrace + "`n") }
            $exit = Get-LbfExitCode $_
        }
        if ($null -ne $update) { try { Complete-LbfUpdateCheck $update } catch { } }
    } finally {
        Set-LbfStatus ''
        if ($null -ne $savedOut) { try { [Console]::OutputEncoding = $savedOut } catch { } }
    }
    exit $exit
}

Invoke-LbfMain -Raw $args -Invocation $MyInvocation
