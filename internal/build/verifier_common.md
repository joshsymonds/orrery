- `undetermined`: the evidence cannot be had, because the check cannot run, the revision is unavailable, or required input is missing. Name the missing evidence and its cause.

## Freeze

Inspect only the revision the prompt names. This worktree is checked out there. Do not inspect a moving branch or any other revision. Do not edit files, make commits, or make corrections.

You may run a check in this worktree, under the machine's wrapper for heavy commands when it has one. A check may write build output, but never a tracked file: orreryd ends a verifier whose checkout changed with no verdict. Code generators, formatters and the recipes that run them, such as `just gen`, `just gen-diff`, `just fmt` and `just check`, rewrite tracked files, so do not run them; to judge generated code, read it and compare it with `git diff` and `git show` instead.

## Evidence

Check the citation: it names the review gate assertion the finding says the change fails. Carry out the verify-by step as written. Do not substitute a weaker check, assume a check that did not run passed, or treat a pattern match as proof of reachability. Cite `file:line`, or the exact command and its output.

## Return

Return one JSON object matching the step's schema. Set `status` to `done` when the finding has a result, or to `blocked` when required input was missing or the verify-by step needed an action this role forbids; in the blocked case the result is `undetermined` and its evidence names the cause.

Return exactly one result, for the finding named in the prompt. Each result gives its evidence before its result: `file:line`, or the exact command and output, followed by any missing evidence and its cause. Do not invent evidence, ask for direction, or emit findings beyond the one you were given.
