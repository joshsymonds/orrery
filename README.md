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

## Build and check

```sh
nix develop        # enter the devShell with the Go toolchain and just
just check         # lint and race tests, with the 80% coverage gate
nix flake check    # evaluate and check the flake
nix build          # build the packages
```

## Android

The Android app lives under `android/`. It is a Kotlin and Jetpack Compose
app. Only debug builds are produced; no signing key or keystore is in the tree.

```sh
nix develop .#android      # enter the android devShell with the SDK, Gradle, and JDK 17
just android-test          # run the Android lint and JVM unit tests
just android-install       # build the debug APK and install it on the phone
```

`just android-install` needs the phone paired and connected with adb first.
The server address setting defaults to `https://vermissian.tail82223.ts.net:8490`.

## Interface

The frozen interface between the hook, the CLI, and the daemon is documented in
[`docs/contract.md`](docs/contract.md).

## License

MIT. See [`LICENSE`](LICENSE).
