# Building and testing

Pushing a `v*` tag builds every platform and publishes a release.

```
npx -p azurite azurite-blob --inMemoryPersistence &   # optional: the publish tests skip without it
go test ./...
LBF_AZURITE=1 LBF_PS=pwsh go test -run TestConformance ./cmd/lbf   # lbf.ps1 against the Go lbf
go run ./tools/buildps -version v0.2.0 -o dist/lbf.ps1      # what Windows installs
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/lbf-windows-amd64.exe ./cmd/lbf
```
