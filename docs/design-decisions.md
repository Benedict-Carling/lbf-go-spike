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
  byte for byte as the Go lbf does.** Imperial's Defender for Endpoint blocks
  unsigned executables (its ransomware rule stopped `lbf.exe`), but not scripts
  run by Microsoft's signed `powershell.exe`, so the same commands, flags,
  messages, exit codes, crates and blobs come from a script that needs no
  signature. The Go code stays the specification, and `conformance_*_test.go`
  runs both on the same input on Windows PowerShell 5.1, PowerShell 7 and Linux.
  The installer sets only the execution policy of the user's own account, and
  stops where Group Policy forbids unsigned scripts rather than work round it.
  `lbf.cmd` is there for Command Prompt; PowerShell picks `lbf.ps1` over it in
  the same folder, so output can be captured. `lbf upgrade` replaces the
  script, and `lbf browse` serves on a socket listener, which, unlike
  Windows' HTTP listener, needs no URL reservation for a user without admin
  rights.
