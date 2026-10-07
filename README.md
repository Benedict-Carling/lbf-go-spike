# lbf (Go spike)

A single-binary replacement for the Nextflow upload, download and mint-sas
pipelines. No Nextflow, Java, Python, bash or azcopy; runs natively on macOS,
Linux and Windows.

## Install

Windows: open PowerShell (Start menu, type "PowerShell") and paste:

```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072; irm https://github.com/Benedict-Carling/lbf-go-spike/releases/latest/download/install.ps1 | iex
```

Nothing needs installing first: `irm` and `iex` are built into PowerShell
(short for `Invoke-RestMethod` and `Invoke-Expression`), and both Windows
PowerShell 5.1 and PowerShell 7 work. The first part turns on TLS 1.2, which
GitHub requires and older Windows PowerShell does not use by default. No admin
rights are needed. The script puts `lbf.exe` in `%LOCALAPPDATA%\lbf` and adds it
to your PATH, so `lbf` works straight away in that window and in new ones. Run
it again to reinstall, even while lbf is running. Then run `lbf login`.

If your organisation blocks scripts this way, download `lbf-windows-amd64.exe`
(or `-arm64.exe` on Arm laptops) from the releases page, rename it to `lbf.exe`
and run it from its folder as `.\lbf.exe`.

macOS / Linux:

```sh
curl -fsSL https://github.com/Benedict-Carling/lbf-go-spike/releases/latest/download/install.sh | sh
```

This puts `lbf` in `~/.local/bin` (override with `LBF_INSTALL_DIR`) and, if
that folder is not already on your PATH, adds it via `~/.profile` and your
bash, zsh and fish startup files, the same way uv and rustup do, sharing their
`env` script when they use the same folder. If the folder already holds an
`env` or `env.fish` that is not such a script, the startup files are left
alone. Open a new terminal afterwards. Set `LBF_NO_MODIFY_PATH=1` to leave
startup files alone.

Both installers check the download against the release's `SHA256SUMS` before
installing it, as `lbf upgrade` does. They and `lbf upgrade` fetch the build
for the machine's own processor, even from an x64 PowerShell or terminal
emulated on Arm.

Binaries are also attached to each
[release](https://github.com/Benedict-Carling/lbf-go-spike/releases).

## Update

```
lbf upgrade
```

lbf checks for a newer release at most once an hour and prints a warning after
any command when it is out of date. Set `LBF_NO_UPDATE_CHECK=1` to turn the
check off. Pre-releases (tags such as `v0.2.0-rc1`) are never offered; anyone
running one is offered the release that follows it.

## Use

```
lbf login                          # optional: browser sign-in to Imperial, remembered (macOS/Windows)
lbf publish <path>                 # land in bronze; prints the new dataset ID (alias: upload)
lbf fetch <id> [--out DIR]         # alias: download
lbf new-id                         # an ID to publish under later, with publish --id
lbf check --mode upload            # proves the credential works, before a long job
lbf mint-sas --mode upload         # writes azure_sas.env for a machine without az (HPC)
lbf logout                         # signs out of lbf and deletes its tokens; says if az login still signs you in
```

Sign-in order: a login saved by `lbf login`, an existing `az login`, then the
browser. Always against Imperial's tenant (`--tenant` overrides). Remembered
logins need the OS credential store, so the static Linux build uses `az` or
`--sas-env` instead. Before uploading, lbf checks it can write to the target and
stops with nothing uploaded if not.

Every publish is validated before anything is uploaded. Every dataset must meet
the bronze profile (`profiles/bronze/profile.json`, compiled into the binary),
plus the one given with `--profile`. Without flags a dataset has no parent and
no properties. Derived datasets record what they came from:

```
lbf publish results/ --container silver \
    --derived-from 20261001-fancy-dassie-eadb \
    --instrument name=my-pipeline,version=0.1.0,url=https://... \
    --properties metadata.json \
    --profile profiles/minimal-silver/0.1.0
```

`metadata.json` holds the properties as strings, e.g. `{"sample_id": "SAM-0001"}`.
`--derived-from` and `--instrument` come together, and `--instrument` repeats
for several. The same provenance can instead be given as one file with
`--provenance provenance.json`:

```json
{"derived_from": "20261001-fancy-dassie-eadb",
 "instruments": [{"name": "my-pipeline", "version": "0.1.0", "url": "https://..."}],
 "properties": {"sample_id": "SAM-0001"}}
```

Every field is optional, but `derived_from` and `instruments` come together.
Unknown fields are rejected.

A profile is a directory holding a JSON Schema `profile.json`, describing both
the data given as `<path>` and the crate lbf writes for it. Its `$id` is
recorded in the crate's `conformsTo`, along with every profile it builds on
through `$ref`; sibling profiles (`profiles/<name>/profile.json` or, versioned,
`profiles/<name>/<version>/profile.json`) and lbf's bronze profile resolve locally. It
validates this view of the dataset, once the uploader is known:

```json
{"data": {"name": "results", "files": [{"path": "features/plate1.parquet", "size": 12}]},
 "crate": {"identifier": "...", "creator": "...", "datePublished": "...",
           "conformsTo": ["..."], "additionalProperty": {"sample_id": "..."},
           "wasDerivedFrom": "...", "instrument": [{"name": "...", "version": "...", "url": "..."}]}}
```

File paths are relative to `<path>`. `crate` uses the RO-Crate's own names.

`--dry-run` validates and prints the crate without signing in, as uploader `dry-run`. Progress goes to
stderr; stdout carries only the result (the dataset ID and URL, or the fetched path). With `--json`,
publish prints `{"id", "url"}` and fetch prints `{"id", "url", "path", "data_path"}` as one line, where
`path` is `<out>/<id>`, holding the crate, and `data_path` is the published folder inside it. `--tag KEY=VALUE` picks
the storage account (default `tag=storage`, production; the test account is
`tag=storage-test`). If several accounts carry the tag, lbf asks which one in a terminal, and
otherwise lists them for `--account NAME` to choose. `--container` defaults to `bronze`, and `--sas-env FILE`
uses a pre-minted credential instead of `az login`.

## Resuming a publish

A dataset is published once its crate lands, so a publish that fails partway
can be finished: run it again with `--id <the ID it printed>`. Files the earlier
attempt stored are checked by sha256 and not sent again; the rest are sent. lbf
never changes or removes a stored file, so if any stored file differs from the
local one, or the earlier attempt stored a file that is no longer there, the
rerun stops before uploading anything; publish without `--id` for a new dataset.
If the dataset was in fact published, the rerun succeeds without uploading
anything when the files and provenance are the same, and refuses if they differ.
Every refusal because the ID holds other files exits with code 3, so a pipeline
can tell it from a failure worth retrying.

A pipeline can take its ID before computing, name its outputs by it, and retry
its publish step:

```
id=$(lbf new-id)                   # offline; no sign-in
lbf check --mode upload --container silver
...compute into results/, logging $id...
lbf publish results --container silver --id $id
```

In Nextflow, mint the ID in its own process so `-resume` reuses it, and give
the publish process `errorStrategy { task.exitStatus == 3 ? 'terminate' : 'retry' }`.
Give that process everything publish records as inputs: the data, its
properties, the parent and the instruments' versions. Then any change mints a new
ID, and exit code 3 only means an ID was reused by hand. `--id` accepts only an ID from
`lbf new-id`. Two publishes of one ID at once cannot overwrite each other's
files, and the stored files are checked against the crate once more after it
lands.

## Integrity

Publish follows symlinks, and refuses, before uploading anything, a dataset with
a broken symlink, an unreadable file, or a name that could not be fetched
everywhere: one Windows cannot store (`\ : * ? " < > |`, a trailing dot or
space, `CON`, `NUL` and the like) or two that differ only in case. Each file's
sha256 is computed as it is sent, recorded in the crate, and stored with the
file as blob metadata, which is how a resumed publish knows what is already
there. A file that changes while it is being sent stops the publish.

Fetch downloads exactly the files the crate lists, refusing a dataset whose
stored files differ from it, and checks each file's size and sha256 (size only
for datasets published before lbf recorded checksums). It works in
`<id>.partial` and renames it to `<id>` only once everything has checked out,
so `<id>` is always complete. Fetching into a folder that already holds the
dataset verifies it instead of downloading again.

Blob layout, RO-Crate sidecar and `azure_sas.env` format match the Nextflow
pipelines, so either tool can read the other's output; crates written by the
Nextflow pipelines carry no checksums.

## Build

Pushing a `v*` tag builds every platform and publishes a release.

```
npx -p azurite azurite-blob --inMemoryPersistence &   # optional: the publish tests skip without it
go test ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/lbf-windows-amd64.exe .
```
