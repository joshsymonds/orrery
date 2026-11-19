import assert from "node:assert/strict";
import { test } from "node:test";

import { round6Names, serializeNames } from "./names";

test("Round6Names: states are deduped and sorted", () => {
  const names = round6Names(["Button/Primary", "Button/Default", "Button/Primary", "Button/Danger"], []);
  assert.deepEqual(names.components, { Button: ["Danger", "Default", "Primary"] });
});

test("Round6Names: a component registered again merges into one key with the union of its states", () => {
  const names = round6Names(["Ask/Session", "Button/Default", "Ask/Options", "Button/Default", "Ask/Session"], []);
  assert.deepEqual(names.components, { Ask: ["Options", "Session"], Button: ["Default"] });
});

test("Round6Names: a state keeps everything after the first slash", () => {
  const names = round6Names(["Field/Filled", "Field/Long/Path"], []);
  assert.deepEqual(names.components, { Field: ["Filled", "Long/Path"] });
});

test("Round6Names: frames come out desk then phone, and a scenario with neither has none", () => {
  const names = round6Names(
    [],
    [
      { id: "both", desk: true, phone: true },
      { id: "phone-only", desk: false, phone: true },
      { id: "desk-only", desk: true, phone: false },
      { id: "neither", desk: false, phone: false },
    ],
  );
  assert.deepEqual(names.scenarios, {
    both: ["desk", "phone"],
    "desk-only": ["desk"],
    neither: [],
    "phone-only": ["phone"],
  });
});

test("Round6Names: keys are sorted, whatever order they are given in", () => {
  const names = round6Names(
    ["Toast/Undo", "Button/Default", "Ask/Session", "Menu/Open"],
    [
      { id: "zeta", desk: true, phone: false },
      { id: "alpha", desk: false, phone: true },
    ],
  );
  assert.deepEqual(Object.keys(names.components), ["Ask", "Button", "Menu", "Toast"]);
  assert.deepEqual(Object.keys(names.scenarios), ["alpha", "zeta"]);
});

test("Round6Names: a cell name that is not Component/State fails, naming it", () => {
  for (const cell of ["Button", "/Default", "Button/"]) {
    assert.throws(() => round6Names([cell], []), { message: `cell name ${JSON.stringify(cell)} is not Component/State` });
  }
});

test("Round6Names: serializeNames writes 2-space indented JSON and a trailing newline", () => {
  const names = round6Names(["Button/Default"], [{ id: "inbox-home", desk: true, phone: true }]);
  assert.equal(
    serializeNames(names),
    '{\n  "components": {\n    "Button": [\n      "Default"\n    ]\n  },\n  "scenarios": {\n    "inbox-home": [\n      "desk",\n      "phone"\n    ]\n  }\n}\n',
  );
});
