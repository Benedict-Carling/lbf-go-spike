#!/bin/sh
set -eu

repo=Benedict-Carling/lbf-go-spike

detect_platform() {
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
    # A shell running under Rosetta reports x86_64 on Apple Silicon.
    if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
        arch=arm64
    fi
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 -r "$1" | awk '{print $1}'
    fi
}

download() {
    asset="lbf-$os-$arch"
    base="https://github.com/$repo/releases/latest/download"
    echo "Downloading lbf for $os $arch..."
    curl -fsSL "$base/$asset" -o "$dir/lbf.new"
    want=$(curl -fsSL "$base/SHA256SUMS" | awk -v n="$asset" '$2 == n || $2 == "*" n {print $1}')
    got=$(sha256_of "$dir/lbf.new")
    if [ -z "$want" ] || [ -z "$got" ] || [ "$want" != "$got" ]; then
        rm -f "$dir/lbf.new"
        if [ -z "$want" ]; then
            echo "lbf: could not fetch the published checksum for $asset; nothing was installed. Try again." >&2
        elif [ -z "$got" ]; then
            echo "lbf: cannot verify the download: install sha256sum, shasum or openssl" >&2
        else
            echo "lbf: the download does not match its published checksum; nothing was installed. Try again." >&2
        fi
        exit 1
    fi
    chmod +x "$dir/lbf.new"
    mv "$dir/lbf.new" "$dir/lbf"
}

# Same env script and rc lines as uv and rustup, so installing several of them does not stack duplicates.
write_env() {
    sh_path_line="export PATH=\"$dir_expr:\$PATH\""
    fish_path_line="set -x PATH \"$dir_expr\" \$PATH"
    if ! ours "$env_sh" "$sh_path_line" "export PATH=\"$dir:" || ! ours "$env_fish" "$fish_path_line" "set -x PATH \"$dir\" "; then
        echo "lbf: $dir already holds an env or env.fish script that does not add $dir to PATH, so your shell startup files were left alone." >&2
        return 1
    fi
    if [ ! -e "$env_sh" ]; then
        { printf '#!/bin/sh\ncase ":${PATH}:" in\n    *:"%s":*) ;;\n    *) %s ;;\nesac\n' "$dir_expr" "$sh_path_line" > "$env_sh"; } 2>/dev/null || return 1
    fi
    if [ ! -e "$env_fish" ]; then
        { printf 'if not contains "%s" $PATH\n    %s\nend\n' "$dir_expr" "$fish_path_line" > "$env_fish"; } 2>/dev/null || return 1
    fi
}

# An existing script is reused only if it is one of these for this folder, as uv's and rustup's are.
ours() {
    [ ! -e "$1" ] || { [ -f "$1" ] && grep -qF -e "$2" -e "$3" "$1"; }
}

add_line() {
    if grep -qF "$2" "$1" 2>/dev/null; then
        return 0
    fi
    if { printf '\n%s\n' "$2" >> "$1"; } 2>/dev/null; then
        changed="$changed $1"
    else
        failed=1
    fi
}

modify_path() {
    if ! write_env; then
        failed=1
        return 0
    fi
    sh_line=". \"$dir_expr/env\""
    add_line "$HOME/.profile" "$sh_line"
    for f in .bashrc .bash_profile .bash_login; do
        if [ -f "$HOME/$f" ]; then
            add_line "$HOME/$f" "$sh_line"
        fi
    done
    case ${SHELL:-} in
        */bash) if [ ! -f "$HOME/.bashrc" ]; then add_line "$HOME/.bashrc" "$sh_line"; fi ;;
    esac
    zdir=${ZDOTDIR:-$HOME}
    if [ -f "$zdir/.zshrc" ]; then
        add_line "$zdir/.zshrc" "$sh_line"
    elif [ -f "$zdir/.zshenv" ]; then
        add_line "$zdir/.zshenv" "$sh_line"
    else
        case ${SHELL:-} in
            */zsh) add_line "$zdir/.zshrc" "$sh_line" ;;
        esac
    fi
    fish_conf="${XDG_CONFIG_HOME:-$HOME/.config}/fish"
    if [ -d "$fish_conf" ] || command -v fish >/dev/null 2>&1; then
        if mkdir -p "$fish_conf/conf.d" 2>/dev/null; then
            add_line "$fish_conf/conf.d/lbf.env.fish" "source \"$dir_expr/env.fish\""
        else
            failed=1
        fi
    fi
}

manual_hint() {
    case ${SHELL:-} in
        */fish) echo "To use plain lbf, add $dir to your PATH: fish_add_path \"$dir\"" ;;
        */zsh) echo "To use plain lbf, add $dir to your PATH: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
        */bash) echo "To use plain lbf, add $dir to your PATH: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc" ;;
        *) echo "To use plain lbf, add $dir to your PATH in your shell's startup file: export PATH=\"$dir:\$PATH\"" ;;
    esac
}

get_started() {
    case $dir in
        *" "*) cmd="\"$dir/lbf\" login" ;;
        *) cmd="$dir/lbf login" ;;
    esac
    echo
    echo "Get started now:"
    echo "    $cmd"
    echo
}

main() {
    detect_platform
    dir=${LBF_INSTALL_DIR:-$HOME/.local/bin}
    mkdir -p "$dir"
    # Startup files are read from other folders, so they need an absolute path.
    case $dir in
        /*) ;;
        *) dir=$(CDPATH='' cd -- "$dir" && pwd) ;;
    esac
    download
    "$dir/lbf" version
    echo "Installed to $dir/lbf"

    case ":$PATH:" in
        *":$dir:"*) echo "Next: lbf login"; return 0 ;;
    esac

    case $dir in
        "$HOME"/*) dir_expr="\$HOME/${dir#"$HOME"/}" ;;
        *) dir_expr=$dir ;;
    esac
    env_sh="$dir/env"
    env_fish="$dir/env.fish"
    changed=""
    failed=""

    if [ "${LBF_NO_MODIFY_PATH:-0}" = 1 ]; then
        get_started
        manual_hint
        return 0
    fi

    modify_path
    if [ -n "$changed" ]; then
        echo "Added $dir to your PATH for new terminals, in:$changed"
    fi
    get_started
    if [ -n "$failed" ]; then
        echo "Could not update all of your shell startup files."
        manual_hint
    else
        echo "Or make plain lbf work in this terminal:"
        case ${SHELL:-} in
            */fish) echo "    source \"$dir/env.fish\"" ;;
            *) echo "    . \"$dir/env\"" ;;
        esac
    fi
}

# Called last so a download cut off part way runs nothing.
main "$@"
