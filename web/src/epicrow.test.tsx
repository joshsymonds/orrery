import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { EpicRow, epicRowWords } from "./epicrow";
import { blockOf } from "./epics";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { liveEpoch } from "./fixtures/clock";
import { contracts } from "./fixtures/contracts";
import { epics, gets } from "./fixtures/epics";
import { installTestDom, removeTestDom } from "./testdom";
import type { ContractItem, EpicNode, Hung } from "./tree";

// now is the clock the row reads its times against, in unix seconds: the fixtures' live instant.
const now = Date.parse(liveEpoch) / 1000;

// listedEpic is the Epics.List row of the id in the fixtures, as a copy the test may change.
function listedEpic(id: string, change: (epic: Epic) => void = () => {}): Epic {
  const found = epics.epics.find((epic) => epic.id === id);
  if (found === undefined) {
    throw new Error(`the fixture epics hold no row for ${id}`);
  }
  const copy: Epic = { ...found, runningSteps: [...found.runningSteps] };
  change(copy);
  return copy;
}

// epicNode hangs the listed epic under a conversation as the tree does: its block read from its detail, and its
// contract when one is given. The detail is the fixture's Get answer unless the test passes its own.
function epicNode(
  listed: Epic,
  contract: Contract | null = null,
  detail: EpicsGetResponse | undefined = gets.get(listed.id),
): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, detail), listed, contract };
}

// contractItem hangs the fixture's proposed contract under its conversation.
function contractItem(): ContractItem {
  const contract = contracts.contracts[0];
  return { kind: "contract", key: `contract:${contract.id}`, contract };
}

// withRow draws the row for the Hung in a fresh DOM, runs check on the container, and removes the DOM again.
async function withRow(
  hung: Hung,
  check: (container: HTMLElement) => void | Promise<void>,
  options: { current?: boolean; currentStep?: string; onSelect?: (key: string) => void } = {},
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const row: JSX.Element = (
    <EpicRow
      hung={hung}
      now={now}
      current={options.current ?? false}
      currentStep={options.currentStep}
      onSelect={options.onSelect ?? (() => {})}
    />
  );
  try {
    render(row, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// Each state's words, read from the fixtures' epics as the tree hangs them.

test("InboxTree EpicRow words read building for an epic that is building", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("orrery/orrery-followups")), now).words, "building · 20 min");
});

test("InboxTree EpicRow words read planning for an epic with no tasks yet", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("tidepool/buoy-feeds")), now).words, "planning · 3 min");
});

test("InboxTree EpicRow words read releasing for an epic whose tag is being pushed", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("orrery/orbit-labels")), now).words, "releasing · 4 min");
});

test("InboxTree EpicRow words read parked for a parked epic", () => {
  const parked = listedEpic("orrery/notices-usable", (epic) => {
    epic.parked = "R5 is already met at the epic’s base";
  });
  assert.equal(epicRowWords(epicNode(parked), now).words, "parked · 29 h");
});

test("InboxTree EpicRow words read paused by you for an epic the owner paused", () => {
  const paused = listedEpic("orrery/harbour-map", (epic) => {
    epic.paused = true;
    epic.pauseSource = "owner";
  });
  assert.equal(epicRowWords(epicNode(paused), now).words, "paused by you · 3 h");
});

test("InboxTree EpicRow gauge of a paused epic takes no tone, and a building one the live tone", () => {
  const paused = listedEpic("orrery/harbour-map", (epic) => {
    epic.paused = true;
    epic.pauseSource = "owner";
  });
  assert.equal(epicRowWords(epicNode(paused), now).gauge?.tone, "");
  assert.equal(epicRowWords(epicNode(listedEpic("orrery/orrery-followups")), now).gauge?.tone, "live");
});

test("InboxTree EpicRow words read paused on a usage limit for an epic the limit paused", () => {
  const paused = listedEpic("orrery/harbour-map", (epic) => {
    epic.paused = true;
    epic.pauseSource = "limit";
  });
  assert.equal(epicRowWords(epicNode(paused), now).words, "paused on a usage limit · 3 h");
});

test("InboxTree EpicRow words read shipped for an epic that ended released", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("orrery/holder-scopes")), now).words, "shipped · 4 h 49 min ago");
});

test("InboxTree EpicRow words read ended short for an epic that ended otherwise", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("tidepool/tide-cache")), now).words, "ended short · 101 h 29 min ago");
});

// Each blocker: a question leads with its wait, a failed release step and a park lead with what they say.

test("InboxTree EpicRow blocker waits on your answer for an epic with an open question", () => {
  assert.equal(
    epicRowWords(epicNode(listedEpic("lighthouse/rota-export")), now).blocker,
    "waits on your answer · 2 min",
  );
});

test("InboxTree EpicRow blocker names the failed release step of an epic", () => {
  assert.equal(
    epicRowWords(epicNode(listedEpic("tidepool/offline-tables")), now).blocker,
    "Release step 2 failed: CI is red on the tag v0.4.0 for the second time: go vet: cache/key_test.go:41.",
  );
});

test("InboxTree EpicRow blocker names the park of an epic with no open question", () => {
  const detail = gets.get("orrery/notices-usable");
  if (detail === undefined) {
    throw new Error("the fixture holds no detail for notices-usable");
  }
  const unasked: EpicsGetResponse = { ...detail, questions: [] };
  const parked = listedEpic("orrery/notices-usable", (epic) => {
    epic.parked = unasked.parked;
  });
  assert.equal(
    epicRowWords(epicNode(parked, null, unasked), now).blocker,
    "R5 is already met at the epic’s base",
  );
});

test("InboxTree EpicRow blocker is empty for an epic nothing blocks", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("orrery/orrery-followups")), now).blocker, "");
});

// The count is the epic's proven share, read by epicLine.

test("InboxTree EpicRow count reads requirements proven out of all of them", () => {
  assert.equal(epicRowWords(epicNode(listedEpic("tidepool/offline-tables")), now).count, "6 of 6 proven");
});

// A contract's row: its version, its state in words with its age, and the ask it waits on.

test("InboxTree EpicRow contract row reads its version, contract · proposed with its age, and its ask", () => {
  const words = epicRowWords(contractItem(), now);
  assert.equal(words.count, "v2");
  assert.equal(words.words, "contract · proposed · 26 min ago");
  assert.equal(words.blocker, "waits to be read and accepted");
  assert.equal(words.gauge, null);
});

test("InboxTree EpicRow an epic whose contract is proposed draws as the contract's row", () => {
  const tide = listedEpic("tidepool/tide-alerts");
  const contract = contracts.contracts[0];
  const words = epicRowWords(epicNode(tide, contract), now);
  assert.equal(words.count, "v2");
  assert.equal(words.words, "contract · proposed · 26 min ago");
  assert.equal(words.blocker, "waits to be read and accepted");
  assert.equal(words.gauge, null);
});

test("InboxTree EpicRow contract row draws the contract's mark and its name, with no gauge", () =>
  withRow(contractItem(), (container) => {
    assert.equal(container.querySelectorAll(".row.epic.contract").length, 1);
    assert.equal(container.querySelectorAll(".cmark").length, 1);
    assert.equal(container.querySelectorAll(".gauge").length, 0);
    assert.equal(textOf(container, ".title"), "tide-alerts");
    assert.equal(textOf(container, ".age"), "v2");
    assert.equal(textOf(container, ".l2"), "contract · proposed · 26 min ago");
    assert.equal(container.querySelectorAll(".l3 svg.icon").length, 1);
    assert.equal(textOf(container, ".l3"), "waits to be read and accepted");
  }));

// The row draws the epic's gauge, name, count, words and blocker.

test("InboxTree EpicRow row draws the gauge, slug, words with count, and the blocker led by a question mark", () => {
  const hung = epicNode(listedEpic("lighthouse/rota-export"));
  const words = epicRowWords(hung, now);
  return withRow(hung, (container) => {
    assert.equal(container.querySelectorAll(".row.epic").length, 1);
    assert.equal(container.querySelectorAll(".gauge").length, 1);
    assert.equal(textOf(container, ".title"), "rota-export");
    assert.equal(textOf(container, ".l2"), `building · 49 min · ${words.count}`);
    assert.equal(container.querySelectorAll(".l3 svg.icon").length, 1);
    assert.equal(textOf(container, ".l3"), "waits on your answer · 2 min");
  });
});

test("InboxTree EpicRow gauge takes the attention tone with a dot when the epic is blocked", () =>
  withRow(epicNode(listedEpic("lighthouse/rota-export")), (container) => {
    const gauge = container.querySelector(".gauge");
    assert.equal(gauge?.getAttribute("data-tone"), "attention");
    assert.equal(gauge?.querySelectorAll("circle.core").length, 1);
  }));

test("InboxTree EpicRow gauge takes the ok tone with a check when the epic shipped", () =>
  withRow(epicNode(listedEpic("orrery/holder-scopes")), (container) => {
    const gauge = container.querySelector(".gauge");
    assert.equal(gauge?.getAttribute("data-tone"), "ok");
    assert.equal(gauge?.querySelectorAll("path.done").length, 1);
  }));

test("InboxTree EpicRow an ended epic's row reads ended", () =>
  withRow(epicNode(listedEpic("orrery/holder-scopes")), (container) => {
    assert.equal(container.querySelectorAll(".row.epic.ended").length, 1);
  }));

test("InboxTree EpicRow a row with nothing blocking draws no question-marked line", () =>
  withRow(epicNode(listedEpic("orrery/orrery-followups")), (container) => {
    assert.equal(container.querySelectorAll(".l3").length, 0);
  }));

test("InboxTree EpicRow draws the current row as cur", () =>
  withRow(epicNode(listedEpic("orrery/orrery-followups")), (container) => {
    assert.equal(container.querySelector(".row.epic")?.classList.contains("cur"), true);
  }, { current: true }));

// The steps under the row: at most four, then a line for the rest.

test("InboxTree EpicRow draws at most four running steps and a line for the rest", () => {
  const detail = gets.get("lighthouse/rota-export");
  if (detail === undefined) {
    throw new Error("the fixture holds no detail for rota-export");
  }
  const busy = listedEpic("lighthouse/rota-export", (epic) => {
    epic.runningSteps = [...detail.steps];
  });
  return withRow(epicNode(busy, null, detail), (container) => {
    assert.equal(container.querySelectorAll(".row.step").length, 4);
    assert.equal(textOf(container, ".fold-line")?.startsWith("3 more"), true);
  });
});

// Presses call onSelect with the row's key, or the step's.

test("InboxTree EpicRow a press on the row calls onSelect with the epic's key", () => {
  const selected: string[] = [];
  return withRow(
    epicNode(listedEpic("orrery/orrery-followups")),
    (container) => {
      container.querySelector(".row.epic")?.dispatchEvent(new Event("click"));
      assert.deepEqual(selected, ["epic:orrery/orrery-followups"]);
    },
    { onSelect: (key) => selected.push(key) },
  );
});

test("InboxTree EpicRow a press on a step calls onSelect with the step's key", () => {
  const listed = listedEpic("orrery/orrery-followups");
  const selected: string[] = [];
  return withRow(
    epicNode(listed),
    (container) => {
      container.querySelectorAll(".row.step")[0]?.dispatchEvent(new Event("click"));
      assert.deepEqual(selected, [`step:${listed.runningSteps[0]?.id ?? ""}`]);
    },
    { onSelect: (key) => selected.push(key) },
  );
});

test("InboxTree EpicRow the step named by currentStep reads cur", () => {
  const listed = listedEpic("orrery/orrery-followups");
  return withRow(
    epicNode(listed),
    (container) => {
      const steps = container.querySelectorAll(".row.step");
      assert.equal(steps[0]?.classList.contains("cur"), true);
      assert.equal(steps[1]?.classList.contains("cur"), false);
    },
    { currentStep: `step:${listed.runningSteps[0]?.id ?? ""}` },
  );
});

// DL-DIR1: the row draws no epic command.

test("InboxTree EpicRow draws no command button on an epic's row", () =>
  withRow(epicNode(listedEpic("lighthouse/rota-export")), (container) => {
    assert.equal(container.querySelectorAll("button").length, 0);
  }));
