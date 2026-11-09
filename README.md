# orrery

orrery is a tmux-aware status board for Claude Code sessions.

A Claude Code hook run through `orrery` reports each session's state (working,
waiting, or done) to a local daemon, `orreryd`, over a Unix socket. The daemon
records that state in per-pane tmux options, summarizes it, and can notify you
through ntfy. `orrery list` shows the panes and their states.

## Binaries

- `cmd/orrery`: the Claude Code hook and the CLI client (`hook`, `list`, `seen`, `send`, `debug summarize`, `shell`, `tree`, `tmux-conf`, `open`).
- `cmd/orreryd`: the local daemon that holds session state and listens on a unix socket only.

## Usage

Register `orrery hook` for the Claude Code hook events in your settings, then
read the pane states with `orrery list --json`. `orrery tree` shows the panes
and `orrery open` attaches or starts a project session. The tmux plugin
(`orrery tmux-conf`) binds prefix `Space` to the tree and prefix `s` to a
directory shell, and the home-manager module (`programs.orrery`) installs
orrery and the `p` launcher. Command lines, flags, environment variables, exit
codes, key tables, and a settings example are documented in
[section 11 of `docs/contract.md`](docs/contract.md#11-commands-and-configuration).

## Build and check

```sh
nix develop        # enter the devShell with the Go toolchain and just
just check         # lint and race tests, with the 80% coverage gate
nix flake check    # evaluate and check the flake
nix build          # build the packages
```

## Interface

The frozen interface between the hook, the CLI, and the daemon is documented in
[`docs/contract.md`](docs/contract.md).

## License

MIT. See [`LICENSE`](LICENSE).
