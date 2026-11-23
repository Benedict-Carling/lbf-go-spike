# Integrity

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
