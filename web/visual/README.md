# Visual reference

Frozen reference data for the web client's wireframe parity work. Nothing here is built or shipped.

- `wireframe/round4/` is the round-4 wireframe the owner accepted on 2026-11-15, copied unchanged from its working directory. `index.html#go=<route>` opens a page; `flows.md` names the seven pages.
- `map.json` pairs each wireframe component with the live element that must match it: the scenario both sides show, the wireframe selector, the live selector, and the computed-style properties compared, at widths 1600 and 1000. Font family is never compared: the live client stays in its mono font, by the owner's decision of 2026-11-16. Widths the wireframe measures in `ch` of its prose font are not compared for the same reason.
- `scenarios.md` specifies, field by field, the synthetic data that makes the live page show each scenario the wireframe shows.

Change these files only on an owner's decision: they are the yardstick the parity epics are judged by.

Proposals, not yardsticks:

- `wireframe/round5/` is the web-only proposal of 2026-11-17. Round 6 supersedes it.
- `wireframe/round6/` is the proposal of 2026-11-17 for all three clients: the web at the desk, the web under 760 px and
  the Android app, drawn from one set of components. `index.html` lists every scenario; `DECISIONS.md` says what it
  changes and what it needs from the server; `BACKLOG.md` assigns all of it to the epics that build it.
- `system/` holds the language round 6 draws: `SYSTEM.md`, `tokens.json` and its generator `gen-tokens.mjs`, the audit
  of both clients as built (`AUDIT.md`) and the owner's notes on the live web client (`OWNER-NOTES.md`).
