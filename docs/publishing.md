# Publishing and fetching

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
