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
lbf browse                         # search every dataset's crate in your browser
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
plus the one given with `--profile`. Derived datasets record what they came from:

```
lbf publish results/ --container silver \
    --derived-from 20261001-fancy-dassie-eadb \
    --instrument name=my-pipeline,version=0.1.0,url=https://... \
    --property sample_id=SAM-0001 \
    --profile profiles/minimal-silver/0.1.0
```

`--property NAME=VALUE` repeats for several properties, and `--properties metadata.json`
gives them as a JSON object of strings, e.g. `{"sample_id": "SAM-0001"}`.
`--name` and `--description` say what the dataset is; without them lbf uses the
folder's name and writes a description. `--derived-from` and `--instrument`
come together, and `--instrument` repeats for several. The same can instead be
given as one file with `--provenance provenance.json`, which `--property`,
`--name` and `--description` add to; anything given twice is refused:

```json
{"name": "Plate 1 features",
 "derived_from": "20261001-fancy-dassie-eadb",
 "instruments": [{"name": "my-pipeline", "version": "0.1.0", "url": "https://..."}],
 "properties": {"sample_id": "SAM-0001"}}
```

Every field is optional, but `derived_from` and `instruments` come together.
`derived_from` must be an ID lbf minted, and instruments that share a `url`
must be the same instrument. Field names are exact: unknown or differently
cased fields are rejected, as is anything after the closing brace.

The crate is RO-Crate 1.3, in schema.org terms or those RO-Crate adopts:
properties are `PropertyValue`s, the parent is `isBasedOn`, and a derived
dataset also has a `CreateAction` run, which the root `mentions`, conforming to
Process Run Crate 0.6.

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

File paths are relative to `<path>`. `crate` uses lbf's own names for what the
crate records, whichever RO-Crate terms it uses.

`--dry-run` validates and prints the crate without signing in, as uploader `dry-run`;
with `--json` it prints `{"id", "url"}` instead, the url on a placeholder `dryrun` account. Progress goes to
stderr; stdout carries only the result (the dataset ID and URL, or the fetched path). With `--json`,
publish prints `{"id", "url"}` and fetch prints `{"id", "url", "path", "data_path", "provenance"}` as one line, where
`path` is `<out>/<id>`, holding the crate, and `data_path` is the published folder inside it; fetch's
`provenance` is what the crate states (`name`, `description`, `derived_from`, `instruments`, `properties`), as in a `--provenance` file, leaving out a name and description lbf filled in. `--tag KEY=VALUE` picks
the storage account (default `tag=storage`, production; the test account is
`tag=storage-test`). If several accounts carry the tag, lbf asks which one in a terminal, and
otherwise lists them for `--account NAME` to choose. `--container` defaults to `bronze`, and `--sas-env FILE`
uses a pre-minted credential instead of `az login`.

## Design decisions

Why lbf describes datasets as it does. Change these deliberately,
and record a new decision here.

- **The crate is RO-Crate 1.3, in the RO-Crate specification's terms first and
  schema.org's next; lbf invents none.** Where schema.org has no word, the
  specification's own choice is used (`conformsTo` from Dublin Core, the
  Profiles vocabulary for profiles). Lab-specific values are
  `additionalProperty` `PropertyValue`s, schema.org's mechanism for properties
  it does not name.
- **A parent is `isBasedOn`, and how it was made is a `CreateAction` the root
  `mentions`, conforming to Process Run Crate 0.6.** The specification records
  provenance with schema.org actions, never PROV's `wasDerivedFrom`, which
  earlier lbf versions wrote and lbf still reads. lbf records only what it saw
  or was told: the uploader is the `creator`, and the run has no `endTime`,
  since lbf never sees the run.
- **What a publisher states is one record: a name, description, parent,
  instruments and properties.** The flags, the `--provenance` file and
  `lbf fetch --json` all use it. Flags give values (`--property`) and links
  (`--derived-from`, `--instrument`, since those point at other entities);
  lbf fills a missing name and description. Richer metadata travels as a crate
  of its own inside the published folder, which lbf publishes as a file and
  never merges.
- **lbf states only what it knows.** The licence is an entity with a name,
  as RO-Crate asks. A file's `encodingFormat` comes from a fixed table of
  extensions, not the operating system's, so a crate is the same wherever it is
  written; a file whose type is not in the table has none. Without a known
  uploader the crate has no `creator`, rather than an invented one, so bronze
  refuses it. `lbf fetch --json` leaves out a name or description lbf filled
  in, so carrying it forward never passes off lbf's words as the publisher's.

## Browsing datasets

`lbf browse` lists every container in the storage account (or only `--container a,b`), reads each
dataset's `ro-crate-metadata.json`, and opens a page on this computer to search, filter and look
through them, with each dataset's provenance, files and fetch command. It reads crates only, never
data, using your own sign-in, so you see what you can read. Crates never change once published, so
each is downloaded once and cached under the user cache directory (`lbf/crates`); a dataset without
a crate is not shown. Listing containers needs read access to the whole account. Ctrl-C stops it.

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
space, `CON`, `NUL`, `CONIN$` and the like), one longer than Linux allows, two
that macOS or Windows would store as one (differing only in case or Unicode
form, or a file named like a folder), or one named like the crate. Each file's
sha256 is computed as it is sent, recorded in the crate, and stored with the
file as blob metadata, which is how a resumed publish knows what is already
there. A file that changes while it is being sent stops the publish.

Fetch downloads exactly the files the crate lists, refusing a dataset whose
stored files differ from it, and checks each file's size and sha256 (size only
for datasets published before lbf recorded checksums). It works in a folder
of its own, `<id>.partial-<random>`, and renames it to `<id>` only once
everything has checked out, so `<id>` is always complete. Fetching into a folder
that already holds the dataset verifies it instead of downloading again, and
several fetches of one dataset into one folder can run at once. A fetch removes
its own `.partial-` folder if it fails; one that is killed can leave it behind,
and it is safe to delete once no fetch is running.

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
