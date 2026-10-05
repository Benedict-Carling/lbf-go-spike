# lbf (Go spike)

A single-binary replacement for the Nextflow upload, download and mint-sas
pipelines. No Nextflow, Java, Python, bash or azcopy; runs natively on macOS,
Linux and Windows.

## Install

Windows: open PowerShell (Start menu, type "PowerShell") and paste:

```powershell
irm https://github.com/Benedict-Carling/lbf-go-spike/releases/latest/download/install.ps1 | iex
```

Nothing needs installing first: `irm` and `iex` are built into PowerShell
(short for `Invoke-RestMethod` and `Invoke-Expression`), and both Windows
PowerShell 5.1 and PowerShell 7 work. No admin rights are needed. The script
puts `lbf.exe` in `%LOCALAPPDATA%\lbf` and adds it to your PATH, so `lbf` works
straight away in that window and in new ones. Then run `lbf login`.

If your organisation blocks scripts this way, download `lbf-windows-amd64.exe`
(or `-arm64.exe` on Arm laptops) from the releases page, rename it to `lbf.exe`
and run it from its folder as `.\lbf.exe`.

macOS / Linux:

```sh
curl -fsSL https://github.com/Benedict-Carling/lbf-go-spike/releases/latest/download/install.sh | sh
```

Binaries are also attached to each
[release](https://github.com/Benedict-Carling/lbf-go-spike/releases).

## Update

```
lbf upgrade
```

lbf checks for a newer release at most once an hour and prints a warning after
any command when it is out of date. Set `LBF_NO_UPDATE_CHECK=1` to turn the
check off.

## Use

```
lbf login                          # optional: browser sign-in to Imperial, remembered (macOS/Windows)
lbf publish <path>                 # land in bronze; prints the new dataset ID (alias: upload)
lbf fetch <id> [--out DIR]         # alias: download
lbf mint-sas --mode upload         # writes azure_sas.env for a machine without az (HPC)
lbf logout
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
stderr; stdout carries only the result (the dataset ID and URL, or the fetched path). `--tag KEY=VALUE` picks
the storage account (default `tag=storage`, production; the test account is
`tag=storage-test`). If several accounts carry the tag, lbf asks which one in a terminal, and
otherwise lists them for `--account NAME` to choose. `--container` defaults to `bronze`, and `--sas-env FILE`
uses a pre-minted credential instead of `az login`.

Blob layout, RO-Crate sidecar and `azure_sas.env` format match the Nextflow
pipelines, so either tool can read the other's output.

## Build

Pushing a `v*` tag builds every platform and publishes a release.

```
go test ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/lbf-windows-amd64.exe .
```
