# lbf

A Go CLI that publishes datasets to the lab's Azure storage with an RO-Crate beside them, checks each against profiles, and fetches them back.

- Domain words (dataset, parent, instrument, property, profile, frame, crate): `CONTEXT.md`. Use them in code, messages and docs.
- Changing the crate lbf writes, the frame, a profile, or what publish and fetch take and print: read `README.md` "Design decisions" first, and record any new decision there with its reason.
- Bronze is compiled in from `profiles/bronze/` (`profile.json` and `frame.json`). Changing either is a new bronze version, and changes what every profile built on it means.
- json-gold runs offline: the RO-Crate 1.3 context is `ro-crate-1.3-context.json`, which also serves the 1.2 URL for crates earlier lbf versions wrote.

## Checking a change

1. Start Azurite (`npx -p azurite azurite-blob --inMemoryPersistence`), then `LBF_AZURITE=1 go test ./...` passes.
2. With lbf built from the working tree first on PATH, publish, fetch and resume real datasets on the test account, `--tag tag=storage-test`. A change to the crate or profiles also runs bronze-to-silver-skeleton and cell-paint-analysis there (their AGENTS.md say how). Tests use only the test account.
3. `uvx --from roc-validator rocrate-validator validate -p ro-crate-1.3 -l required <fetched dataset>` passes on what was published.

Done when all three pass.

## Writing code

- Comments are one line, for what the code cannot show.
- Messages say what happened and what to do, in the domain words; a profile failure names the flag that fixes it.
