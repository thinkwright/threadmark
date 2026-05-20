# Installing Threadmark

Threadmark is installed once per user account, then activated in each project
you want it to observe.

Follow the steps in order.

## Requirements

- macOS or Linux
- Homebrew, or Go 1.24 or newer for the `go install` path
- Claude Code if you want Claude Code hooks
- Codex if you want Codex hooks
- A working `claude -p` command if you want journal entries written

The reflector uses the Claude CLI. Codex can emit events and receive startup
packets, but journal reflection currently goes through `claude -p`.

## 1. Install the Commands

The recommended install path is Homebrew:

```sh
brew install --cask thinkwright/tap/threadmark
```

This installs both `threadmark` and `threadmarkd`.

If you prefer `go install`:

```sh
go install github.com/thinkwright/threadmark/cmd/threadmark@latest github.com/thinkwright/threadmark/cmd/threadmarkd@latest
```

Go chooses the install directory. It uses `GOBIN` when it is set; otherwise it
uses `$(go env GOPATH)/bin`, usually `$HOME/go/bin`. You do not need to create
that directory first.

After this command completes, you should have:

```text
<go-bin-dir>/threadmark
<go-bin-dir>/threadmarkd
```

For a pinned install, replace `@latest` with a release tag.

## 2. Put Threadmark on PATH

The shell that launches Claude Code or Codex must be able to find `threadmark`.
Homebrew normally links the binaries into its bin directory automatically.

If your shell cannot find `threadmark` after a Homebrew install, check that your
Homebrew bin directory is on PATH. On Apple Silicon macOS this is usually
`/opt/homebrew/bin`; on Intel macOS it is usually `/usr/local/bin`.

For the `go install` path, if you have not customized `GOBIN`, add Go's default
bin directory:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Then check:

```sh
command -v threadmark
command -v threadmarkd
```

## 3. Verify the Install

Run:

```sh
threadmark version
threadmarkd --version
threadmark help
```

`threadmarkd` is the per-user daemon shared by all projects. It may be stopped
until `threadmark activate` or the first hook event starts it. Project
separation happens under `~/.threadmark/projects/<project-id>/`.

## 4. Activate a Project

Go to the project you want Threadmark to observe:

```sh
cd ~/code/my-project
```

Activate Threadmark:

```sh
threadmark activate
```

This writes project-local harness configuration and makes the per-user daemon
ready:

```text
.claude/settings.json
.codex/hooks.json
```

For launchers and automation:

```sh
threadmark activate --quiet
```

For user-level hook config instead of project-local config:

```sh
threadmark activate --scope user
```

## 5. Trust Codex Hooks

Codex requires explicit manual hook trust for project hooks. After activating a
project, open Codex in that project and run:

```text
/hooks
```

Review and trust the Threadmark hooks. Threadmark should not bypass this trust
step.

Claude Code does not require the same `/hooks` trust review in the current
workflow.

## 6. Run a Health Check

From the activated project:

```sh
threadmark doctor
```

`doctor` checks storage permissions, daemon reachability, project state, journal
presence, hook config, reflector mode, reflector command, and reflector prompt.
A fresh project may warn that state or journal files are missing until hook
events arrive and a checkpoint fires. That fresh-project state is expected; the
important setup signal is `0 fail`.

Known limitation: `doctor` can verify that Codex hook config exists, but it
cannot prove that Codex project hooks have been trusted in the interactive
`/hooks` UI.

## Source Checkout Install

Use this path when you are developing Threadmark from a local checkout.

From a Threadmark checkout:

```sh
go build -o bin/threadmark ./cmd/threadmark
go build -o bin/threadmarkd ./cmd/threadmarkd
bin/threadmark install
```

For a different install directory, choose a directory your user can write to:

```sh
bin/threadmark install --bin-dir /usr/local/bin
```

Then return to step 2 and make sure the chosen directory is on PATH.

## Upgrade

Repeat the install command or source checkout install. If you installed with
Homebrew, run:

```sh
brew upgrade --cask thinkwright/tap/threadmark
```

If a daemon is already running during an upgrade, restart it once so future
events use the upgraded daemon binary:

```sh
threadmark daemon restart
```

The daemon is shared across projects, so one restart updates all future hook
handling.

## Uninstall

Stop the daemon:

```sh
threadmark daemon stop
```

Remove installed binaries:

```sh
go_bin="$(go env GOBIN)"
if [ -z "$go_bin" ]; then go_bin="$(go env GOPATH)/bin"; fi
rm -f "$go_bin/threadmark" "$go_bin/threadmarkd"
```

If you installed with Homebrew instead:

```sh
brew uninstall --cask thinkwright/tap/threadmark
```

Remove project hooks manually from any project where you activated Threadmark:

```text
.claude/settings.json
.codex/hooks.json
```

Remove local Threadmark state only if you want to delete journals and daemon
data:

```sh
rm -rf "$HOME/.threadmark"
```
