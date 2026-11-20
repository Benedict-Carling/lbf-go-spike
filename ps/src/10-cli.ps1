$script:Usage = @'
lbf - land and retrieve datasets in the Azure bronze layer

Usage:
  lbf publish <path>    upload a dataset (alias: upload)
  lbf fetch <id>        download a dataset (alias: download)
  lbf new-id            print a dataset ID to publish under later
  lbf browse            search and look through every dataset in your browser
  lbf check             prove a credential can publish or fetch, without changing anything
  lbf mint-sas          write a pre-minted credential file
  lbf login             sign in with the browser and remember it
  lbf logout            sign out of lbf and delete its saved tokens
  lbf upgrade           update lbf to the latest release
  lbf version

Run 'lbf <command> --help' for its options.

'@ -replace "`r`n", "`n"

$script:ProvenanceHelp = @'
--derived-from and --instrument are given together or not at all. --provenance is a JSON file:
{"name", "description", "derived_from": "<id>", "instruments": [{"name", "version", "url"}], "properties": {"name": "value"}};
--property, --name and --description add to it, and anything given twice is refused.
--profile is a directory holding a JSON Schema profile.json; publish validates against it, and lbf's
bronze profile, before uploading anything.

'@ -replace "`r`n", "`n"

$script:ImperialTenant = '2b897507-ee8c-4575-830b-4f8267c3d307'
$script:ExitIDTaken = 3
$script:SasLifetime = [TimeSpan]::FromHours(144)

# Writes text to stdout as fmt.Print would, one pipeline string per line so PowerShell adds no blank line.
function Write-LbfOut {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Text)
    if ($Text.EndsWith("`n")) { $Text = $Text.Substring(0, $Text.Length - 1) }
    # A crate can be tens of megabytes; one string prints the same bytes far faster than a line each.
    if ($Text.Length -gt 65536) { Write-Output $Text; return }
    foreach ($l in ($Text -split "`n", 0, 'SimpleMatch')) { Write-Output $l }
}

function New-LbfOptions {
    return @{
        tag = ''; account = ''; tenant = ''; container = ''; sasEnv = ''
        out = ''; mode = ''; port = ''; profile = ''; id = ''
        dryRun = $false; json = $false
        prov = @{
            file = ''; derivedFrom = ''; properties = ''; name = ''; description = ''
            instruments = (New-Object System.Collections.Generic.List[object])
            props = (New-Object System.Collections.Generic.List[string])
        }
    }
}

# A command's flags as Go's flag.FlagSet holds them, in the order its usage lists them.
function New-LbfCommand {
    param([Parameter(Mandatory)][string]$Name, [Parameter(Mandatory)][hashtable]$Options)
    $c = @{ name = $Name; synopsis = ''; notes = ''; arg = ''; argDesc = ''; flags = (New-Object System.Collections.Generic.List[hashtable]) }
    $o = $Options
    $str = {
        param($key, $name, $arg, $def, $desc, [hashtable]$into)
        if ($null -eq $into) { $into = $o }
        $into[$key] = $def
        $c.flags.Add(@{ name = $name; arg = $arg; usage = $desc; def = $def; kind = 'string'; key = $key; into = $into; str = $true; required = $false })
    }
    $bool = {
        param($key, $name, $desc)
        $o[$key] = $false
        $c.flags.Add(@{ name = $name; arg = ''; usage = $desc; def = 'false'; kind = 'bool'; key = $key; into = $o; str = $false; required = $false })
    }
    $tenant = { & $str 'tenant' 'tenant' 'ID' '' 'Entra tenant to sign in to (default Imperial College London)' }
    $account = {
        & $str 'tag' 'tag' 'KEY=VALUE' 'tag=storage' 'storage account tag; the test account is tag=storage-test'
        & $str 'account' 'account' 'NAME' '' 'which tagged storage account to use when several are tagged (asked for in a terminal)'
        & $tenant
    }
    $storage = {
        & $str 'container' 'container' 'NAME' 'bronze' 'blob container'
        & $account
    }
    $jsonFlag = { param($desc) & $bool 'json' 'json' $desc }
    $sasEnv = { & $str 'sasEnv' 'sas-env' 'FILE' '' "pre-minted credential file from 'lbf mint-sas'" }

    switch -CaseSensitive ($Name) {
        { $_ -eq 'publish' -or $_ -eq 'upload' } {
            $c.synopsis = "$Name <path> [options]"
            $c.arg = '<path>'; $c.argDesc = 'dataset directory to upload'
            $c.notes = $script:ProvenanceHelp
            & $str 'derivedFrom' 'derived-from' 'ID' '' 'the dataset this one was derived from' $o.prov
            $c.flags.Add(@{ name = 'instrument'; arg = 'name=NAME,version=VERSION,url=URL'; usage = 'what produced it; repeat for several'; def = ''; kind = 'func'; into = $o.prov; str = $false; required = $false })
            $c.flags.Add(@{ name = 'property'; arg = 'NAME=VALUE'; usage = 'a property of the dataset; repeat for several'; def = ''; kind = 'func'; into = $o.prov; str = $false; required = $false })
            & $str 'properties' 'properties' 'FILE' '' 'JSON object of property names and values, e.g. {"sample_id": "SAM-0001"}' $o.prov
            & $str 'name' 'name' 'TEXT' '' "what people call the dataset (default the folder's name)" $o.prov
            & $str 'description' 'description' 'TEXT' '' 'what the dataset holds (default one lbf writes)' $o.prov
            & $str 'file' 'provenance' 'FILE' '' "the dataset's name, description, parent, instruments and properties as one JSON file" $o.prov
            & $str 'profile' 'profile' 'DIR' '' 'directory containing profile.json'
            & $str 'id' 'id' 'ID' '' "publish under this ID from 'lbf new-id'; run again with the same ID to finish a failed publish"
            $c.notes += "Exits with 3 when the ID already holds different files or provenance, which retrying cannot fix; use a new ID.`n"
            & $bool 'dryRun' 'dry-run' 'validate and print the crate without signing in or uploading'
            & $jsonFlag 'print {"id", "url"} as JSON instead of text, or instead of the crate with --dry-run (its url names the placeholder account dryrun)'
            & $storage
            & $sasEnv
        }
        { $_ -eq 'fetch' -or $_ -eq 'download' } {
            $c.synopsis = "$Name <id> [options]"
            $c.arg = '<id>'; $c.argDesc = "dataset ID, as printed by 'lbf publish'"
            & $str 'out' 'out' 'DIR' '.' 'output directory'
            & $jsonFlag 'print {"id", "url", "path", "data_path", "provenance"} as JSON instead of the path; data_path is the uploaded folder inside path, and provenance what its crate states, as in a --provenance file'
            & $storage
            & $sasEnv
        }
        'browse' {
            $c.synopsis = 'browse [options]'
            $c.notes = "Reads every dataset's crate, caching each one since crates never change, and serves them on this computer only.`n"
            & $str 'container' 'container' 'NAMES' '' 'comma-separated containers to read (default every container you can list)'
            & $str 'port' 'port' 'PORT' '0' 'local port to serve on (0 picks a free one)'
            & $account
        }
        'new-id' {
            $c.synopsis = 'new-id'
            $c.notes = "Prints a new dataset ID without signing in. Pass it to 'lbf publish --id' once the data is ready.`n"
        }
        'check' {
            $c.synopsis = 'check --mode upload|download [options]'
            $c.notes = "Run before a long job, so a missing or expiring credential is found before the work, not after.`n"
            & $str 'mode' 'mode' 'upload|download' '' 'what the credential must be able to do'
            $c.flags[$c.flags.Count - 1].required = $true
            & $storage
            & $sasEnv
        }
        'mint-sas' {
            $c.synopsis = 'mint-sas --mode upload|download [options]'
            & $str 'mode' 'mode' 'upload|download' '' 'what the credential may do'
            $c.flags[$c.flags.Count - 1].required = $true
            & $str 'out' 'out' 'FILE' 'azure_sas.env' 'output file'
            & $storage
        }
        'login' {
            $c.synopsis = 'login [options]'
            & $tenant
        }
        'logout' {
            $c.synopsis = 'logout [options]'
            $c.notes = "lbf also signs in through 'az login'; logout says if that still signs you in, and 'az logout' ends it.`n"
            & $tenant
        }
        'upgrade' {
            $c.synopsis = 'upgrade'
            $c.notes = "lbf also warns after any command when a newer release exists; set LBF_NO_UPDATE_CHECK=1 to stop it checking.`n"
        }
        default {
            throw (New-LbfError ("unknown command $(Format-LbfQuote $Name)`n`n" + $script:Usage))
        }
    }
    return $c
}

function Get-LbfFlag {
    param([hashtable]$Command, [string]$Name)
    foreach ($f in $Command.flags) { if ($f.name -ceq $Name) { return $f } }
    return $null
}

function Get-LbfFlagValue {
    param([hashtable]$Flag)
    switch ($Flag.kind) {
        'string' { return [string]$Flag.into[$Flag.key] }
        'bool' { if ($Flag.into[$Flag.key]) { return 'true' } else { return 'false' } }
        default { return '' }
    }
}

function New-LbfHelpRequest {
    $e = New-LbfError 'flag: help requested'
    $e.Data['lbfHelp'] = $true
    return $e
}

function Test-LbfHelpRequest {
    param($ErrorOrException)
    return (Get-LbfException $ErrorOrException).Data.Contains('lbfHelp')
}

# Go's FlagSet.parseOne over the whole list; returns the arguments left after the flags.
function Invoke-LbfFlagParse {
    param([hashtable]$Command, [string[]]$Arguments, [hashtable]$Set)
    $rest = New-Object System.Collections.Generic.List[string]
    foreach ($a in $Arguments) { $rest.Add($a) }
    while ($rest.Count -gt 0) {
        $s = $rest[0]
        if ($s.Length -lt 2 -or $s[0] -ne '-') { break }
        $minuses = 1
        if ($s[1] -eq '-') {
            $minuses = 2
            if ($s.Length -eq 2) { $rest.RemoveAt(0); break }
        }
        $name = $s.Substring($minuses)
        if ($name.Length -eq 0 -or $name[0] -eq '-' -or $name[0] -eq '=') { throw (New-LbfError "bad flag syntax: $s") }
        $rest.RemoveAt(0)
        $hasValue = $false
        $value = ''
        for ($i = 1; $i -lt $name.Length; $i++) {
            if ($name[$i] -eq '=') {
                $value = $name.Substring($i + 1)
                $hasValue = $true
                $name = $name.Substring(0, $i)
                break
            }
        }
        $flag = Get-LbfFlag $Command $name
        if ($null -eq $flag) {
            if ($name -ceq 'help' -or $name -ceq 'h') { throw (New-LbfHelpRequest) }
            throw (New-LbfError "flag provided but not defined: -$name")
        }
        if ($flag.kind -eq 'bool') {
            if ($hasValue) {
                $b = ConvertFrom-LbfGoBool $value
                if ($null -eq $b) { throw (New-LbfError "invalid boolean value $(Format-LbfQuote $value) for -${name}: parse error") }
                $flag.into[$flag.key] = $b
            } else {
                $flag.into[$flag.key] = $true
            }
        } else {
            if (-not $hasValue -and $rest.Count -gt 0) {
                $hasValue = $true
                $value = $rest[0]
                $rest.RemoveAt(0)
            }
            if (-not $hasValue) { throw (New-LbfError "flag needs an argument: -$name") }
            if ($flag.kind -eq 'func') {
                try {
                    if ($flag.name -ceq 'instrument') { $flag.into.instruments.Add((ConvertTo-LbfInstrument $value)) }
                    else { $flag.into.props.Add($value) }
                } catch { throw (New-LbfError "invalid value $(Format-LbfQuote $value) for flag -${name}: $(Get-LbfErrorText $_)") }
            } else {
                $flag.into[$flag.key] = $value
            }
        }
        $Set[$name] = $true
    }
    return , $rest.ToArray()
}

function ConvertFrom-LbfGoBool {
    param([string]$Text)
    switch -CaseSensitive ($Text) {
        { @('1', 't', 'T', 'TRUE', 'true', 'True') -ccontains $_ } { return $true }
        { @('0', 'f', 'F', 'FALSE', 'false', 'False') -ccontains $_ } { return $false }
    }
    return $null
}

$script:FlagErrorName = '^(flag provided but not defined: |flag needs an argument: |invalid value ".*" for flag |invalid boolean value ".*" for )-'

# Go's command.parse: flags anywhere, then the checks lbf adds.
function Invoke-LbfCommandParse {
    param([hashtable]$Command, [string[]]$Arguments)
    $set = @{}
    $positional = New-Object System.Collections.Generic.List[string]
    $args2 = $Arguments
    try {
        while ($true) {
            $rest = Invoke-LbfFlagParse $Command $args2 $set
            if ($rest.Count -eq 0) { break }
            $positional.Add($rest[0])
            if ($rest.Count -gt 1) { $args2 = [string[]]$rest[1..($rest.Count - 1)] } else { $args2 = [string[]]@() }
        }
    } catch {
        if (Test-LbfHelpRequest $_) { throw }
        $msg = [regex]::Replace((Get-LbfErrorText $_), $script:FlagErrorName, '$1--')
        throw (New-LbfUsageError $Command $msg)
    }
    foreach ($f in $Command.flags) {
        if ($f.str -and $set.ContainsKey($f.name) -and (Get-LbfFlagValue $f).Trim() -eq '') {
            throw (New-LbfError "--$($f.name) is empty; give it a value, or leave it out for the default")
        }
    }
    if ($set.ContainsKey('sas-env')) {
        foreach ($n in @('tag', 'account')) {
            if ($set.ContainsKey($n)) {
                throw (New-LbfError "--$n cannot be used with --sas-env, whose file already names the storage account; choose the account when minting the file with 'lbf mint-sas'")
            }
        }
        if ($set.ContainsKey('tenant')) { throw (New-LbfError '--tenant cannot be used with --sas-env, which needs no sign-in') }
    }
    $want = 0
    if ($Command.arg -ne '') { $want = 1 }
    if ($positional.Count -gt $want) { throw (New-LbfUsageError $Command "unexpected argument $(Format-LbfQuote $positional[$want])") }
    if ($positional.Count -lt $want) { throw (New-LbfUsageError $Command "missing $($Command.arg)") }
    foreach ($f in $Command.flags) {
        if ($f.required -and (Get-LbfFlagValue $f) -eq '') { throw (New-LbfUsageError $Command "missing --$($f.name)") }
    }
    return , $positional.ToArray()
}

function New-LbfUsageError {
    param([hashtable]$Command, [string]$Message)
    return New-LbfError ("$Message`n`n" + (Get-LbfCommandUsage $Command))
}

function Get-LbfCommandUsage {
    param([hashtable]$Command)
    $required = New-Object System.Collections.Generic.List[hashtable]
    $optional = New-Object System.Collections.Generic.List[hashtable]
    if ($Command.arg -ne '') { $required.Add(@{ left = $Command.arg; desc = $Command.argDesc }) }
    foreach ($f in $Command.flags) {
        $desc = $f.usage
        if ($f.def -ne '' -and $f.def -ne 'false') { $desc += " (default $($f.def))" }
        $row = @{ left = ('--' + ($f.name + ' ' + $f.arg).Trim()); desc = $desc }
        if ($f.required) { $required.Add($row) } else { $optional.Add($row) }
    }
    $width = 0
    foreach ($r in $required) { $width = [Math]::Max($width, (Get-LbfUtf8Length $r.left)) }
    foreach ($r in $optional) { $width = [Math]::Max($width, (Get-LbfUtf8Length $r.left)) }
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append("Usage: lbf $($Command.synopsis)`n")
    foreach ($sec in @(@{ title = 'Required:'; rows = $required }, @{ title = 'Optional:'; rows = $optional })) {
        if ($sec.rows.Count -eq 0) { continue }
        [void]$sb.Append("`n$($sec.title)`n")
        foreach ($r in $sec.rows) {
            [void]$sb.Append('  ' + $r.left.PadRight($width) + '   ' + $r.desc + "`n")
        }
    }
    if ($Command.notes -ne '') { [void]$sb.Append("`n" + $Command.notes) }
    return $sb.ToString()
}

function Invoke-LbfParseArgs {
    param([string]$Name, [string[]]$Arguments)
    $o = New-LbfOptions
    $c = New-LbfCommand $Name $o
    $positional = Invoke-LbfCommandParse $c $Arguments
    if ($o.tenant -eq '') { $o.tenant = $script:ImperialTenant }
    return @{ options = $o; positional = $positional; command = $c }
}

function New-LbfTarget {
    param([string]$Account = '', [string]$Container = '', [string]$SAS = '', $Expiry = $null, [string]$Permissions = '',
        [string]$User = '', [string]$SubscriptionName = '', [string]$SubscriptionID = '', [string]$Source = '', [string]$Endpoint = '')
    return @{
        Account = $Account; Container = $Container; SAS = $SAS; Expiry = $Expiry; Permissions = $Permissions
        User = $User; SubscriptionName = $SubscriptionName; SubscriptionID = $SubscriptionID; Source = $Source; Endpoint = $Endpoint
    }
}

function Get-LbfContainerUrl {
    param([hashtable]$Target)
    if ($Target.Endpoint -ne '') { return $Target.Endpoint + '/' + $Target.Container }
    return "https://$($Target.Account).blob.core.windows.net/$($Target.Container)"
}

# Go's Time.Format(time.RFC3339): Z for a zero offset.
function Format-LbfRfc3339 {
    param([Parameter(Mandatory)]$Time)
    if ($Time -is [DateTimeOffset]) {
        if ($Time.Offset -eq [TimeSpan]::Zero) { return $Time.UtcDateTime.ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", $script:Invariant) }
        return $Time.ToString("yyyy-MM-dd'T'HH:mm:sszzz", $script:Invariant)
    }
    return Format-LbfTimestamp $Time
}

function Get-LbfOrUnknown {
    param([AllowNull()][string]$Text)
    if ($null -eq $Text -or $Text.Trim() -eq '') { return 'unknown' }
    return $Text.Trim()
}

function Get-LbfCheckSummary {
    param([hashtable]$Target, [string]$Mode)
    if ($Target.Source -eq '') {
        return "$(Get-LbfOrUnknown $Target.User) can $Mode $($Target.Account)/$($Target.Container) until $(Format-LbfRfc3339 $Target.Expiry)"
    }
    return "$(Get-LbfOrUnknown $Target.User) can $Mode $($Target.Account)/$($Target.Container), signed in via $($Target.Source)"
}

function Get-LbfLoginMessage {
    param([string]$User, $StoreError)
    if ($null -eq $StoreError) { return "Signed in as $User; lbf will reuse this login until 'lbf logout'" }
    if ((Get-LbfException $StoreError).Data.Contains('lbfNoStore')) { return "Signed in as $User, but this build cannot remember logins; use 'az login' or --sas-env" }
    return "Signed in as $User, but lbf could not remember this login because $(Get-LbfErrorText $StoreError); use 'az login' or --sas-env"
}

function Get-LbfLogoutMessage {
    param([string]$User, [bool]$HadRecord, [bool]$HadTokens, $StoreError)
    $who = $User
    if ($who -eq '') { $who = 'you' }
    if ($HadTokens) { return "Signed $who out of lbf and deleted its saved tokens" }
    if ($HadRecord) { return "Signed $who out of lbf" }
    if ($null -ne $StoreError -and (Get-LbfException $StoreError).Data.Contains('lbfNoStore')) { return 'This build of lbf never saves a login, so there was nothing to sign out of' }
    return 'lbf had no saved login'
}

# The storage account and container the options pick, from --sas-env or by signing in.
function Get-LbfTarget {
    param([hashtable]$Options, [string]$Mode, [string]$Container)
    if ($Options.sasEnv -ne '') { return Read-LbfSasEnv $Options.sasEnv $Mode $Container }
    return Get-LbfMintedTarget -Tenant $Options.tenant -Tag $Options.tag -AccountName $Options.account -ContainerName $Container -Mode $Mode
}

function Invoke-Lbf {
    param([string[]]$Arguments)
    if ($Arguments.Count -eq 0 -or $Arguments[0] -ceq '-h' -or $Arguments[0] -ceq '--help' -or $Arguments[0] -ceq 'help') {
        Write-LbfOut $script:Usage
        return
    }
    if ($Arguments[0] -ceq 'version' -or $Arguments[0] -ceq '--version') {
        Write-LbfOut "lbf $($script:LbfVersion)`n"
        return
    }
    $cmd = $Arguments[0]
    $rest = [string[]]@()
    if ($Arguments.Count -gt 1) { $rest = [string[]]$Arguments[1..($Arguments.Count - 1)] }
    try {
        $parsed = Invoke-LbfParseArgs $cmd $rest
    } catch {
        if (Test-LbfHelpRequest $_) {
            Write-LbfOut (Get-LbfCommandUsage (New-LbfCommand $cmd (New-LbfOptions)))
            return
        }
        throw
    }
    $o = $parsed.options
    $positional = $parsed.positional

    switch -CaseSensitive ($cmd) {
        { $_ -eq 'publish' -or $_ -eq 'upload' } {
            $pub = New-LbfPublication -InputPath $positional[0] -Provenance $o.prov -ProfileDir $o.profile -Id $o.id
            if ($o.dryRun) {
                $t = New-LbfTarget -Account 'dryrun' -Container $o.container -User 'dry-run'
                $crate = Get-LbfCrate -Publication $pub -Target $t
                if ($o.json) {
                    Write-LbfOut ((ConvertTo-LbfJson @{ id = $pub.ID; url = (Get-LbfContainerUrl $t) + '/' + $pub.ID }) + "`n")
                } else {
                    Write-LbfOut ($crate + "`n")
                }
                return
            }
            $t = Get-LbfTarget $o 'upload' $o.container
            Invoke-LbfUpload -Target $t -Publication $pub
            $url = (Get-LbfContainerUrl $t) + '/' + $pub.ID
            if ($o.json) { Write-LbfOut ((ConvertTo-LbfJson @{ id = $pub.ID; url = $url }) + "`n") }
            else { Write-LbfOut "Uploaded as $($pub.ID)`n$url`n" }
            return
        }
        { $_ -eq 'fetch' -or $_ -eq 'download' } {
            $id = $positional[0]
            if (-not (Test-LbfValidId $id)) { throw (New-LbfError "$(Format-LbfQuote $id) is not a dataset ID") }
            $t = Get-LbfTarget $o 'download' $o.container
            $got = Invoke-LbfDownload -Target $t -Id $id -OutDir $o.out
            $url = (Get-LbfContainerUrl $t) + '/' + $id
            if ($o.json) {
                $m = @{ id = $id; url = $url; path = $got.Path; data_path = $got.DataPath; provenance = $got.Stated }
                Write-LbfOut ((ConvertTo-LbfJson $m) + "`n")
            } else {
                Write-LbfOut "$($got.Path)`n"
            }
            return
        }
        'new-id' {
            Write-LbfOut "$(New-LbfId)`n"
            return
        }
        'browse' {
            Invoke-LbfBrowse -Options $o
            return
        }
        'check' {
            if ($o.mode -cne 'upload' -and $o.mode -cne 'download') { throw (New-LbfError "--mode must be 'upload' or 'download'") }
            $t = Get-LbfTarget $o $o.mode $o.container
            Invoke-LbfCheckAccess -Target $t -Mode $o.mode
            Write-LbfOut "$(Get-LbfCheckSummary $t $o.mode)`n"
            return
        }
        'login' {
            $cred = Invoke-LbfLogin -Tenant $o.tenant
            $user = Get-LbfSignedInUser -Credential $cred
            Write-LbfOut "$(Get-LbfLoginMessage $user (Get-LbfTokenStoreError))`n"
            return
        }
        'logout' {
            $r = Invoke-LbfLogout
            Write-LbfOut "$(Get-LbfLogoutMessage $r.User $r.HadRecord $r.HadTokens (Get-LbfTokenStoreError))`n"
            $azUser = Get-LbfAzCliUser -Tenant $o.tenant
            if ($null -ne $azUser) {
                Write-LbfOut "Still signed in as $azUser through 'az login', which lbf uses next; run 'az logout' to sign out of that too`n"
            }
            return
        }
        'upgrade' {
            if ($script:LbfVersion -eq 'dev') { throw (New-LbfError 'this is a development build; install a release with the command in the README') }
            $r = Invoke-LbfUpgrade
            if ($r.Upgraded) { Write-LbfOut "Upgraded lbf $($script:LbfVersion) -> $($r.Latest)`n" }
            else { Write-LbfOut "lbf $($script:LbfVersion) is up to date`n" }
            return
        }
        'mint-sas' {
            if ($o.mode -cne 'upload' -and $o.mode -cne 'download') { throw (New-LbfError "--mode must be 'upload' or 'download'") }
            $t = Get-LbfMintedTarget -Tenant $o.tenant -Tag $o.tag -AccountName $o.account -ContainerName $o.container -Mode $o.mode
            Write-LbfSasEnv -Path $o.out -Target $t
            Write-LbfOut "Wrote $($o.out) ($($t.Account)/$($t.Container), permissions=$($t.Permissions), expires $(Format-LbfRfc3339 $t.Expiry))`n"
            return
        }
    }
    throw (New-LbfError ("unknown command $(Format-LbfQuote $cmd)`n`n" + $script:Usage))
}
