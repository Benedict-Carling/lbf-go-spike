# Building and testing

What a change must pass before it is merged is in
[AGENTS.md](../AGENTS.md#checking-a-change): the tests against Azurite, real
publishes on the test account, and the RO-Crate validator. Pushing a `v*` tag
builds every platform and publishes a release.

With Azurite running (`npx -p azurite azurite-blob --inMemoryPersistence --skipApiVersionCheck`):

```
LBF_AZURITE=1 go test ./...
LBF_AZURITE=1 LBF_PS=pwsh go test -run TestConformance ./cmd/lbf   # lbf.ps1 against the Go lbf
LBF_PS_SPAWN=1 LBF_AZURITE=1 LBF_PS=pwsh go test -run TestConformance ./cmd/lbf   # the same, starting PowerShell for every run
```

Building by hand:

```
go build -o lbf ./cmd/lbf
go run ./tools/buildps -version v0.2.0 -o dist/lbf.ps1      # what Windows installs
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/lbf-windows-amd64.exe ./cmd/lbf
```
