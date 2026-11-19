import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { unnamed } from "./unnamed";
import { type Round6, storyNameProblems } from "./names";
import { storyModules } from "./registry.gen";
import { storyName } from "./story";

// round6 is a fixed round-6 name set: one component with two states, and one scenario.
const round6: Round6 = {
  components: { InboxRow: ["WaitingQuestion", "Working"], Reply: ["Default"] },
  scenarios: { "inbox-home": [] },
};

test("StoryNames: a story named for a round-6 Component/State passes, and so does a page/<scenario id>", () => {
  assert.deepEqual(storyNameProblems(["inbox-row/waiting-question", "page/inbox-home"], round6, {}), []);
});

test("StoryNames: a story that bears no round-6 name and is listed nowhere is reported, naming just round6-names", () => {
  const problems = storyNameProblems(["inbox-row/nosuch"], round6, {});
  assert.equal(problems.length, 1, `expected one problem, got ${problems.join("; ")}`);
  assert.ok(problems[0].includes("inbox-row/nosuch"), "the problem does not name the story");
  assert.ok(problems[0].includes("web/src/catalog/round6.json"), "the problem does not name round6.json");
  assert.ok(problems[0].includes("web/src/catalog/unnamed.ts"), "the problem does not name unnamed.ts");
  assert.ok(problems[0].includes("just round6-names"), "the problem does not say to rerun just round6-names");
});

test("StoryNames: a story covered by name, or through its export's entry, passes", () => {
  assert.deepEqual(
    storyNameProblems(["markdown/paragraph", "markdown/list"], round6, { markdown: { replacement: "Reply", epic: "F3" } }),
    [],
    "the export's entry does not cover its stories",
  );
  assert.deepEqual(
    storyNameProblems(["inbox-row/answered"], round6, { "inbox-row/answered": { replacement: "InboxRow", epic: "F2" } }),
    [],
    "the story's own entry does not cover it",
  );
});

test("StoryNames: an entry that covers no registered story is reported, by name and by export", () => {
  assert.deepEqual(
    storyNameProblems(["inbox-row/waiting-question"], round6, {
      "inbox-row/gone": { replacement: "InboxRow", epic: "F2" },
      "gone-export": { replacement: "Reply", epic: "F3" },
    }),
    [
      "web/src/catalog/unnamed.ts entry gone-export covers no registered story",
      "web/src/catalog/unnamed.ts entry inbox-row/gone covers no registered story",
    ],
  );
});

test("StoryNames: an entry that covers a story already bearing a round-6 name is reported, for a story and for an export", () => {
  assert.deepEqual(
    storyNameProblems(["inbox-row/waiting-question", "page/inbox-home"], round6, {
      "inbox-row/waiting-question": { replacement: "Reply", epic: "F3" },
      page: { replacement: "Reply", epic: "F3" },
    }),
    [
      "web/src/catalog/unnamed.ts entry inbox-row/waiting-question covers inbox-row/waiting-question, which already bears the round-6 name InboxRow/WaitingQuestion",
      "web/src/catalog/unnamed.ts entry page covers page/inbox-home, which already bears the round-6 name page/inbox-home",
    ],
  );
});

test("StoryNames: an export-level entry whose export is a round-6 component is reported", () => {
  assert.deepEqual(storyNameProblems(["inbox-row/answered"], round6, { "inbox-row": { replacement: "InboxRow", epic: "F2" } }), [
    "web/src/catalog/unnamed.ts entry inbox-row is an export-level entry, but inbox-row is the kebab name of round-6 component InboxRow: list its stories by name",
  ]);
});

test("StoryNames: an entry whose replacement is neither a round-6 component nor a scenario is reported", () => {
  assert.deepEqual(storyNameProblems(["markdown/paragraph"], round6, { "markdown/paragraph": { replacement: "Nosuch", epic: "F3" } }), [
    "web/src/catalog/unnamed.ts entry markdown/paragraph replaces with Nosuch, which is neither a component nor a scenario in web/src/catalog/round6.json",
  ]);
});

test("StoryNames: an entry whose epic is not F1 to F6 is reported", () => {
  assert.deepEqual(storyNameProblems(["markdown/paragraph"], round6, { "markdown/paragraph": { replacement: "Reply", epic: "F0" } }), [
    "web/src/catalog/unnamed.ts entry markdown/paragraph names epic F0, which is not F1 to F6",
  ]);
});

test("StoryNames: the real registry, round6.json and unnamed.ts report no problem", () => {
  // This file is bundled to web/build/catalog, so web/src is two directories up.
  const srcDir = fileURLToPath(new URL("../../src/", import.meta.url));
  const registered = Object.values(storyModules).flatMap((exports) =>
    Object.entries(exports).flatMap(([exportName, stories]) =>
      Object.keys(stories as Record<string, unknown>).map((state) => storyName(exportName, state)),
    ),
  );
  assert.ok(registered.length > 0, "the registry holds no story");
  const real = JSON.parse(readFileSync(`${srcDir}catalog/round6.json`, "utf8")) as Round6;
  assert.deepEqual(storyNameProblems(registered, real, unnamed), []);
});
