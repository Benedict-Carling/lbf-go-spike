# profile-skeleton

How to write a profile, describe a dataset, and publish it with `lbf`, without
Nextflow. The example is a plate reader: a plate read lands in bronze, and its
summary lands in silver, recorded as made from the plate read.

```
profiles/
  plate-read/0.1.0/profile.json      rules for the bronze plate read
  plate-summary/0.1.0/profile.json   rules for the silver summary
example/
  plate1/                            the plate read: readings.csv, notes.txt
  plate1.properties.json             its properties
  plate-summary/                     the summary: summary.csv
  summary.properties.json            its properties
```

## Try it

Run from this folder. `--dry-run` checks the dataset and prints its crate
without uploading anything:

```bash
lbf publish example/plate1 --profile profiles/plate-read/0.1.0 \
    --property plate_id=P-0001 --property "operator=A. Researcher" --property reader_serial=SN-204417 \
    --dry-run
```

`lbf profiles show profiles/plate-read/0.1.0` says what the profile asks for.
To publish the plate read for real:

```bash
lbf publish example/plate1 --profile profiles/plate-read/0.1.0 \
    --properties example/plate1.properties.json --tag tag=storage-test
```

That prints the new dataset's ID, e.g. `20261007-causal-bison-9ece`. Publish
the summary as made from it:

```bash
lbf publish example/plate-summary --container silver \
    --derived-from 20261007-causal-bison-9ece \
    --instrument name=plate-summary,version=0.1.0,url=https://github.com/ImperialCollegeLondon/lbf-profile-skeleton/releases/tag/v0.1.0 \
    --properties example/summary.properties.json \
    --profile profiles/plate-summary/0.1.0 --tag tag=storage-test
```

In a real pipeline the summary would be computed from the fetched plate read;
here it is already made. `lbf fetch 20261007-causal-bison-9ece --json` also
prints what its crate states, in the same shape as a `--provenance` file, so a
script can carry `plate_id` forward without reading the crate itself.

## What the publisher gives

lbf writes an RO-Crate 1.3 crate beside the data. Everything in it is a
schema.org term, or one the RO-Crate specification adopts.

| Flag | In the crate |
|---|---|
| `<path>` | `hasPart`: a `File` for each file, with its size and sha256 |
| `--name`, `--description` | `name`, `description`; lbf uses the folder's name, and writes a description, when they are left out |
| `--property NAME=VALUE`, `--properties FILE` | `additionalProperty`: a `PropertyValue` each |
| `--derived-from ID` | `isBasedOn`, and the `object` of the run: a `CreateAction` the root `mentions` |
| `--instrument name=,version=,url=` | the run's `instrument`, a `SoftwareApplication` |
| `--profile`, repeated when it must meet several | `conformsTo`, with the schema of each profile it checked against |

lbf itself sets `identifier`, `datePublished`, `license`, `creator` (whoever
uploaded), `distribution`, and the properties `subscription_name`,
`subscription_id` and `source_path`. `--derived-from` and `--instrument` come
together or not at all. The same can be given as one file with
`--provenance provenance.json`, which `--property`, `--name` and
`--description` add to:

```json
{"name": "Plate P-0001 summary",
 "derived_from": "20261007-causal-bison-9ece",
 "instruments": [{"name": "plate-summary", "version": "0.1.0", "url": "https://…/releases/tag/v0.1.0"}],
 "properties": {"plate_id": "P-0001", "method": "mean"}}
```

## Writing a profile

A profile is a JSON Schema in `profiles/<name>/<version>/profile.json`. It
needs:

- `$id`: `https://w3id.org/lbf/profiles/<name>/<version>`. It is recorded in
  the crate's `conformsTo`, and ends in the profile's name and version.
- `$ref`: the profile it builds on. Every chain ends at lbf's bronze profile,
  `https://w3id.org/lbf/profiles/bronze/0.5.0`, which is compiled into lbf.
  A profile beside it can be built on by its `$id`.
- `title` and `description`.
- Its rules, written against the crate's root as lbf lays it out (bronze's
  `frame.json`, a JSON-LD frame): what the root refers to is embedded, and
  `additionalProperty` is keyed by name:

```json
{"@id": "./", "@type": "Dataset", "identifier": "…", "name": "plate1",
 "creator": {"@type": "Person", "name": "…"},
 "hasPart": [{"@id": "plate1/readings.csv", "@type": "File", "contentSize": "904"}],
 "additionalProperty": {"plate_id": {"@type": "PropertyValue", "value": "P-0001"}},
 "isBasedOn": [{"@type": "Dataset", "identifier": "20261007-causal-bison-9ece"}],
 "mentions": [{"@type": "CreateAction", "instrument": [{"name": "plate-summary", "version": "0.1.0"}]}]}
```

The two profiles here show the usual rules:

| Rule | How |
|---|---|
| a file must be present | `"hasPart": {"contains": {"properties": {"@id": {"pattern": "(^\|/)readings\\.csv$"}}}}` |
| a property must be given | `"additionalProperty": {"required": ["plate_id"]}` |
| its format | `"additionalProperty": {"properties": {"plate_id": {"properties": {"value": {"pattern": "^P-[0-9]{4}$"}}}}}`, or `enum` |
| it must be made from another dataset | `"required": ["isBasedOn", "mentions"]` |
| by a given program | `"mentions": {"contains": {"properties": {"instrument": {"contains": {"properties": {"name": {"const": "plate-summary"}}}}}}}` |

Give each property a `title`, `description` and `examples`. lbf shows them to
whoever publishes, both in `lbf profiles show` and when something is missing.
A published version never changes: to change the rules, copy the folder to
`0.2.0`, edit it there and change its `$id`.

## When a dataset does not meet its profile

Nothing is uploaded, and lbf says which flag fixes each problem, in the
profile's own words:

```
error: dataset does not meet profile Plate read (https://w3id.org/lbf/profiles/plate-read/0.1.0):
  --property plate_id: 'plate one' is not in the form the profile asks for
      Plate barcode; On the side of the plate; e.g. P-0001
  missing --property operator=...
      Who ran the reader; e.g. A. Researcher
  missing --property reader_serial=...
      Reader serial number; On the back of the reader; e.g. SN-204417
```

## How conformsTo is filled in

No one writes `conformsTo`. lbf follows the profile's `$ref` chain down to
bronze, checks the crate against each profile in it, and lists them all, since
RO-Crate infers nothing from a profile's parents. A derived dataset also
conforms to Process Run Crate 0.6, because lbf records how it was made. Each
profile is described in the crate by what it builds on (`isProfileOf`) and by
the exact schema it was checked against, carried as the `text` of a
`ResourceDescriptor`'s artifact with the role `schema`. So a dataset says not
only which profiles it meets, but what their rules were when it was published.
