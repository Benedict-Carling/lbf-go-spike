# lbf for Windows as a PowerShell script

Status: ready-for-agent

## Why

Imperial's Windows PCs run Defender for Endpoint with the ASR rule "Use advanced protection against ransomware", which blocks the unsigned `lbf.exe` (Stuart's install, 2026-11-05). Scripts run by Microsoft's signed `powershell.exe` are not executables, so the rule does not apply. On Windows, `install.ps1` installs `lbf.ps1` instead of `lbf.exe`. macOS, Linux and pipelines keep the Go binary.

## The one rule

Every command, flag, message, exit code, file and blob the PowerShell lbf produces is the same as the Go lbf's on the same input. The Go code is the specification. Where they differ, the PowerShell side is wrong unless the difference is one listed under "Allowed differences".

## Layout

- `ps/src/*.ps1`: the source, concatenated in file-name order by `go run ./tools/buildps` into one `lbf.ps1`. Each file owns one area:
  - `00-core.ps1`: errors, stdout/stderr, status/logf, Go-style `%q`, the Go-compatible JSON writer and strict JSON reader, small helpers.
  - `10-cli.ps1`: the command table, Go `flag`-compatible parsing, usage text, dispatch.
  - `20-dataset.ps1`: walking a dataset, name problems, IDs.
  - `30-provenance.ps1`: provenance flags and files.
  - `40-profile.ps1`: JSON Schema validation for profiles.
  - `50-crate.ps1`: building and reading crates.
  - `60-blobs.ps1`: Azure Blob REST over a SAS.
  - `70-transfer.ps1`: publish, fetch, check.
  - `75-progress.ps1`: progress display.
  - `80-auth.ps1`: sign-in, the saved login, ARM discovery, the account picker, user delegation SAS, sas-env files.
  - `85-browse.ps1`: browse.
  - `90-upgrade.ps1`: upgrade and the update check.
  - `99-main.ps1`: the entry point.
- The builder prepends the version, the bronze profile, the petname word lists and `browse.html`, so the script needs no other file.
- `conformance_test.go` and `conformance_*_test.go`: run scenarios through both lbfs and compare. Skipped unless `LBF_PS` names a PowerShell (`pwsh` or `powershell.exe`).

## Windows PowerShell 5.1 rules

The script must run on Windows PowerShell 5.1 (.NET Framework 4.x), which is what Imperial PCs have, and on PowerShell 7. Locally you have only PowerShell 7 (`$LBF_PS`), so obey these by reading, not by testing:

- No PS7-only syntax: no `?:`, `??`, `??=`, `?.`, `&&`, `||`, `ForEach-Object -Parallel`, `clean {}`, `[ordered]` casts of non-literals, `-AsHashtable`, `ConvertFrom-Json -Depth`, `Join-String`, `Get-Content -AsByteStream`, `$PSStyle`, `$IsWindows` (use `[Environment]::OSVersion.Platform -eq 'Win32NT'`), `Test-Json`, `ConvertTo-Json -EscapeHandling`, string `.Contains(char)` overloads that .NET Framework lacks, `[System.Text.Json]`, `[System.IO.Path]::GetRelativePath`, `[Convert]::ToHexString`, `[System.Security.Cryptography.SHA256]::HashData`, `RandomNumberGenerator.Fill`/`GetBytes(int)`, `[System.Net.Http.HttpClient]` without first `Add-Type -AssemblyName System.Net.Http`, `Range`/`Index`, `String.Split(string)` (use `-split` or `Split([string[]], options)`).
- Never `Add-Type` with source code. It compiles a DLL to disk, which Defender may block. `Add-Type -AssemblyName` of a .NET Framework assembly is fine.
- `Set-StrictMode -Version 3.0` and `$ErrorActionPreference = 'Stop'` are on.
- Windows PowerShell 5.1 reads a `.ps1` without a BOM as the ANSI code page, so the source must be ASCII only. Write non-ASCII characters as `[char]0x2026` etc.
- Text files lbf writes are UTF-8 without BOM: `[IO.File]::WriteAllText($p, $s, (New-Object Text.UTF8Encoding $false))`. Never `Out-File`, `Set-Content` or `>`.
- Paths: always pass full paths to .NET (`[IO.Path]::GetFullPath`), since .NET's current directory is not PowerShell's location.

## Output rules

- Results that the Go lbf prints to stdout are written only by the command functions in `10-cli.ps1`, with `Write-Output`, so `$id = lbf new-id` captures them as it does for the exe. Helper functions return values and never write to the pipeline: wrap any .NET call that returns a value you do not want in `[void](...)` or `$null = ...`.
- stderr is `Write-LbfErr` (`[Console]::Error.Write`). Progress, `[auth]`/`[sas]` logs, status lines: as in Go's `logf` and `status`, via `Write-LbfLog` and `Set-LbfStatus`.
- Errors: `throw (New-LbfError 'message')`, or `New-LbfError 'message' -ExitCode 3` where Go returns `idTaken`. `99-main.ps1` prints `error: <message>`, redacting `sig=`, and exits 1 (or the error's code). Messages must be Go's exactly, including wrapped causes. For OS errors, Go on Windows prints e.g. `open C:\x\y: The system cannot find the file specified.`; produce the same with `Format-LbfOsError`.

## JSON

- Write with `ConvertTo-LbfJson` (in `00-core.ps1`). It encodes like Go's `encoding/json`: keys of a `[hashtable]` or `Dictionary` sorted by ordinal (Go maps); keys of an `[ordered]`/`OrderedDictionary` in insertion order (Go structs); Go's string escaping (`<`, `>`, `&`, U+2028, U+2029, control characters); `-Indent '    '` for `json.MarshalIndent`; compact plus a newline for `json.Encoder`.
- Read untrusted JSON (provenance, properties and profile files) with `ConvertFrom-LbfJson`, which keeps key case and order, refuses duplicate keys like Go would, and returns ordered dictionaries, `[object[]]`, strings, `[long]`/`[double]`, booleans and `$null`. Crates are read the same way.

## Testing

- `LBF_AZURITE=1 LBF_PS=<path to pwsh> go test ./...` with Azurite running. Every scenario runs the Go lbf (the test binary re-executed with `LBF_CONFORMANCE_MAIN=1`) and `lbf.ps1` through `$LBF_PS -NoProfile -NonInteractive -Command`, invoking the script as a user does in a PowerShell session, then compares stdout, stderr, exit code, files written, and blobs stored.
- `LBF_BLOB_ENDPOINT` points both lbfs at Azurite instead of `https://<account>.blob.core.windows.net`.
- `LBF_TEST_NO_MAIN=1` makes the script define its functions without running, so a test can dot-source it and call them.
- Test seams for code only the PowerShell side can test against fakes are named `LBF_TEST_*` and read only in PowerShell.
- CI runs the conformance tests on `windows-latest` with `powershell.exe` (5.1) and on Linux with `pwsh`.

## Contracts between areas

`10-cli.ps1` calls these; each area defines its own. Targets are hashtables from `New-LbfTarget` (Account, Container, SAS, Expiry as `[DateTimeOffset]`, Permissions, User, SubscriptionName, SubscriptionID, Source, Endpoint). Options are the hashtable from `New-LbfOptions`.

- Dataset, provenance, profile, crate building (`20`, `30`, `40`, `50`):
  - `ConvertTo-LbfInstrument <string>`: Go's `parseInstrument`; throws its error text.
  - `New-LbfPublication -InputPath -Provenance <options.prov> -ProfileDir -Id`: Go's `preparePublication`. Returns a hashtable with at least `ID`, `Source`, `Files` (list of hashtables `Rel`, `Path`, `Size`, `SHA256`), `Provenance`, `Profile`.
  - `Get-LbfCrate -Publication -Target`: Go's `publication.crate(t)`, the crate's bytes as a string.
  - `New-LbfId`, `Test-LbfValidId <id>`.
  - Provenance as the JSON Go writes for the `provenance` struct: `ConvertTo-LbfProvenanceValue <provenance>` returns what `ConvertTo-LbfJson` encodes identically (used for `fetch --json`).
- Blobs, transfer, progress, sas-env files (`60`, `70`, `75`, and `Read-LbfSasEnv`/`Write-LbfSasEnv` in `70`):
  - `Read-LbfSasEnv <path> <mode> <container>`, `Write-LbfSasEnv -Path -Target`.
  - `Invoke-LbfUpload -Target -Publication`, `Invoke-LbfDownload -Target -Id -OutDir` (returns `Path`, `DataPath`, `Stated`), `Invoke-LbfCheckAccess -Target -Mode`.
  - Reading stored crates (Go's `readCrate`, `byPath`, `differences`, `differsFrom`, `statement`) lives here too.
- Sign-in and Azure (`80`): `Get-LbfMintedTarget -Tenant -Tag -AccountName -ContainerName -Mode`, `Invoke-LbfLogin -Tenant` (returns a credential), `Get-LbfSignedInUser -Credential`, `Get-LbfTokenStoreError` (`$null`, or an error whose `Data` has `lbfNoStore` when this lbf cannot remember logins), `Invoke-LbfLogout` (returns `User`, `HadRecord`, `HadTokens`), `Get-LbfAzCliUser -Tenant` (`$null` when az is absent or signed out). For browse: `Get-LbfCredential -Tenant` (Go's `credential`, returns `Credential` and `Source`), `Find-LbfAccounts -Credential -Tag`, `Select-LbfAccount -Matches -Tag -Want` (Go's `chooseAccount` with the terminal picker), `Get-LbfAccessToken -Credential -Scope`.
- Browse and upgrade (`85`, `90`): `Invoke-LbfBrowse -Options`, `Invoke-LbfUpgrade` (returns `Latest`, `Upgraded`), `Start-LbfUpdateCheck <args>` and `Complete-LbfUpdateCheck <state>`.

## Allowed differences

- Line endings: PowerShell writes `\r\n` on Windows; the harness compares with `\r\n` folded to `\n`.
- Timing text in progress (rates, durations) and the order in which parallel transfers finish.
- `lbf version` on a development build prints `lbf dev` from both.
- Writing to a closed stdout (Go's "could not write ... to stdout" errors): PowerShell has no equivalent.
