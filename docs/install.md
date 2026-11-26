# Installing, updating and uninstalling

The install commands are in the [README](../README.md#install).

## Windows

Nothing needs installing first: `irm` and `iex` are built into PowerShell
(short for `Invoke-RestMethod` and `Invoke-Expression`), and both Windows
PowerShell 5.1 and PowerShell 7 work. The first part turns on TLS 1.2, which
GitHub requires and older Windows PowerShell does not use by default. No admin
rights are needed. The script puts `lbf.ps1`, and `lbf.cmd` for Command Prompt,
in `%LOCALAPPDATA%\lbf` and adds that folder to your PATH, so `lbf` works
straight away in that window and in new ones, in PowerShell and in Command
Prompt alike. It replaces an `lbf.exe` an earlier install left there. Run it
again to reinstall, even while lbf is running. Then run `lbf login`.

On Windows lbf is a PowerShell script. Typed in PowerShell, `lbf` runs
`lbf.ps1` itself, so `$id = lbf new-id` and PowerShell's quoting work as for
any command; Command Prompt runs `lbf.cmd`, which starts Windows PowerShell on
`lbf.ps1`.

PowerShell runs scripts only if its execution policy allows them, and Windows
ships refusing them. If nothing else has set it, the installer sets the policy
for your account to `RemoteSigned` (local scripts run, downloaded ones must be
signed) and says so. If your organisation's Group Policy allows only signed
scripts (`AllSigned` or `Restricted`), the installer stops without installing
anything and says to ask IT to allow `RemoteSigned` for your account; lbf
never tries to get round Group Policy.

## macOS and Linux

This puts `lbf` in `~/.local/bin` (override with `LBF_INSTALL_DIR`) and, if
that folder is not already on your PATH, adds it via `~/.profile` and your
bash, zsh and fish startup files, the same way uv and rustup do, sharing their
`env` script when they use the same folder. If the folder already holds an
`env` or `env.fish` that is not such a script, the startup files are left
alone. Open a new terminal afterwards. Set `LBF_NO_MODIFY_PATH=1` to leave
startup files alone.

## What the installers check

Both installers check the download against the release's `SHA256SUMS` before
installing it, as `lbf upgrade` does. On macOS and Linux they and `lbf upgrade`
fetch the build for the machine's own processor, even from a terminal emulated
on Arm; `lbf.ps1` runs on any Windows processor.

Binaries, `lbf.ps1`, and the Windows exes earlier installs used are also
attached to each [release](https://github.com/ImperialCollegeLondon/lbf-cli/releases).

## Update

```
lbf upgrade
```

On Windows this replaces `lbf.ps1` with the release's, checked against
`SHA256SUMS`; an install that still has `lbf.exe` gets the newer exe, and the
install command above moves it to `lbf.ps1`.

lbf checks for a newer release at most once an hour and prints a warning after
any command when it is out of date. Set `LBF_NO_UPDATE_CHECK=1` to turn the
check off. Pre-releases (tags such as `v0.2.0-rc1`) are never offered; anyone
running one is offered the release that follows it.

## Uninstall

Run `lbf logout` first to delete saved sign-ins.

Windows: delete the `%LOCALAPPDATA%\lbf` folder (it holds `lbf.ps1` and
`lbf.cmd`) and remove that folder from your user PATH (Start menu, "Edit
environment variables for your account"). The installer may have let your
account run local scripts; `Set-ExecutionPolicy -Scope CurrentUser Undefined`
in Windows PowerShell undoes that, if nothing else of yours needs it.

macOS / Linux: delete `~/.local/bin/lbf` (or the file in `LBF_INSTALL_DIR`).
The PATH line the installer added to your startup files can stay or be removed.
