# Profile skeleton

Two profiles to copy when writing your own: `plate-read`, for a plate as the
reader exported it, and `plate-summary`, for statistics derived from one by a
script. Both build on lbf's bronze profile 0.7.0. How profiles work is in
[docs/publishing.md](../../docs/publishing.md).

```
profiles/
  plate-read/0.1.0/profile.json      a bronze dataset: readings.csv and the plate's properties
  plate-summary/0.1.0/profile.json   a derived dataset: summary.csv, its parent and the script
example/
  plate1/readings.csv                a plate read
  plate1.properties.json             its properties, as a form would write them
  plate-summary/summary.csv          per-row statistics of plate1
  summary.properties.json
```

## A profile's two halves

`properties` checks the crate itself, in RO-Crate's terms. `plate-read` asks
that some file in `hasPart` be `readings.csv`; `plate-summary` requires a parent
(`isBasedOn`) and a run (`mentions`) whose `instrument` is the plate-summary
script.

`$defs/properties` checks the properties a publisher gives, as one record. Give
each property a `type`, `title`, `description` and `examples`: lbf stores the
value as that type, and says the title, description and first example when a
dataset falls short. A unit is `x-unitCode`, its URL, and `x-unitText`, how
people write it; lbf records both on the property in the crate.

## Check, then publish

Run from this folder. Check a profile before anyone publishes against it:

```bash
lbf profiles check profiles/plate-read/0.1.0
```

`--dry-run` checks a dataset and prints its crate without signing in:

```bash
lbf publish example/plate1 --profile profiles/plate-read/0.1.0 \
    --properties example/plate1.properties.json --dry-run

lbf publish example/plate-summary --profile profiles/plate-summary/0.1.0 \
    --properties example/summary.properties.json \
    --derived-from <plate1's ID> \
    --instrument name=plate-summary,version=0.1.0,url=https://example.org/plate-summary --dry-run
```

Leave a property out, or give one the profile refuses, to see what lbf says:

```
dataset does not meet profile Plate read (https://github.com/.../plate-read/0.1.0):
  --property wavelength_nm: is 90, but must be at least 200
      Wavelength; The wavelength the reader measured at, in nanometres; e.g. 450
  missing --property operator=...
      Who ran the reader; Your name as the lab rota gives it; e.g. A. Researcher
```

Each published dataset keeps the schemas it met under `ro-crate-profiles/`,
beside the published folder.

## Changing a profile

A published version never changes: copy the folder to a new version, change
its `$id` to match, and point the pipeline's `--profile` at it. A profile's
`$id` ends in `/<name>/<version>`; these use the GitHub URL of their folder, so
they resolve once committed.
