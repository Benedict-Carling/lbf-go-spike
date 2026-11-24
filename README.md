# lbf

A command-line tool that publishes datasets to the lab's Azure storage with an
[RO-Crate](https://www.researchobject.org/ro-crate/) beside them, checks each
against profiles, and fetches them back.

One binary on macOS and Linux, one PowerShell script on Windows. Nothing else
needs installing.

```
lbf login
lbf publish plate1/                      # prints the new dataset ID
lbf fetch 20261001-fancy-dassie-eadb
```

## Install

Windows, in PowerShell:

```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072; irm https://github.com/ImperialCollegeLondon/lbf-cli/releases/latest/download/install.ps1 | iex
```

macOS and Linux:

```sh
curl -fsSL https://github.com/ImperialCollegeLondon/lbf-cli/releases/latest/download/install.sh | sh
```

Neither needs admin rights. Both check the download against the release's
`SHA256SUMS`, put `lbf` on your PATH, and can be run again to reinstall. Then
run `lbf login`.

`lbf upgrade` updates lbf, which warns after a command when a newer release is
out. What the installers change, and how to uninstall, is in
[docs/install.md](docs/install.md).

## Commands

| Command | What it does |
| --- | --- |
| `lbf login` | Sign in to Imperial with the browser, and remember it (macOS and Windows) |
| `lbf publish <path>` | Validate a folder and publish it as a new dataset; prints its ID |
| `lbf fetch <id> [--out DIR]` | Fetch a dataset and verify every file |
| `lbf new-id` | Print an ID to publish under later, with `publish --id` |
| `lbf browse` | Search every dataset's crate in your browser |
| `lbf check --mode upload` | Prove the credential works before a long job |
| `lbf mint-sas --mode upload` | Write `azure_sas.env` for a machine without `az`, such as HPC |
| `lbf logout` | Sign out and delete saved tokens |
| `lbf upgrade` | Update lbf to the latest release |

`lbf <command> --help` lists a command's options. Progress goes to stderr and
the result to stdout, as one line of JSON with `--json`.

## Publishing with provenance

Every dataset must meet the bronze profile before anything is uploaded. A
derived dataset also records its parent, the instruments that made it, and any
properties, and can be held to a profile of its own:

```
lbf publish results/ --container silver \
    --derived-from 20261001-fancy-dassie-eadb \
    --instrument name=my-pipeline,version=0.1.0,url=https://... \
    --property sample_id=SAM-0001 \
    --profile profiles/minimal-silver/0.1.0
```

`--dry-run` validates and prints the crate without signing in. A publish that
fails partway is finished by running it again with `--id`.

## Documentation

- [Publishing and fetching](docs/publishing.md): sign-in, provenance, profiles, JSON output, browsing, and resuming a publish.
- [Integrity](docs/integrity.md): what publish refuses and what fetch verifies.
- [Design decisions](docs/design-decisions.md): why the crate and the Windows script are as they are.
- [Installing, updating and uninstalling](docs/install.md)
- [Building and testing](docs/development.md)
- [Privacy](docs/privacy.md)
- [CONTEXT.md](CONTEXT.md): the words lbf uses.

## Licence

MIT; see [LICENSE](LICENSE).
