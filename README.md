# orrery

orrery is a tmux-aware status board for Claude Code sessions.

A Claude Code hook run through `orrery` reports each session's state (working,
waiting, or done) to a local daemon, `orreryd`, over a Unix socket. The daemon
records that state in per-pane tmux options, summarizes it, and can notify you
through ntfy. `orrery list` shows the panes and their states.

## Binaries

- `cmd/orrery`: the Claude Code hook and the CLI client (`hook`, `list`, `seen`, `send`, `debug summarize`).
- `cmd/orreryd`: the local daemon that holds session state and listens on a unix socket only.

## Usage

Register `orrery hook` for the Claude Code hook events in your settings, then
read the pane states with `orrery list --json`. Command lines, flags,
environment variables, exit codes, and a settings example are documented in
[section 11 of `docs/contract.md`](docs/contract.md#11-commands-and-configuration).

## Phone service

`orrery serve` exposes the pane list, pane screens, and input over HTTP, so a
phone client can read and drive the panes. It listens on a loopback address and
sits behind `tailscale serve`. The routes, guard headers, push registration, and
Web Push payload are documented in
[section 12 of `docs/contract.md`](docs/contract.md#12-http-api).

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
