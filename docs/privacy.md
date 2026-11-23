# Privacy

lbf sends no telemetry. It only contacts other systems to do what you asked:

- Microsoft Entra ID, to sign you in (`lbf login`, or when a command needs Azure access).
- Azure Resource Manager and the Azure Storage accounts you choose, to list, publish and fetch datasets.
- GitHub, to check for a newer release at most once an hour (turn off with
  `LBF_NO_UPDATE_CHECK=1`) and to download one when you run `lbf upgrade`.

Sign-ins saved by `lbf login` stay on your machine, in the macOS Keychain or
an encrypted file in `%LOCALAPPDATA%\.IdentityService` on Windows, until
`lbf logout`.
