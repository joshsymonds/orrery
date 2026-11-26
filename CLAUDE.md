# Orrery

How to build and test is in [CONTRIBUTING.md](CONTRIBUTING.md); the engine's behaviour is specified in
[docs/contract.md](docs/contract.md). This file holds the principle every change to Orrery is judged against.

## Intelligence, not encoded rules

Orrery's bedrock is intelligence: models, and smart gates, that see what the owner wants and work to deliver it. The
engine's job is to give them what they need to judge well and to carry out what they decide. Its job is not to decide for
them with fixed rules. Encoding judgement as rules is the most common way Orrery goes wrong, because the cases it meets
are hazy and the rules are written for the cases someone thought of.

- **Judgements belong to the model.** Whether a failure is recoverable, whether to retry, whether a change fixes it, what
  to do next and whether to ask the owner are decisions for the planner or the step, not for an allowlist of causes, a
  state check or a heuristic in code.
- **No arbitrary limits.** No fixed number of tries, retries or replans while there is a way to land the change. If a
  backstop against a runaway loop is ever needed, it watches for no progress and hands the decision to the planner; it
  is not a count.
- **Guard rails, not walls.** Say what is expected: role text, prompts, what already happened, why something stopped,
  what is reversible. Then let the model act. Refuse an action only when it would do something unsafe or take a
  decision that belongs to the owner, and say in the refusal what is possible instead.
- **Raise to the owner only for the owner's calls.** Irreversible actions, giving up on a change, scope, and the truly
  exceptional. Everything else the model handles and reports.
- **Every offered choice is carried out.** A question, to the owner or to a model, may offer only choices the engine
  performs when chosen. A choice whose answer does nothing is a bug.
- **Nothing specific to one repository in the engine.** Orrery runs other projects. A check that only Orrery's own
  repository needs belongs in that repository's config (`.orrery/project.toml`, its gates), not in engine code or a
  generic release-step kind.
- **Count each new rule as a cost when designing.** Before adding a threshold, a cause list, a special case or a
  canned question, ask whether better information for the model would do. If a question to the owner exists only
  because a mechanism was added, remove the mechanism, not the question's answer.

Example (2026-11-26): a release stopped when the nix build of its tag failed a test. The planner diagnosed the one-line
fix, but the engine refused to send the release back to planning because `replanOfStop` allowed a replan only for an
integrator or reviewer stop. The fix was to remove that cause list, so the engine carries out whatever recovery the
planner chooses, and not to add a replan budget, once-per-tip guards or a build check inside the pin step.
