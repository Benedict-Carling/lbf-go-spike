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

case $dir in
    "$HOME"/*) dir_expr="\$HOME/${dir#"$HOME"/}" ;;
    *) dir_expr=$dir ;;
esac
env_sh="$dir/env"
env_fish="$dir/env.fish"
changed=""
failed=""

# Same env script and rc lines as uv and rustup, so installing several of them does not stack duplicates.
write_env() {
    if [ ! -f "$env_sh" ]; then
        { printf '#!/bin/sh\ncase ":${PATH}:" in\n    *:"%s":*) ;;\n    *) export PATH="%s:$PATH" ;;\nesac\n' "$dir_expr" "$dir_expr" > "$env_sh"; } 2>/dev/null || failed=1
    fi
    if [ ! -f "$env_fish" ]; then
        { printf 'if not contains "%s" $PATH\n    set -x PATH "%s" $PATH\nend\n' "$dir_expr" "$dir_expr" > "$env_fish"; } 2>/dev/null || failed=1
    fi
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
    write_env
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
        */fish) echo "Add $dir to your PATH: fish_add_path \"$dir\"" ;;
        */zsh) echo "Add $dir to your PATH: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
        */bash) echo "Add $dir to your PATH: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc" ;;
        *) echo "Add $dir to your PATH in your shell's startup file: export PATH=\"$dir:\$PATH\"" ;;
    esac
}

case ":$PATH:" in
    *":$dir:"*) echo "Next: lbf login"; exit 0 ;;
esac

if [ "${LBF_NO_MODIFY_PATH:-0}" = 1 ]; then
    manual_hint
    echo "Then: lbf login"
    exit 0
fi

modify_path
if [ -n "$changed" ]; then
    echo "Added $dir to your PATH in:$changed"
fi
if [ -n "$failed" ]; then
    echo "Could not update all of your shell startup files."
    manual_hint
else
    echo "Open a new terminal, or run this in the current one:"
    case ${SHELL:-} in
        */fish) echo "    source \"$dir/env.fish\"" ;;
        *) echo "    . \"$dir/env\"" ;;
    esac
fi
echo "Then: lbf login"
