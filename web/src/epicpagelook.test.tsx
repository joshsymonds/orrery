import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicCommands, EpicPage } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import { type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token EpicPageLook: the epic page's section heads and doors look as round 6 draws them.
// The DOM checks read the page as it draws; the hues, the label face and the door colours are the style sheet's rules,
// which the CSS checks read from style.css.

type Props = Parameters<typeof EpicPage>[0];

// now is the clock the page measures its times against, in unix seconds.
const now = 10_000;

// liveEpic is a live record epic named demo-epic. orrerydEpic is the same epic as an accepted orreryd epic, whose
// requirements, Contract and Steered from draw.
const liveEpic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "", live: true };
const orrerydEpic = { ...liveEpic, source: "orreryd", outcome: "accepted" };

// contract is an orreryd epic's contract, accepted at zero so its words carry no time.
const contract = { title: "Show the accepted contract", care: "high", accepted: 0n };

// build is a live build: task t1 is running its implementing step and names R1, and task t2 is planned and names R2.
const build = {
  epic: liveEpic,
  buildTasks: [
    { id: "t1", title: "Login", status: "dispatched", requirements: ["R1"] },
    { id: "t2", title: "Logout", status: "", requirements: ["R2"] },
  ],
  steps: [
    {
      id: "s1",
      task: "t1",
      agent: "builder",
      stage: "implement",
      started: 9_000n,
      stageStarted: 9_500n,
      doing: "writing the login",
    },
  ],
  requirements: [
    { id: "R1", text: "A user signs in" },
    { id: "R2", text: "A user signs out" },
  ],
};

// reviewed is a reviewing orreryd epic with one reviewer and one finding, so its Review section draws.
const reviewed = {
  ...build,
  epic: { ...orrerydEpic, outcome: "reviewing" },
  summary: "Ship the demo",
  contract,
  reviews: [{ position: 1n, reviewer: "security", reason: "The token is checked", verdict: "holds" }],
  ledger: [
    { id: "security-1", position: 1n, reviewer: "security", claim: "Token not checked", location: "auth.go:12", result: "" },
  ],
};

// releasing is an orreryd epic releasing: its step 1 passed and its step 2 is running, so its Release section draws.
const releasing = {
  ...build,
  epic: { ...orrerydEpic, outcome: "releasing" },
  summary: "Ship the demo",
  contract: { ...contract, releaseThen: "the tag is pushed" },
  releaseSteps: [
    { position: 1n, step: "Tag the release", target: "origin", undo: "git tag -d", state: "passed" },
    { position: 2n, step: "Push the branch", target: "origin", undo: "none", state: "running" },
  ],
};

// reported is an accepted orreryd epic with a report, so its Report section draws.
const reported = {
  ...build,
  epic: orrerydEpic,
  summary: "Ship the demo",
  contract,
  requirements: [{ id: "R1", text: "A user signs in" }],
  report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
};

// detail makes the epic's response from its fields.
function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// commands are the epic's commands, none of which these checks send.
const commands: EpicCommands = {
  pause: () => {},
  resume: () => {},
  stop: () => {},
  retry: () => {},
  answer: async () => "",
  release: async () => ({ kind: "landed" }),
  close: async () => ({ kind: "landed" }),
  reread: async () => true,
};

// noArtifact reads no artifact: no check here opens a log.
const noArtifact: LoadArtifact = async () => {
  throw new Error("no artifact is read in this check");
};

// drawn renders the epic page for the detail into a fresh test DOM, runs check on its container, and removes the DOM.
async function drawn(epic: EpicsGetResponse, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const props: Props = {
    detail: epic,
    history: [],
    now,
    loadArtifact: noArtifact,
    commands,
    numberKeys: true,
    onOpenContract: () => {},
    onOpenSession: () => {},
    onBack: () => {},
    commandError: "",
  };
  try {
    await act(() => {
      render(<EpicPage {...props} />, container);
    });
    check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// one returns the first element of root that matches selector, which the page must draw.
function one(root: ParentNode, selector: string): Element {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// iconPathOf is the drawing the named icon takes in glyphs.tsx: the path data of its one path.
function iconPathOf(name: IconName): string | null | undefined {
  const holder = document.createElement("div");
  render(<Icon name={name} />, holder);
  const path = holder.querySelector("svg.icon path")?.getAttribute("d");
  render(null, holder);
  return path;
}

// partsOf is the children of the element, in the order they draw.
function partsOf(element: Element): Element[] {
  return [...element.children];
}

// nameOf is a part's class, or its tag when it has no class.
function nameOf(part: Element): string {
  return part.getAttribute("class") ?? part.tagName.toLowerCase();
}

// pathOf is the path data of an icon part, or undefined when there is no part.
function pathOf(part: Element | undefined): string | null | undefined {
  return part?.querySelector("path")?.getAttribute("d");
}

// assertHead checks a section head: it is drawn in the section, its icon first and in the hue, then its name, then its
// count when it has one.
function assertHead(section: string, root: ParentNode, icon: IconName, hue: string, counted: boolean): void {
  const head = one(root, `${section} > .sec`);
  assert.equal(head.getAttribute("data-hue"), hue);
  assert.deepEqual(partsOf(head).map(nameOf), counted ? ["icon", "name", "n"] : ["icon", "name"]);
  assert.equal(pathOf(partsOf(head)[0]), iconPathOf(icon));
}

// css is style.css with its comments cut.
const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// declaredValue is the value the style sheet gives a property on a selector: the last rule that names the selector.
function declaredValue(selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (!selectors.includes(selector)) {
      continue;
    }
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0 && declaration.slice(0, colon).trim() === property) {
        value = declaration.slice(colon + 1).trim();
      }
    }
  }
  return value;
}

test("EpicPageLook: Now's head draws the clock icon in the live hue, its name, and no count", () =>
  drawn(detail({ ...build }), (container) => {
    assertHead("section.now", container, "clock", "live", false);
  }));

test("EpicPageLook: Tasks' head draws the pools icon in series 4, its name, and its count", () =>
  drawn(detail({ ...build }), (container) => {
    assertHead("section.tasks", container, "pools", "4", true);
  }));

test("EpicPageLook: Requirements' head draws the check icon in the ok hue, its name, and its count", () =>
  drawn(detail({ ...build, epic: orrerydEpic }), (container) => {
    assertHead("section.requirements", container, "check", "ok", true);
  }));

test("EpicPageLook: Review's head draws the search icon in series 2, its name, and its count", () =>
  drawn(detail(reviewed), (container) => {
    assertHead("section.review", container, "search", "2", true);
  }));

test("EpicPageLook: Release's head draws the open icon in the engine hue, its name, and its count", () =>
  drawn(detail(releasing), (container) => {
    assertHead("section.release", container, "open", "engine", true);
  }));

test("EpicPageLook: Report's head draws the doc icon in series 5, its name, and no count", () =>
  drawn(detail(reported), (container) => {
    assertHead("section.report", container, "doc", "5", false);
  }));

test("EpicPageLook: the Contract door draws the doc icon, its label, its text, then the chevron", () =>
  drawn(detail({ ...build, epic: orrerydEpic, summary: "Ship the demo", contract }), (container) => {
    const parts = partsOf(one(container, ".summary .door.contract"));
    assert.deepEqual(parts.map(nameOf), ["icon", "span", "span", "icon"]);
    assert.equal(pathOf(parts[0]), iconPathOf("doc"));
    assert.equal(parts[1]?.textContent, "Contract");
    assert.equal(pathOf(parts.at(-1)), iconPathOf("chevronRight"));
  }));

test("EpicPageLook: the Steered from door draws the chat icon, its label, its title, then the chevron", () =>
  drawn(detail({ ...build, epic: { ...orrerydEpic, steering: "s2" }, summary: "Ship the demo" }), (container) => {
    const parts = partsOf(one(container, ".summary .door.steered"));
    assert.equal(pathOf(parts[0]), iconPathOf("chat"));
    assert.equal(parts[1]?.textContent, "Steered from");
    assert.equal(pathOf(parts.at(-1)), iconPathOf("chevronRight"));
  }));

test("EpicPageLook: each section head's icon takes its head's hue, and its name the label face", () => {
  assert.equal(declaredValue(".sec > .icon", "color"), "var(--hue)");
  assert.equal(declaredValue(".sec .name", "color"), "var(--hue)");
  assert.equal(declaredValue(".sec .name", "font"), "var(--o-type-label)");
});

test("EpicPageLook: the section head hues are series 1 to 5 and the live, ok and engine roles", () => {
  const hues: [string, string][] = [
    ["1", "var(--o-series-1)"],
    ["2", "var(--o-series-2)"],
    ["3", "var(--o-series-3)"],
    ["4", "var(--o-series-4)"],
    ["5", "var(--o-series-5)"],
    ["live", "var(--o-role-live)"],
    ["ok", "var(--o-role-ok)"],
    ["engine", "var(--o-role-engine)"],
  ];
  for (const [hue, value] of hues) {
    assert.equal(declaredValue(`.sec[data-hue="${hue}"]`, "--hue"), value, `hue ${hue}`);
  }
});

test("EpicPageLook: a door's label takes the reference colour, its text the ink, and its icon the quiet ink", () => {
  assert.equal(declaredValue(".epic-page .door > span:first-of-type", "color"), "var(--o-role-ref)");
  assert.equal(declaredValue(".epic-page .door", "color"), "var(--o-ink-3)");
  assert.equal(declaredValue(".epic-page .door > .icon", "color"), "var(--o-ink-4)");
});
