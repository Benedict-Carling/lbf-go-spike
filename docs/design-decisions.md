# Design decisions

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
- **A profile is JSON Schema over the crate itself, as bronze's
  `frame.json` lays it out, and over the properties given as one record.**
  Bronze 0.4.0 checked a view only lbf used (`data` for the files beside
  `crate` for its metadata), so profile authors learned lbf's names rather than
  RO-Crate's. From 0.7.0 json-gold frames the crate's flat graph into a tree,
  with what the root refers to embedded and properties keyed by name, so what a
  crate stores, what a profile checks and what RO-Crate tools read share one
  vocabulary. The properties a publisher gives are checked apart, against
  `$defs/properties`, as the flat record flags, files and forms give, so a
  profile doubles as a form's schema without lbf reshaping the crate. The frame
  is part of bronze's version. Files refer to nothing, so they join the tree
  after framing, whose time grows with the square of the entities.
- **A property's type and unit are its profile's.** `--property` gives text;
  the profile's `type` makes it a number or true/false in the crate, and
  `x-unitCode` and `x-unitText` become schema.org's `unitCode` (a unit's URL,
  such as QUDT's) and `unitText`. JSON Schema has no word for a unit, so these
  are annotations named after the terms they fill. `lbf fetch --json` gives
  properties back as text, as a `--provenance` file takes them.
- **A dataset keeps the exact schemas it met, as files.** Each is
  `ro-crate-profiles/<name>/<version>/profile.json` beside the published folder,
  a `File` with its `sha256`, linked from `conformsTo` through the Profiles
  Vocabulary (`hasResource`, a `ResourceDescriptor` in the schema role,
  `hasArtifact`), as an RO-Crate profile crate describes its own. The files are
  in the root's `hasPart`, which RO-Crate requires of every file and which
  earlier lbf versions can still fetch; profiles see only the published files
  there. A schema that changed under the same `$id` is therefore a different
  file, and resuming a publish with it is refused. `ro-crate-` matches
  `ro-crate-metadata.json`, and a published folder of that name is refused;
  `profiles` alone is a common folder name in cell painting.
- **A profile's `$id` ends in `/<name>/<version>`, and a version never
  changes.** The name and version say where the dataset keeps the schema. IDs
  are GitHub URLs of the folder holding the profile, as bronze's is, so they
  resolve without registering a namespace.
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
- **On Windows, lbf is `lbf.ps1`, a PowerShell port that must behave
  byte for byte as the Go lbf does.** Managed Windows machines at Imperial
  blocked the unsigned `lbf.exe`, so the same commands, flags, messages, exit
  codes, crates and blobs come from a script. The Go code stays the specification, and `conformance_*_test.go`
  runs both on the same input on Windows PowerShell 5.1, PowerShell 7 and Linux.
  The installer sets only the execution policy of the user's own account, and
  stops where Group Policy forbids unsigned scripts rather than work round it.
  `lbf.cmd` is there for Command Prompt; PowerShell picks `lbf.ps1` over it in
  the same folder, so output can be captured. `lbf upgrade` replaces the
  script, and `lbf browse` serves on a socket listener, which, unlike
  Windows' HTTP listener, needs no URL reservation for a user without admin
  rights.
