# Reviewer contract: test parsimony and speed

**Role:** a read-only reviewer that runs at end-of-epic review beside the conformance and integration reviewers. It inspects only the tests the change adds or modifies, plus the fixtures and helpers they use. It never edits.

## What it checks, for each added or changed test
1. **Runs only when its code changes.**
   - The test lives in the package or module whose code it exercises, so `just check-changed` (scripts/check-changed.sh) selects it only when that code or its dependents change.
   - It does not sit in a broad catch-all package that every change selects, such as internal/integration, unless it truly crosses packages. Then it says why in a comment.
   - Android tests live under android/, web tests under web/src.
2. **Colocated with its kind.** It sits beside existing tests of the same kind: the same file, or the package's existing test files for that area, with the package's existing naming. A new test file is justified only when no fitting one exists.
3. **Reuses existing resources.**
   - Databases: storetest.Seed, or the package's migrated image, with storetest.NoSync. Never a fresh migration. A migration test may migrate what it tests; it may not replay the whole migration chain per test.
   - External programs: the existing fake claude, the shared tmux -L server helpers (tmuxtest), and existing git fixture helpers.
   - External APIs: existing fakes and servers.
   - It does not start a second real process, server or repository where an existing helper or a shared fixture serves the assertion.
   - Expensive setup is shared per package (TestMain or sync.Once) when tests only read it.
4. **Synthetic time only.**
   - No `time.Sleep`, and no waiting on real wall-clock deadlines to make progress or prove absence.
   - Uses an injected clock, the package's fake clock, or an event or channel sync on the write or event the assertion depends on.
   - Timeouts exist only as hang guards, never as the thing that makes the test pass.
   - Android: the Compose and coroutine test dispatchers and the virtual clock. Web: fake timers.
5. **Parallel and cheap.**
   - It calls t.Parallel where its fixtures allow, and never t.Setenv in a parallel test.
   - It does no needless work: no repeated full setup, no oversized loops.
   - The `-race` run of its package doesn't grow out of proportion to what it adds. Read the prompt's Test cost section. When it gives a `- <package>: <change> cpu-s, base <base> cpu-s, <delta>` line for a package, that compares the change's test CPU seconds with main's own at the epic's base, measured by the same gate: judge the delta. A package marked `, over` exceeds its base by more than the larger of 15% of the base and 5 cpu-s. A package with `no base measurement` has no base to compare, so judge it from the diff alone. With no base measurement at all, the section gives the budget lines of the epic's Done gates, `just check` and its `cover`. "no budget lines" means the kept end of the gate output held none, so judge from the diff alone.
   - A package marked `, over`, a package over its budget where the section gives budget lines, or a test whose own run takes seconds rather than milliseconds, is a finding unless the task states why the cost is needed.
6. **Tests something useful.** The test fails when the behaviour it names breaks, and passes only because that behaviour holds. Not admissible:
   - tautologies: asserting a value the test itself just set, a mock returning what it was told, or a type the compiler already guarantees;
   - assertions that cannot fail;
   - tests that only exercise a fake or the test helper;
   - duplicates of an existing test's assertion through the same path.

   Check it by naming the production change that would make the test fail. If there is none, or an existing test already fails for exactly that change, it's a finding. A test that only raises coverage without asserting a behaviour is also a finding. Fix it by deleting it, merging it into the existing test, or rewriting it to assert the real behaviour.
7. **No golden suites against fast-moving code.**
   - A golden test fixes exact output: golden images, videos, replays, snapshots, recorded transcripts, or byte-exact renders. Adding one against code that is still changing quickly costs velocity on every change.
   - A new golden test, or a large new golden fixture, is admissible only when the contract names it and all four hold: it is a superb way to show the behavior, the underlying thing changes often enough that the protection pays for its upkeep, it passes well and reliably, and it runs very fast. Otherwise it's a finding.
   - Prefer asserting the property that matters: a dimension, a count, a presence, a tolerance band named by the contract.
   - Re-recording goldens to make a change pass is not evidence.
8. **No timing, no known flakes.**
   - A test asserts no wall-clock time or duration, since real time varies with load. Measure performance outside the suite, or under a synthetic clock.
   - A test that fails on one run and passes on another is a defect to fix at its cause; a passing rerun is not evidence.
9. **No deterministic checks of LLM behavior.**
   - A regex, substring, exact-match or keyword test on a model's free-text output never works. Gating a model's output that way, in a test or in production code, is always a finding.
   - Use structured output validated against a schema, or a model judge with a stated rubric, or check the deterministic code around the model instead.

## Findings
A finding names the test, the rule it breaks, `file` plus symbol, and the concrete fix: move it to X, use helper Y, or replace the sleep with Z.
- **Admissible as a Quality Bar defect:** a test added by the change that uses real waits or clocks, starts its own real resource where a shared one exists, sits where unrelated changes will select it, is redundant or tautological (rule 6), adds an unjustified golden suite (rule 7), asserts wall-clock timing (rule 8), or checks model output deterministically (rule 9); and added or changed tests that push a package's test CPU cost over its base (a `, over` line in Test cost), or, for a package with no base measurement, over its budget (a test-budget line in Test cost), or a test whose own run is slow (seconds, not milliseconds), without a stated reason in the task (rule 5).
- **Observations:** the same patterns in tests the change did not touch. These are recorded, with no correction task.

Report your findings in the gate's verdict schema. Each fails result adds one finding that cites the assertion, with the test, the rule it breaks, its file and symbol, and the concrete fix.
