# lbf (Go spike)

A single-binary replacement for the Nextflow upload, download and mint-sas
pipelines. No Nextflow, Java, Python, bash or azcopy; runs natively on macOS,
Linux and Windows.

## Use

```
az login
lbf upload <path>              # prints the new dataset ID
lbf download <id> [--out DIR]
lbf mint-sas --mode upload     # writes azure_sas.env for a machine without az (HPC)
lbf upload <path> --sas-env azure_sas.env
```

`--tag KEY=VALUE` picks the storage account (default `tag=storage`; the test
account is `role=bronze-test`). `--container` defaults to `bronze`.

Blob layout, RO-Crate sidecar and `azure_sas.env` format match the Nextflow
pipelines, so either tool can read the other's output.

## Build

```
go test ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/lbf-windows-amd64.exe .
```
