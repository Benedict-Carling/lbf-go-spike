#!/bin/sh
set -eu

repo=Benedict-Carling/lbf-go-spike
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) echo "lbf: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
case $os in
    darwin | linux) ;;
    *) echo "lbf: unsupported OS $os (on Windows use install.ps1)" >&2; exit 1 ;;
esac

dir=${LBF_INSTALL_DIR:-$HOME/.local/bin}
mkdir -p "$dir"
echo "Downloading lbf for $os $arch..."
curl -fsSL "https://github.com/$repo/releases/latest/download/lbf-$os-$arch" -o "$dir/lbf.new"
chmod +x "$dir/lbf.new"
mv "$dir/lbf.new" "$dir/lbf"

"$dir/lbf" version
echo "Installed to $dir/lbf"
case ":$PATH:" in
    *":$dir:"*) echo "Next: lbf login" ;;
    *) echo "Add $dir to your PATH (e.g. echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc), then: lbf login" ;;
esac
