import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type Epic, EpicSchema } from "./gen/orrery/v1/epics_pb";
import { ProjectPage } from "./projectpage";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import { buildTree, type Project } from "./tree";

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// buttonNamed returns the button under root whose text is the words, which must be there.
function buttonNamed(root: ParentNode, words: string): HTMLElement {
  const found = all(root, "button").find((button) => button.textContent.trim() === words);
  assert.ok(found !== undefined, `no button reads ${words}`);
  return found;
}

const serving = "halyard";
const now = 1000;
const directory = "/srv/app";

// makeRow is a pane row in the directory on the serving host, unless the init says otherwise.
function makeRow(init: Partial<Row> & Pick<Row, "key" | "label">): Row {
  return {
    id: "%3",
    archiveId: "",
    kind: "pane",
    project: directory,
    host: serving,
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "session",
    tool: "",
    ...init,
  };
}

type EpicInit = MessageInitShape<typeof EpicSchema>;
type ContractInit = MessageInitShape<typeof ContractSchema>;

function makeEpic(init: EpicInit): Epic {
  return create(EpicSchema, { repository: "repo-a", path: directory, live: true, ...init });
}

function makeContract(init: ContractInit): Contract {
  return create(ContractSchema, { repository: directory, title: "Beta", care: "serious", state: "proposed", ...init });
}

// projectOfTree is the project of the directory the fixture tree draws. Its conversations, stand-ins included, are:
// a waiting pane with a proposed contract, a working pane with a live epic, and an unlisted conversation's live epic
// under a stand-in. Its ended line holds one done session with an ended epic.
function projectOfTree(): Project {
  const rows = [
    makeRow({
      key: "pane:%1",
      id: "%1",
      archiveId: "conv-wait",
      label: "Tide table",
      state: "waiting",
      question: "Which branch should I merge?",
      changed: 940,
    }),
    makeRow({
      key: "pane:%2",
      id: "%2",
      archiveId: "conv-work",
      label: "Lamp",
      state: "working",
      tool: JSON.stringify({ name: "mcp__orrery__step_start", input: "alpha", start: 900 }),
      changed: 990,
    }),
    makeRow({
      key: "session:done",
      id: "done",
      kind: "session",
      archiveId: "conv-done",
      label: "Old thread",
      state: "done",
      changed: 300,
    }),
  ];
  const epics = [
    makeEpic({ id: "repo-a/alpha", slug: "alpha", steering: "conv-work", live: true, changed: 990n }),
    makeEpic({ id: "repo-a/gamma", slug: "gamma", steering: "conv-gone", live: true, changed: 700n }),
    makeEpic({ id: "repo-a/delta", slug: "delta", steering: "conv-done", live: false, changed: 320n }),
  ];
  const contracts = [makeContract({ id: "repo-a/beta", conversation: "conv-wait", changed: 920n })];
  const projects = buildTree({ rows, epics, details: new Map(), contracts, host: serving, view: "inbox" });
  const project = projects.find((candidate) => candidate.dir === directory);
  assert.ok(project !== undefined, "the fixture tree draws the directory");
  return project;
}

// handlersOf gives the page its props, each recording its call into calls.
function handlersOf(calls: string[]) {
  return {
    onOpen: (key: string) => calls.push(`open:${key}`),
    onNewHere: (dir: string) => calls.push(`new:${dir}`),
    onBack: () => calls.push("back"),
  };
}

test("ProjectPage titles its row with the project's name, and New session here calls onNewHere with its directory", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    draw(container, <ProjectPage project={projectOfTree()} now={now} narrow={false} {...handlersOf(calls)} />);
    const page = one(container, "section.read.project");
    assert.equal(page.getAttribute("aria-label"), "project");
    assert.equal(one(page, "header .crumb.here").textContent, "app");
    buttonNamed(one(page, ".acts"), "New session here").click();
    assert.deepEqual(calls, [`new:${directory}`]);
    assert.equal(page.querySelector(".cmdbar"), null, "wide, the New session here button is in the title row, not a foot bar");
  });
});

test("ProjectPage draws no New session here for a project with no directory", async () => {
  await withDom((container) => {
    const repository: Project = { key: "repo:tide", dir: "", name: "tide", path: "", host: "", convos: [], ended: [] };
    draw(container, <ProjectPage project={repository} now={now} narrow={false} {...handlersOf([])} />);
    assert.equal(one(container, "header .crumb.here").textContent, "tide");
    assert.deepEqual(
      all(container, "button").map((button) => button.textContent.trim()).filter((text) => text === "New session here"),
      [],
    );
  });
});

test("ProjectPage draws its path, and its host after a dot when the host is another", async () => {
  await withDom((container) => {
    const project = projectOfTree();
    draw(container, <ProjectPage project={project} now={now} narrow={false} {...handlersOf([])} />);
    assert.equal(one(container, ".proj-head .path").textContent, directory);
    draw(container, <ProjectPage project={{ ...project, host: "brine" }} now={now} narrow={false} {...handlersOf([])} />);
    assert.equal(one(container, ".proj-head .path").textContent, `${directory} · on brine`);
  });
});

test("ProjectPage counts its conversations, its epics, what waits on you and its last change", async () => {
  await withDom((container) => {
    draw(container, <ProjectPage project={projectOfTree()} now={now} narrow={false} {...handlersOf([])} />);
    const counts = all(one(container, ".counts"), "span").map((span) => span.textContent.trim());
    assert.deepEqual(counts, ["3 conversations", "2 epics", "1 thing waits on you", "last change 10s ago"]);
    assert.ok(one(container, ".counts .is-attention").textContent.includes("1 thing waits on you"));
  });
});

test("ProjectPage draws every conversation of the project, stand-ins included, each opening on a click", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    const project = projectOfTree();
    draw(container, <ProjectPage project={project} now={now} narrow={false} {...handlersOf(calls)} />);
    const rows = all(container, ".pconv > button.pc-row");
    assert.deepEqual(
      rows.map((row) => row.getAttribute("data-key")),
      project.convos.map((convo) => convo.key),
    );
    const [waiting, working, standIn] = rows;
    assert.equal(one(waiting, ".t").textContent, "Tide table");
    assert.equal(one(waiting, "svg.glyph").getAttribute("data-state"), "waiting");
    assert.equal(one(waiting, ".said.asked").textContent, "Which branch should I merge?");
    assert.equal(one(waiting, ".end .id").textContent, "1m");
    assert.equal(one(working, ".said").textContent, "step_start alpha · 1:40");
    assert.equal(working.querySelector(".said.asked"), null, "a working pane asks nothing");
    assert.equal(one(standIn, ".t").textContent, "Its conversation is not listed");
    assert.equal(one(standIn, ".end .id").textContent, "5m");
    assert.equal(container.querySelector(".pconv .ctx"), null, "no row draws a context meter (DL-G5)");
    standIn.click();
    assert.deepEqual(calls, [`open:${project.convos[2].key}`]);
  });
});

test("ProjectPage draws a WorkCard for each hung thing, and a click on it opens its key", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    draw(container, <ProjectPage project={projectOfTree()} now={now} narrow={false} {...handlersOf(calls)} />);
    const cards = all(container, ".cards > .wcard").map((card) => card.getAttribute("data-key"));
    assert.deepEqual(cards, ["contract:repo-a/beta", "epic:repo-a/alpha", "epic:repo-a/gamma"]);
    const contract = one(container, '.wcard[data-key="contract:repo-a/beta"]');
    assert.equal(one(contract, ".slug").textContent, "beta");
    assert.equal(one(contract, "p.now").textContent, "Beta");
    assert.equal(one(contract, ".asks").textContent.includes("waits for you to accept"), true);
    buttonNamed(contract, "Read and decide").click();
    assert.deepEqual(calls, ["open:contract:repo-a/beta"]);
  });
});

test("ProjectPage unfolds its ended line into the ended conversations and their cards", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    draw(container, <ProjectPage project={projectOfTree()} now={now} narrow={false} {...handlersOf(calls)} />);
    assert.equal(container.querySelector('button.pc-row[data-key="session:done"]'), null);
    const line = one(container, "button.fold-line");
    assert.equal(line.textContent.trim(), "1 ended conversation");
    act(() => {
      line.click();
    });
    assert.equal(one(container, 'button.pc-row[data-key="session:done"] .t').textContent, "Old thread");
    assert.ok(container.querySelector('.wcard[data-key="epic:repo-a/delta"]') !== null, "the ended epic is a card");
  });
});

test("ProjectPage under 760 px draws Back, which calls onBack, and New session here as the foot bar", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    draw(container, <ProjectPage project={projectOfTree()} now={now} narrow {...handlersOf(calls)} />);
    const page = one(container, "section.read.project");
    one(page, 'button[aria-label="back"]').click();
    assert.deepEqual(calls, ["back"]);
    assert.equal(
      all(one(page, ".acts"), "button").filter((button) => button.textContent.trim() === "New session here").length,
      0,
    );
    buttonNamed(one(page, ".cmdbar"), "New session here").click();
    assert.deepEqual(calls, ["back", `new:${directory}`]);
  });
});
