# lbf

Lands datasets in the lab's Azure storage and retrieves them, recording where each one came from and which rules it meets.

## Language

### Datasets

**Dataset**:
A set of files landed together under one Dataset ID, never changed after landing.
_Avoid_: upload, payload, run

**Dataset ID**:
The name lbf gives a dataset when it is published: date, two words and four hex digits, e.g. `20261001-fancy-dassie-eadb`.
_Avoid_: target ID, run ID

**Layer**:
How refined a dataset is: **bronze** holds data as it arrived from an instrument, **silver** holds data derived from bronze. Each layer is a container in the storage account.
_Avoid_: grade, tier, stage

**Storage account**:
The Azure account holding every layer, found by its tag (production `tag=storage`, test `tag=storage-test`).

### Describing a dataset

**Provenance**:
What a publisher states about how a dataset was produced: its parent, the instruments, and its properties.
_Avoid_: metadata, lineage

**Parent**:
The dataset another was derived from (`derived_from`). A dataset has at most one.
_Avoid_: source, target

**Instrument**:
A versioned piece of software that produced a derived dataset, e.g. CellProfiler 4.2.6 or a pipeline.
_Avoid_: tool, workflow

**Property**:
A named text value describing a dataset, e.g. `sample_id`. Some names are reserved for lbf.
_Avoid_: tag, metadata field

**Profile**:
A named, versioned JSON Schema a dataset's crate must meet before it is published, checked against the crate's root as the frame lays it out. Identified by a URI that the crate records, with the schema itself. Profiles build on one another, and every dataset meets the bronze profile as well as its own.
_Avoid_: schema, shape, contract

**Frame**:
How lbf lays a crate's root out for profiles to check: what the root refers to embedded, its files as `hasPart`, its properties keyed by name. Part of the bronze profile's version.

**Crate**:
The RO-Crate record published beside a dataset's files: its ID, parent, instruments, properties, files and profiles.
_Avoid_: sidecar, manifest

### Actions

**Publish**:
Validate a folder against a profile and land it as a new dataset.
_Avoid_: upload, ingest

**Fetch**:
Retrieve a dataset by its ID.
_Avoid_: download, pull
