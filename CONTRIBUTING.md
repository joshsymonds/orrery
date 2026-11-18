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

- `just check` is the full gate. It runs, in order: `reap-test-tmux`, `pin-check`, `doc-cites`, `lint`, `proto-lint`, `proto-breaking`, `cover`, `sqlc-diff`, `gen-diff`, `web-check`, `web-page`, `alert-check`, `gate-remote-test`, `box-cache-test`, `test-deadline-test` and `test-budget-test`. Run it when you want the whole gate; nothing runs it on push. CI runs it.
- `just check-changed [BASE]` is the fast gate for work in progress. It runs only what the changes since `BASE` can affect. `BASE` defaults to the merge base of `HEAD` and `origin/main`. Go lint and race tests run on the packages that changed and on every package that imports them. A change to `go.mod`, `go.sum`, `sqlc.yaml`, `internal/store` SQL or `proto/` selects every package. The whole run has a 240-second budget, and each tested package's CPU time is checked against its baseline. See [Test time budgets](#test-time-budgets). `sqlc-diff`, the proto checks, `gen-diff`, the web checks, `alert-check`, `android-test` (when `android/` or `proto/` changed) and the four script self-tests run when their inputs changed. `pin-check` always runs. It does not measure coverage.
- `just lint` runs `golangci-lint run` on the whole tree. See [Lint and coverage](#lint-and-coverage).
- `just cover` runs the tests with the race detector and fails below 80% total coverage. See [Lint and coverage](#lint-and-coverage).
- `just proto-lint` runs `buf lint` in `proto/`.
- `just proto-breaking` runs `buf breaking` against the newest tag that does not point at `HEAD`. It fails when no such tag exists, so a clone without tags needs `git fetch --tags`. CI fetches tags.
- `just sqlc-diff` runs `sqlc diff`. It fails when `internal/store/sqlcgen` does not match `internal/store/queries.sql` and the migrations.
- `just gen-diff` regenerates everything, then fails when the output differs from what is committed. See [Generated code](#generated-code).
- `just pin-check` fails when `devenv.lock` and `flake.lock` resolve nixpkgs to different revisions.
- `just doc-cites` fails when `docs/` cites code by `file:line`. Cite the enclosing symbol instead.
- `just web-check` typechecks `web/` with `tsc`, bundles every `*.test.ts` and `*.test.tsx` under `web/src` with esbuild, and runs the bundles with `node --test`. Each test has a 5-second limit and the whole run a 120-second budget. See [Test time budgets](#test-time-budgets). It runs offline, using the `web/node_modules` that the shell links from nix.
- `just web-page` bundles the web page into `web/build/page`, the directory the flake package embeds. `scripts/build-web.sh` holds the bundle command, and the flake calls it too.
- `just alert-check` runs `promtool test rules` on `observability/orrery-rules.test.yml`.
- `just gate-remote-test` tests `scripts/gate-remote.sh` against a fake box, with no AWS and no network. `just box-cache-test` tests `scripts/box-cache.sh` against a fixture checkout with stub `go`, `nix` and `nix-store`, and no Nix store. `just test-deadline-test` tests `scripts/test-deadline.sh`, and `just test-budget-test` tests `scripts/test-budget.sh` with synthetic numbers.
- `just reap-test-tmux` stops tmux servers that tests started and outlived. `check` and `check-changed` run it first. See [Test conventions](#test-conventions).

### Generation and setup

- `just generate` regenerates the store's typed queries, the Go API, the Android client and the web client from `proto/` and the SQL. See [Generated code](#generated-code).
- `just refresh-env` rebuilds `.devenv-shell-env.sh` now, even when it is fresh.
- `just remote-test-budget-update` re-measures every package's test CPU time on the gate box and rewrites `testdata/test-budget.json`. `just test-budget-update` does the same on this machine. See [Test time budgets](#test-time-budgets).
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
- `just catalog [HOST] [PORT]` serves the web story catalog at `127.0.0.1:8123` by default, and rebuilds it on each edit. See [Web stories and the catalog](#web-stories-and-the-catalog).
- `just shoot NAME... [--width W]...` writes a PNG of each named story at each width, under the `visual` profile's Chromium. It has no default: with no name it exits 1. See [Web stories and the catalog](#web-stories-and-the-catalog).

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

## Test time budgets

The tests run under four limits. Three are wall-clock limits that end a hang with the hung test named. The fourth is a CPU budget per Go package that catches a package whose tests grow expensive.

| Limit | Where | Value | Override |
| --- | --- | --- | --- |
| Go test run under `cover` | `just cover` | 360 s | `just TEST_BUDGET=600 cover` |
| Whole `check-changed` run | `just check-changed` | 240 s | `just CHECK_CHANGED_BUDGET=600 check-changed` |
| Whole web suite | `just web-check` | 120 s | `just WEB_TEST_BUDGET=300 web-check` |
| One web test | `just web-check` | 5 s | none |
| CPU time of one Go package | `just cover` and `just check-changed` | its baseline, plus 15% or 5 CPU seconds | `just remote-test-budget-update` |

`cover` and `check-changed` run each Go test binary through `scripts/test-deadline.sh`. The binary's `-test.timeout` ends 10 seconds before the budget, so a package still running then panics and names its running tests. A `cover` test run that passes its budget fails with exit 124 and `tests exceeded 360s budget`.

The `check-changed` budget counts from the start of the run and covers every check it runs: `pin-check`, lint, the Go tests, and whichever of the web, proto, SQL, alert, script and Android checks the change selects. Each check runs under `timeout` with what is left of the budget. A check still running when the budget ends, or one that fails past it, ends the run with exit 124 and `check-changed: exceeded 240s budget (<check> was running)`. A passing run ends with `check-changed: OK after <n>s of its 240s budget`. `android-test`, which a change to `android/` or `proto/` selects, counts against the same budget. Give such a run a larger `CHECK_CHANGED_BUDGET` when it needs one.

`web-check` runs `node --test --test-timeout=5000` under `timeout 120`. A test still running after 5 seconds fails with its name. A suite still running after 120 seconds is ended, and the recipe fails with exit 124 and `web-check: the web suite exceeded 120s budget`. A test that needs a timeout to fire uses a short one: the child runs in `web/src/main.test.tsx` and `web/src/testwait.test.ts` set their own `--test-timeout` of a few hundred milliseconds.

### CPU budget per package

`cover` and `check-changed` set `ORRERY_TEST_CPU_FILE`, and `scripts/test-deadline.sh` then appends one line per test binary to `test-artifacts/test-budget.record`. The line holds the package directory and the user plus system CPU seconds of the binary and the processes it waited for, such as `internal/store 158.420`. CPU time is used rather than wall time because it changes little when the machine is loaded, while a package's wall time under load is mostly waiting.

`testdata/test-budget.json` is the baseline. It maps each package directory to its CPU seconds under the gate's full test run, the `go test -count=1 -race -covermode=atomic -coverpkg=./... ./...` that `cover` runs. After a passing test run, `cover` and `check-changed` run `scripts/test-budget.sh check` over the record: `cover` checks every package, and `check-changed` only the packages it tested. `check-changed` runs the same tests without coverage, which costs 1% to 10% less CPU per package (about 6% in total), so its packages have that much more room under the same baseline. A package's budget is its baseline plus the larger of 15% and 5 CPU seconds. The check prints:

- `test-budget: internal/store 212 cpu-s > 160 (+33%)` for a package over its budget: it used 212 CPU seconds against a baseline of 160, 33% more. The line is a report, and the check still passes.
- `test-budget: internal/store 330 cpu-s > 160 (+106%), over twice its baseline` for a package over its budget and over twice its baseline. The check fails with exit 3, so `cover`, `check` and `check-changed` fail with exit 3.
- `test-budget: internal/new 12 cpu-s, no baseline` for a package the baseline does not list. This is a report, and the check still passes.
- a last line that counts the packages checked, or the packages over twice their baseline.

A package the baseline lists but the run did not test is not mentioned.

The check also writes these lines to `test-artifacts/test-budget.lines` (`scripts/test-budget.sh check --lines`), and the gates print them again as their last output. `just check` prints the file after its last check, and `check-changed` prints its budget lines again, followed by its `check-changed: OK after …` or `check-changed: exceeded …` line, after the checks that follow its Go tests. A gate keeps only the tail of each output stream, 4 KiB for a node check and 8 KiB for a step gate, and the checks after `cover` print far more than that. The `test_parsimony` reviewer reads the budget lines from that tail.

The baseline is measured on the gate box, where an epic's `check` and `check-changed` run. The same tests take more CPU time on some machines than on others. A 32-core workstation measured 20% to 80% more per package than the 64-core box, about 40% in total, so a full `cover` or `check-changed` there reports the largest packages over budget without failing.

When a change makes a package's tests cost more on purpose, or after the tests got cheaper, re-measure with `just remote-test-budget-update` and commit the new `testdata/test-budget.json` with the change. It runs `just test-budget-update` on the gate box, takes the baseline from the run's output, and writes it here. `just test-budget-update` runs `cover`'s `go test` twice, keeps each package's lower figure, and writes the file, one package per line, sorted, to one decimal. It measures the machine it runs on, so run it directly only when no box can come up. A run that fails or passes the 360-second budget writes nothing.

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
- On a box that mounts the fleet S3 cache at `~/.cache/fleet` (the `orrery-gate` class's `cache = true`), the command runs under `scripts/box-cache.sh run ~/.cache/fleet/box`, so a fresh box starts from what an earlier box built. It restores the Go module and build caches before the command, keyed by `go.mod`, `go.sum` and the Go version, and saves them after a command that exits 0 when the restore missed. `just android-test` adds Gradle's caches (keyed by `flake.lock` and the `android/` build files) and the `.#android` shell's Nix store paths, the unfree Android SDK no public cache holds (keyed by `flake.nix` and `flake.lock`), through `scripts/box-cache.sh android`. Entries are content-keyed tars that are never replaced, and a cache step that fails only warns: the cache never fails a gate. `ORRERY_BOX_CACHE_ROOT` on the box names another cache root, and empty runs without one.
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

## Web stories and the catalog

A story draws one state of a web component. The story catalog shows every story, and `just shoot` writes screenshots of the stories it is named. The catalog is built from the `*.stories.tsx` files under `web/src`.

### Where stories live and how they are named

A component's stories go in `<file>.stories.tsx` beside its `.tsx`: the stories of `inboxrow.tsx` are in `inboxrow.stories.tsx`. The file exports one `Stories` record (the type in `web/src/catalog/story.ts`) per exported component, named exactly as the component's export, so `export const InboxRow: Stories` holds the states of `InboxRow`. Each key of the record is a state in kebab case, and each value is a function that returns the element for that state.

A story's name is `component/state`. The component part is its export's name in kebab case, so the `working` state of `InboxRow` is `inbox-row/working`. `kebab` and `storyName` build the name, and `catalogStories` throws when an export is not a record of functions, when a state is not kebab case, or when two stories share a name.

Round 6 is the reference the catalog's names follow, so a story's name takes a round-6 name. It is either the kebab form of a `Component` and `State` in `web/src/catalog/round6.json`, so `InboxRow/WaitingQuestion` is `inbox-row/waiting-question`, or `page/<round-6 scenario id>`. A story whose state round 6 has no name for is listed in `web/src/catalog/unnamed.ts`, with its round-6 replacement and the frontend epic (F1 to F6) that draws it. A component whose stories export takes its round-6 name, so the composer's and the step box's stories export `Dock`; its coverage is kept through the table in `web/src/catalog/coverage.ts`. After editing round 6, run `just round6-names` to rewrite `round6.json`.

`app.stories.tsx` exports two records. `App` holds the routes, named `app/<route>`, and `Page` holds the scenarios, named `page/<scenario>`. Each of its stories is drawn by `PageStory` (`web/src/catalog/pagestory.tsx`) at the address the story gives, over the in-memory fixture transport. `PageStory` reads the rows, epics and host the way `main.tsx` does.

### How to add one

Build a story's props from the slices in `web/src/fixtures/`. `shippedSlices` in `slices.ts` holds one field for each answer the fixture transport gives, and a component's rows can be read from them with `rowsFrom`, as `inboxrow.stories.tsx` does.

- Give any `now` prop as `Date.parse(liveEpoch) / 1000`, with `liveEpoch` from `web/src/fixtures/clock.ts`. Do not use `Date.now()`, so the times a story draws stay at the fixtures' instant.
- Give clients as `createFixtureTransport().clients`. The fixture transport answers each read from the slices and absorbs each write. Every other call is refused and recorded, and the page then draws a `data-story-failure` element that names it. A story must make no call the fixture refuses.
- Nothing needs registering. `scripts/gen-stories.sh` writes `web/src/catalog/registry.gen.ts`, which imports every `*.stories.tsx` under `web/src`. The file is git-ignored, and the recipes that read it regenerate it first, so there is no list to edit.

A short stories file, trimmed from `card.stories.tsx`:

```tsx
import type { Stories } from "./catalog/story";
import { EpicWidget as Card, epicCallsOf } from "./card";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;
const calls = epicCallsOf(clients, () => {}, () => {}, 0);

export const EpicWidget: Stories = {
  // working is an epic that is building, with its steps running.
  working: () => <Card epic="orrery/orrery-followups" calls={calls} />,
  // unavailable is an epic the fixtures hold no detail for, so its read fails and the card says so.
  unavailable: () => <Card epic="orrery/no-such-epic" calls={calls} />,
};
```

### Checking stories

`just web-check` runs `scripts/gen-stories.sh` before it typechecks, so the registry it checks is current. To see a story drawn, use `just catalog` or `just shoot`. A page story that calls something the fixtures do not serve draws its failure element, and `just shoot` fails that story.

### `just catalog`

`just catalog [HOST] [PORT]` builds the catalog into `web/build/catalog` and serves it at `127.0.0.1:8123` by default. It rebuilds on each edit. A new `*.stories.tsx` file needs a restart, because the registry is generated when the recipe starts.

The index lists every story by name, each name a link to its hash. The hash selects a story, and the index frames it at 1600, 1000 and 412 wide. `?story=<name>` draws one story alone, which each frame loads, and a name the catalog does not hold draws a failure element.

### `just shoot`

`just shoot NAME... [--width W]...` builds the catalog as `just catalog` does, then writes a screenshot of each named story at each width into `web/build/shots/<component>/<state>-<width>.png`. It runs under the `visual` devenv profile, which supplies the Chromium that playwright-core drives. The driver is `web/src/shoot/shoot.ts`.

- A `NAME` is `component/state`, naming one story, or a bare component, naming every state of that component. `inbox-row` names all five states of `InboxRow`.
- `--width W` may repeat. Without it the width is 1600. A width must be a positive integer.
- Motion is frozen at the fixtures' instant. The page's clock is paused at `liveEpoch`, and CSS animations and transitions are turned off.
- The shots directory is cleared once the names resolve, before any screenshot is written.
- It never shoots every story by default. Running it with no name exits 1.
- A story that draws its failure element, raises a page error, or has not settled within 10 seconds fails the run with exit 1.
- An unknown name, or a bad width, exits 1 with a message, and writes no screenshot.

For example, this writes four screenshots: the `waiting-question` and `answered` states at 1600 and at 412 wide.

```bash
just shoot inbox-row/waiting-question inbox-row/answered --width 1600 --width 412
```

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
