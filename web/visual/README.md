# Visual reference

Reference material for the web client's look. Nothing here is built or shipped.

- `wireframe/round6/` and `system/` are the reference for the web client's look. Round 6 was adopted as the reference with the epic `orrery-round6-yardstick`: the web catalog's story names and fixtures follow it, and the frontend epics are judged by it. `wireframe/round6/index.html` lists its scenarios.
- `web/src/catalog/round6.json` holds round 6's names: each catalogue `Component` and `State`, and each scenario id with its frames. `just round6-names` writes it, and it must be rerun after editing round 6.
- A story whose state round 6 has no name for is listed in `web/src/catalog/unnamed.ts`, with its round-6 replacement and the frontend epic (F1 to F6) that draws it.
- Round 5, and round 4 with `map.json`, are superseded by round 6.
