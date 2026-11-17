# Contributing to orrery

This file explains how to work on orrery as the tree stands: the dev shell, the `just` targets, the lint and coverage standard, CI, generated code, the remote gate box, the test conventions, and how fixtures are recorded. The commands named here are recipes in `justfile`, scripts in `scripts/`, or tools the dev shell provides. Running `just` with no argument lists the recipes.

## Dev shell

The Go toolchain comes from a devenv shell. `devenv.nix` imports `devenv-base.nix`, and `devenv.yaml` pins nixpkgs. Enter the shell with:

```bash
devenv shell
```

The shell provides the Go 1.26 toolchain, golangci-lint, buf, sqlc, goose, just, tmux, rsync, the protoc plugins for Go and Connect, and the web tools (node, esbuild, typescript).

- Go's module and build caches live under `$HOME/.cache/orrery`, shared by every checkout on the machine.
- golangci-lint's cache is `.golangci-cache/` inside the checkout. Its results are keyed by import path, so a shared cache would replay one checkout's results in another.
- The shell sets `SHELL` to bash. Tests that attach tmux clients through script(1) run their command with `$SHELL`.

`scripts/in-devenv` runs its arguments in bash with the devenv environment loaded from `.devenv-shell-env.sh`:

```bash
scripts/in-devenv -c "go test ./internal/store/..."
```

The script regenerates that snapshot with `devenv print-dev-env` when the snapshot is missing or older than `devenv.yaml`, `devenv.lock` or a top-level `*.nix` file. `scripts/in-devenv --refresh` and `just refresh-env` regenerate it unconditionally. The justfile sets `scripts/in-devenv` as its shell, so every recipe runs in the devenv environment. `just check` therefore works without an entered shell, as long as `just` and `devenv` are on `PATH`.

The flake's only dev shell is `.#android`, for `x86_64-linux`. It holds the Android SDK (platform 37.0, build-tools 36.0.0, platform-tools 37.0.1), Gradle 9 and JDK 17. `just android-test` and `just android-install` enter it themselves. Use `nix develop .#android` to work in it directly.

The nixpkgs revision is written as a literal in both `devenv.yaml` and `flake.nix`, and the two must match. `just bump-nix [REF]` moves both literals, refreshes both locks with their own tools, regenerates the snapshot and runs `pin-check`. Do not edit the locks by hand. `pin-check` compares the two lock files with each other. It does not compare the two literals.

## Just targets

### Gate

- `just check` is the full gate. It runs, in order: `reap-test-tmux`, `pin-check`, `doc-cites`, `lint`, `proto-lint`, `proto-breaking`, `cover`, `sqlc-diff`, `gen-diff`, `web-check`, `web-page`, `alert-check`, `gate-remote-test` and `test-deadline-test`. Run it when you want the whole gate; nothing runs it on push. CI runs it.
- `just check-changed [BASE]` is the fast gate for work in progress. It runs only what the changes since `BASE` can affect. `BASE` defaults to the merge base of `HEAD` and `origin/main`. Go lint and race tests run on the packages that changed and on every package that imports them. A change to `go.mod`, `go.sum`, `sqlc.yaml`, `internal/store` SQL or `proto/` selects every package. `sqlc-diff`, the proto checks, `gen-diff`, the web checks, `alert-check`, `android-test` (when `android/` or `proto/` changed) and the two script self-tests run when their inputs changed. `pin-check` always runs. It does not measure coverage.
- `just lint` runs `golangci-lint run` on the whole tree. See [Lint and coverage](#lint-and-coverage).
- `just cover` runs the tests with the race detector and fails below 80% total coverage. See [Lint and coverage](#lint-and-coverage).
- `just proto-lint` runs `buf lint` in `proto/`.
- `just proto-breaking` runs `buf breaking` against the newest tag that does not point at `HEAD`. It fails when no such tag exists, so a clone without tags needs `git fetch --tags`. CI fetches tags.
- `just sqlc-diff` runs `sqlc diff`. It fails when `internal/store/sqlcgen` does not match `internal/store/queries.sql` and the migrations.
- `just gen-diff` regenerates everything, then fails when the output differs from what is committed. See [Generated code](#generated-code).
- `just pin-check` fails when `devenv.lock` and `flake.lock` resolve nixpkgs to different revisions.
- `just doc-cites` fails when `docs/` cites code by `file:line`. Cite the enclosing symbol instead.
- `just web-check` typechecks `web/` with `tsc`, bundles every `*.test.ts` and `*.test.tsx` under `web/src` with esbuild, and runs the bundles with `node --test`. Each test has a 60-second limit. It runs offline, using the `web/node_modules` that the shell links from nix.
- `just web-page` bundles the web page into `web/build/page`, the directory the flake package embeds. `scripts/build-web.sh` holds the bundle command, and the flake calls it too.
- `just alert-check` runs `promtool test rules` on `observability/orrery-rules.test.yml`.
- `just gate-remote-test` tests `scripts/gate-remote.sh` against a fake box, with no AWS and no network. `just test-deadline-test` tests `scripts/test-deadline.sh`.
- `just reap-test-tmux` stops tmux servers that tests started and outlived. `check` and `check-changed` run it first. See [Test conventions](#test-conventions).

### Generation and setup

- `just generate` regenerates the store's typed queries, the Go API, the Android client and the web client from `proto/` and the SQL. See [Generated code](#generated-code).
- `just refresh-env` rebuilds `.devenv-shell-env.sh` now, even when it is fresh.
- `just bump-nix [REF]` moves the nixpkgs pin. `REF` is a 40-character revision, or a branch name that `git ls-remote` resolves on github.com/NixOS/nixpkgs. It defaults to `nixpkgs-unstable`. It needs the network.

### Android

- `just android-test` assembles the debug APK, then runs Android lint and the JVM unit tests in `android/`. CI runs it when `android/`, `proto/`, `flake.nix` or `flake.lock` changed. `just check` does not run it.
- `just android-install` builds the debug APK and installs it on a connected device with `adb install -r`. The phone must be paired and connected first.
- `just android-font` regenerates the Orrery Mono font files (Regular, Bold and Italic) in `android/app/src/main/res/font/`. It subsets Maple Mono NF from the flake's pinned nixpkgs and renames it, because the OFL reserves the Maple Mono name.

### Remote gate box

- `just remote <command> [args...]` runs a command on the `orrery-gate` EC2 box. Example: `just remote just check`.
- `just remote-check` is `just check` on the box.
- `just remote-android-test` is `just android-test` on the box.
- `just remote-flake-check` is `nix flake check` on the box.

See [The remote gate box](#the-remote-gate-box).

### Engine and screenshots

- `just engine-accept [ARGS]` reruns the Claude Code protocol probes live against the `claude` on `PATH`. The probes spend Haiku turns on your sign-in, so this target is not part of `just check`. Pass `-probe NAME` to run one probe, and `-keep DIR` to keep the raw output in `DIR`, outside the checkout. It writes no fixtures and exits 1 when a probe fails its checks. See [Fixtures](#fixtures).
- `just measure-restart-cost` measures how soon a restart over a large store serves `sessions.sock`. It runs `TestMeasureRestartCost`, which has the `measure` build tag, so it is never part of `check`. Run it on an idle machine, because load inflates the store writes it times.
- `just tui-shots` regenerates `docs/img/tree.png`. It builds `cmd/orrery`, starts the made-up fixture in `docs/tui/fixture.sh` on a private tmux server, and renders it with VHS under `devenv --profile tui-shots shell`. The image is replaced only when the rendered frame shows every project and pane label of the fixture. The `tui-shots` profile adds VHS, ttyd and ffmpeg, which the default shell does not carry.

## Lint and coverage

`.golangci.yml` configures golangci-lint v2, and `just lint` runs it on the whole tree. The configuration enables many linters. Some are relaxed for test files and for `cmd/` (the `exclusions` block). These are the rules most likely to reject new code:

- `forbidigo` rejects `fmt.Print*`, `print`, `println`, `panic`, `time.Sleep`, and identifiers ending in `V2`, `v2`, `Old`, `Deprecated`, `Legacy` or `Compat`, and identifiers beginning with `Old`. It does not run on test files or `cmd/`. Delete replaced code instead of renaming it.
- `depguard` rejects the standard `log` package outside files named main.go, and `math/rand` outside test files. Use `log/slog` and `math/rand/v2`.
- `errcheck` rejects discarded errors (`_ = f()`) and unchecked type assertions. `fmt.Fprint*` and `Close` on files are excluded.
- `wrapcheck` rejects returning an error from another package without wrapping it.
- `godox` rejects `TODO`, `FIXME`, `XXX`, `HACK`, `BUG` and `OPTIMIZE` comments.
- `nolintlint` requires each `//nolint` to name one linter and give a reason. `funlen`, `gocognit` and `golines` may omit the reason.
- `gochecknoglobals` and `gochecknoinits` reject package-level variables and `init` functions.
- `ireturn` rejects returning an interface unless it is on the allow-list in `.golangci.yml`, such as `error` or `io.Reader`.
- Size and complexity limits, as `.golangci.yml` sets them: `funlen` 100 lines and 50 statements, `cyclop` max-complexity 30 (package average 10), and min-complexity 20 for `gocognit`, 15 for `gocyclo` and 5 for `nestif`. A function at or past a linter's threshold may be reported, so keep well under each.
- `varnamelen` rejects short names outside its ignore list. `mnd` rejects magic numbers outside its ignore list.
- `tagliatelle` requires camelCase JSON tags and snake_case YAML tags. `musttag` requires tags on values passed to `encoding/json`.
- `sloglint` requires static log messages, snake_case keys and no global logger.
- `godot` requires declaration comments to end with a period.

Formatting is part of the same configuration. `goimports` groups imports from `github.com/joshsymonds/orrery` apart from the rest, and `golines` wraps at 120 columns. `golangci-lint fmt` applies both, and `golangci-lint fmt --diff` shows the changes without writing them.

`just cover` runs `go test -count=1 -race -covermode=atomic -coverpkg=./... ./...`, with the flags that write the coverage profile and the test artifacts, and with the deadline wrapper described below. `-coverpkg=./...` attributes coverage across packages, so a test counts toward every package it exercises. The recipe then:

- drops the lines under `/cmd/` and `/internal/gen/` from the profile, because entry points and generated code are not counted;
- computes one total over the rest of the code with `go tool cover -func`, and fails when it is below 80.0%;
- fails when a tmux `-L` socket is left in the run's private tmux directory, so a test that leaves its server running is caught.

The run has a wall-clock budget of 360 seconds, set by `TEST_BUDGET` at the top of `justfile`. `scripts/test-deadline.sh` gives each test binary a `-test.timeout` that ends 10 seconds before the budget does, so a package still running then panics and names its running tests. A run that passes the budget fails with `tests exceeded 360s budget`. Override the budget for one run with `just TEST_BUDGET=600 cover`. The coverage profile is written to `coverage.out` and `coverage.cov.out`, and each test's artifacts to `test-artifacts/`. All three are git-ignored.

## CI

There are no git hooks: neither a commit nor a push runs a check. Run `just check-changed`, `just check` or `just remote-check` when you want them; an epic's gates run them for its tasks and its candidate.

CI is `.github/workflows/ci.yml`. It runs on pushes to `main` and on every pull request, as three jobs that run in parallel:

1. `check` runs `TMPDIR=/tmp just check`, after installing the devenv CLI at the revision `devenv.lock` pins.
2. `flake` runs `nix flake check`.
3. `android` runs `just android-test`, only when `android/`, `proto/`, `flake.nix` or `flake.lock` changed since the base commit or the pull request's base.

A push can pass the hook and still fail CI's flake or Android job.

## Generated code

Each generated file starts with a header that names its generator. Do not edit generated files by hand. Change the input and run `just generate`.

| Input | Output | Generator |
| --- | --- | --- |
| `proto/orrery/v1/*.proto` and `proto/orrery/engine/v1/*.proto` | `internal/gen/orrery/` | buf with protoc-gen-go and protoc-gen-connect-go (`proto/buf.gen.yaml`) |
| `proto/orrery/v1/*.proto` | `android/app/src/main/java/io/github/joshsymonds/orrery/gen/` | buf with Java lite and connect-kotlin (`proto/buf.gen.android.yaml`) |
| `proto/orrery/v1/*.proto` | `web/src/gen/` | buf with protoc-gen-es (`proto/buf.gen.web.yaml`) |
| `internal/store/queries.sql` and `internal/store/migrations/` | `internal/store/sqlcgen/` | sqlc (`sqlc.yaml`) |

The proto files carry no `go_package` option. Managed mode in `proto/buf.gen.yaml` sets it from a prefix.

`just gen-diff` runs `generate`, then fails when:

- a tracked file under `internal/gen` differs from the git index. Stage the regenerated files with `git add internal/gen` before you run it. Untracked files under `internal/gen` are not checked.
- the Android or web output differs from `HEAD`. Commit it. Staged changes are not enough, because these two trees are compared with `HEAD`.
- the Android or web output has untracked files.

`gen-diff` does not check `internal/store/sqlcgen`. `just sqlc-diff` does. After a `gen-diff` failure, the working tree already holds the regenerated files.

After changing `proto/`, run `just generate`, then `just proto-lint`, `just proto-breaking` and `just check`.

## The remote gate box

`scripts/gate-remote.sh` runs a gate command on an `orrery-gate` EC2 box, so heavy checks leave your machine's cores alone. `just remote` is its entry point. It runs the command with `scripts/in-devenv` on the box, through a systemd user scope, and streams the output back.

What a run sends and where it runs:

- It pushes `HEAD`, `origin/main` and every tag into the box's object store, with `--no-verify`, so no hook runs.
- It syncs the work tree with rsync. Files that are not git-ignored are sent, whether or not they are committed, and `.git` and `.worktrees/` are not. The box's checkout gets `HEAD` as its index, plus your staged changes.
- It holds a slot before the command starts. `just check`, `just android-test` and `just cover` share the full-check slots, whose default count is the box's CPU count divided by 20, at least 1. Every other command shares the light-check slots, whose default count is the CPU count divided by 8, at least 1. A run waits in a queue when every slot of its class is held.
- The run lives on the box apart from its ssh connections. When a connection drops while the box stays up, the script reconnects and shows the rest of the output from where it stopped, then the run's own exit status, so a dropped connection costs no run.
- The command's exit status is the run's. Exit 75 is an environment result: the box went away (a spot interruption), the connections stayed down for `ORRERY_GATE_COLLECT_SECS` seconds (default 120), or the run was killed on the box. It is never a test failure. Run again.

Finding the box: when `ORRERY_GATE_DEST` is set, it is the destination, and a run whose destination does not answer runs locally without a wake. Otherwise the destination is the one line of the host file, `ORRERY_GATE_HOST_FILE`, which defaults to `orrery/gate-host` under `XDG_CONFIG_HOME` (or `~/.config`). When the host file's box does not answer, `scripts/gate-remote.sh wake` finds a running box or launches one with `ec2 up orrery-gate --spot-only`.

When no box can come up, the command runs on the local machine. `ORRERY_GATE_LOCAL` sets the prefix for a local run. When it is unset, the script uses its built-in local wrapper if that is executable, and otherwise runs the command directly. `ORRERY_GATE_REMOTE=0` always runs locally. The header of `scripts/gate-remote.sh` lists the rest of its environment variables and exit codes.

## Test conventions

These conventions are enforced by the lint configuration, the recipes, or a test.

- Tests run with `-count=1`, so a cached pass never hides a rerun. `cover` and `check-changed` both pass it.
- The `cover` and `check-changed` recipes run the tests with `TMUX` and `TMUX_PANE` unset.
- A test that starts a tmux `-L` server stops it with `tmuxtest.Stop`, which kills the server and removes its socket. A test that leaves a socket behind fails `cover`.
- `just reap-test-tmux` stops the servers of test binaries that were killed or timed out. It recognises a server by its `-L` name, `orrery-<word>(-<word>)*-<pid>-<hex>`, such as `orrery-test-<pid>-<hex>`, and stops it only when the test binary `<pid>` is gone. Name a test's server in that form so an orphan can be recognised.
- Tests that start processes write their logs under `t.ArtifactDir()`. `cover` and `check-changed` pass `-artifacts -outputdir test-artifacts`, so the logs survive a failed run.
- `thelper`, `tparallel` and `usetesting` apply to tests. Helpers call `t.Helper()`, and tests use `t.TempDir()` rather than `os.TempDir()`.
- Test files are exempt from a list of linters, among them `forbidigo`, `errcheck`, `wrapcheck`, `varnamelen`, `funlen`, `cyclop`, `gocognit` and `gosec`. The `exclusions` block of `.golangci.yml` has the full list.
- Recorded and hand-written test data use invented values only: `/home/user/project` for paths, `example-*` for names, `example.test` for email addresses, `192.0.2.0/24` for IPv4 addresses, and the fixed UUID form `00000000-0000-4000-8000-00000000000N` for ids. `TestEngineHoldsNoPrivateDetail` (`internal/engine/leak_test.go`) scans every file under `internal/engine`, testdata included. It fails on any other home path, a tailnet host name, an IPv4 address outside 127.0.0.0/8 and 192.0.2.0/24, an email address outside `example.test`, a UUID outside the fixed form, an `sk-ant-` token, or a `msg_` id. It prints each finding with its file and line.
- Golden files hold the expected output of some tests: `internal/cli/testdata/list.golden.json`, `internal/help/testdata/card.golden` and `page.golden`, `internal/tmuxconf/testdata/plugin.golden`, and the five frames in `internal/tui/testdata/`. No test has a flag or environment variable that rewrites a golden file. To change one, write the new output into the file yourself. `checkGolden` in `internal/tui/tui_test.go` expects the frame followed by one newline.
- The help card is checked in two more places. `TestHelpPrintsCardWithServerPrefix` in `internal/integration` compares the output of `orrery help` with `card.golden`. `TestReadmeDailyUseMatchesCard` in `internal/help` requires the `Daily use` block of `README.md` to equal `help.Card`. Changing the card therefore means changing `README.md` too.

## Fixtures

Only one set of fixtures is recorded: the Claude Code stream-json probes in `internal/engine/capture/testdata/`. The other `testdata` directories under `internal/` hold golden files, fake programs and JSON payloads that were written by hand.

Each of the six probes (`two-turns`, `interrupt`, `mode-change`, `question`, `permission-allow` and `permission-deny`) has two fixtures. `<probe>.in.ndjson` holds the lines the host sent, and `<probe>.out.ndjson` holds the lines Claude Code printed. `docs/engine-protocol.md` records what the probes show, and its section 1 describes how they run.

To record new fixtures you need a signed-in `claude` on `PATH`. Recording spends Haiku turns on your sign-in. Each probe runs with your real `HOME`, so your own plugins, skills and agents load. Keep the raw directory outside the checkout:

```bash
go run ./internal/engine/capture/cmd/enginecapture record -raw DIR
go run ./internal/engine/capture/cmd/enginecapture sanitize -raw DIR -out internal/engine/capture/testdata
```

`record` takes `-probe NAME` to record one probe. It creates the raw directory with mode 0700 and its files with mode 0600. The raw directory also holds each probe's stderr, which is not sanitised, and nothing in `.gitignore` excludes it. Delete it when you are done.

`sanitize` reads the raw output of every probe in the raw directory and writes both fixtures of each one. Both streams of a probe share one sanitiser, so an id has the same made-up value in each. The rules keep only allow-listed values. Any other string becomes `redacted`, any other number becomes 0, and any other boolean becomes false. Ids become `<prefix>_<n>`, numbered from 1 for each probe. Keys of some objects, such as the answer to a question, become `key_<n>`. Lines of type `rate_limit_event` are dropped. `docs/engine-protocol.md`, section 1, lists the rules, and `internal/engine/capture/sanitise.go` implements them.

Three tests in `just check` hold the fixtures to this:

- `TestFixturesResanitiseUnchanged` (`internal/engine/capture/sanitise_fixtures_test.go`) fails when running the sanitiser over the committed fixtures changes them.
- `TestFixturesDecodeAsWireMessages` (`internal/engine/capture/fixtures_test.go`) fails when a fixture line does not decode to a declared wire message.
- `TestEngineHoldsNoPrivateDetail` (`internal/engine/leak_test.go`) scans the fixtures for private detail, as described under [Test conventions](#test-conventions).

To run only these after recording, use:

```bash
scripts/in-devenv -c "go test -count=1 -run 'TestFixtures|TestEngineHoldsNoPrivateDetail' ./internal/engine/..."
```

`just engine-accept` is the live check. It reruns the probes against the `claude` on `PATH` and checks each probe's stdout. It writes no fixtures.

`docs/tui/fixture.sh` builds the made-up tmux world that `just tui-shots` captures. Its projects and panes are invented, and the image is published, so do not add real project names or paths to the fixture.
