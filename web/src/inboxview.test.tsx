import assert from "node:assert/strict";
import { test } from "node:test";

import type { JSX, VNode } from "preact";
import { render } from "preact";

import { create } from "@bufbuild/protobuf";

import { ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { Inbox, type InboxProps } from "./inbox";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import type { Convo, Project } from "./tree";
import { projectWaiting } from "./treefilter";

const noop = () => {};

// row is a seen row in the given state, as the tree reads a conversation's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// convo is a conversation of row drawn in a project, with nothing hung under it.
function convo(init: Pick<Row, "key"> & Partial<Row>): Convo {
  return { key: init.key, row: row(init), standIn: "", again: false, hung: [] };
}

// makeProject is a project card with the conversations and ended conversations given.
function makeProject(init: Partial<Project> & Pick<Project, "key" | "name">): Project {
  return {
    dir: `/work/${init.name}`,
    path: `~/work/${init.name}`,
    host: "",
    convos: [],
    ended: [],
    ...init,
  };
}

// inboxProps are the inputs of an inbox that is ready, empty, unsearched and unnarrowed, with every press a no-op, and the
// init given over them.
function inboxProps(init: Partial<InboxProps> = {}): InboxProps {
  const projects = init.projects ?? [];
  return {
    view: "inbox",
    state: "ready",
    projects: [],
    waitingOf: new Map(projects.map((project) => [project.key, projectWaiting(project)])),
    counts: { waiting: 0, fresh: 0 },
    narrow: "",
    onNarrow: noop,
    search: null,
    onSearch: noop,
    folded: new Set(),
    onFold: noop,
    endedOpen: new Set(),
    onEnded: noop,
    renderConvo: (item: Convo): VNode => <p class="convo" data-key={item.key}>{item.key}</p>,
    archivedCount: 0,
    onView: noop,
    onRetry: noop,
    onNew: noop,
    unread: 0,
    onNotices: noop,
    ...init,
  };
}

// withInbox draws the Inbox with the props given into a fresh DOM and hands check the container it drew into.
async function withInbox(init: Partial<InboxProps>, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Inbox {...inboxProps(init)} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// textOf is the text of the first element matching selector, which the inbox must draw.
function textOf(container: HTMLElement, selector: string): string {
  const element = container.querySelector(selector);
  assert.ok(element !== null, `the inbox draws no ${selector}`);
  return element.textContent ?? "";
}

// buttonNamed is the first button under root whose text begins with label, which the inbox must draw there.
function buttonNamed(root: ParentNode, label: string): HTMLButtonElement {
  const button = [...root.querySelectorAll<HTMLButtonElement>("button")].find((item) =>
    (item.textContent ?? "").trim().startsWith(label),
  );
  assert.ok(button !== undefined, `the inbox draws no button named ${label}`);
  return button;
}

test("InboxTree Inbox: the head draws the mark, orrery, a search control and New", () =>
  withInbox({}, (container) => {
    const head = container.querySelector(".inbox-head");
    assert.ok(head !== null, "the inbox draws no head");
    assert.ok(head.querySelector("svg.mark") !== null, "the head draws no Orrery mark");
    assert.equal(textOf(container, ".inbox-head .name"), "orrery");
    assert.ok(head.querySelector('button[aria-label="Search sessions and epics"]') !== null, "the head has no search control");
    assert.equal(buttonNamed(head, "New").textContent?.trim(), "New");
    assert.equal(head.querySelector(".is-attention, .bell"), null, "the head draws a bell");
  }));

test("InboxTree Inbox: each chip is drawn only when its count is not zero", async () => {
  await withInbox({ counts: { waiting: 0, fresh: 0 } }, (container) => {
    assert.equal(container.querySelector(".fchip"), null, "the inbox draws a chip with nothing counted");
  });
  await withInbox({ counts: { waiting: 2, fresh: 0 } }, (container) => {
    const chips = container.querySelectorAll(".fchip");
    assert.equal(chips.length, 1, "a zero new count draws its chip");
    assert.ok(chips[0].classList.contains("attention"), "the waiting chip is not the attention tone");
    assert.equal(chips[0].textContent, "2wait on you");
  });
  await withInbox({ counts: { waiting: 1, fresh: 3 } }, (container) => {
    const chips = container.querySelectorAll(".fchip");
    assert.equal(chips.length, 2, "a non-zero count draws no chip");
    assert.equal(chips[0].textContent, "1waits on you", "one waiting thing uses the singular label");
    assert.equal(chips[1].textContent, "3new");
  });
});

test("InboxTree Inbox: pressing a chip narrows by it, and pressing the chip that is on narrows to none", async () => {
  const narrowed: string[] = [];
  const counts = { waiting: 2, fresh: 3 };
  await withInbox({ counts, narrow: "", onNarrow: (by) => { narrowed.push(by); } }, (container) => {
    const [waiting, fresh] = container.querySelectorAll<HTMLButtonElement>(".fchip");
    waiting.click();
    fresh.click();
  });
  assert.deepEqual(narrowed, ["waiting", "new"], "a chip that is not on narrows by its own name");
  narrowed.length = 0;
  await withInbox({ counts, narrow: "waiting", onNarrow: (by) => { narrowed.push(by); } }, (container) => {
    const [waiting] = container.querySelectorAll<HTMLButtonElement>(".fchip");
    assert.equal(waiting.getAttribute("aria-pressed"), "true", "the narrowing chip is not on");
    waiting.click();
  });
  assert.deepEqual(narrowed, [""], "pressing the chip that is on does not narrow to none");
});

test("InboxTree Inbox: the archived view draws no chips", () =>
  withInbox({ view: "archived", counts: { waiting: 2, fresh: 3 } }, (container) => {
    assert.equal(container.querySelector(".fchip"), null, "the archived view draws a chip");
  }));

test("InboxTree Inbox: the search control opens search with an empty field, and the field draws only while search is open", async () => {
  const opened: Array<string | null> = [];
  await withInbox({ search: null, onSearch: (text) => { opened.push(text); } }, (container) => {
    assert.equal(container.querySelector(".inbox-search"), null, "the inbox draws a search field while search is closed");
    container.querySelector<HTMLButtonElement>('button[aria-label="Search sessions and epics"]')?.click();
  });
  assert.deepEqual(opened, [""], "the search control does not open search with an empty text");

  await withInbox({ search: "" }, (container) => {
    const field = container.querySelector<HTMLInputElement>(".inbox-search input");
    assert.ok(field !== null, "an open search draws no field");
    assert.equal(field.getAttribute("placeholder"), "Search sessions and epics");
  });
});

test("InboxTree Inbox: typing in the search field calls onSearch with the text, and esc in it calls onSearch with null", async () => {
  const typed: Array<string | null> = [];
  await withInbox({ search: "", onSearch: (text) => { typed.push(text); } }, (container) => {
    const field = container.querySelector<HTMLInputElement>(".inbox-search input");
    assert.ok(field !== null, "the inbox draws no search field");
    field.value = "api";
    field.dispatchEvent(new Event("input"));
  });
  assert.deepEqual(typed, ["api"], "typing does not call onSearch with the text");

  const closed: Array<string | null> = [];
  await withInbox({ search: "api", onSearch: (text) => { closed.push(text); } }, (container) => {
    const field = container.querySelector<HTMLInputElement>(".inbox-search input");
    assert.ok(field !== null, "the inbox draws no search field");
    field.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  });
  assert.deepEqual(closed, [null], "esc in the field does not close search");
});

test("InboxTree Inbox: loading draws that the inbox is loading, and no project", () =>
  withInbox({ state: "loading", projects: [makeProject({ key: "a", name: "api" })] }, (container) => {
    assert.equal(textOf(container, ".inbox-rows .blank"), "loading…");
    assert.equal(container.querySelector(".dir"), null, "a loading inbox draws a project");
  }));

test("InboxTree Inbox: an empty inbox says nothing is running and offers New, which calls onNew", async () => {
  let created = 0;
  await withInbox({ onNew: () => { created += 1; } }, (container) => {
    assert.ok(textOf(container, ".inbox-rows .blank").includes("Nothing is running."), "the empty inbox does not say so");
    buttonNamed(container.querySelector(".inbox-rows .blank") ?? container, "New").click();
  });
  assert.equal(created, 1, "New in the empty inbox does not call onNew once");
});

test("InboxTree Inbox: an empty inbox says a session started in a directory shows there, grouped by it", async () => {
  await withInbox({}, (container) => {
    assert.equal(
      textOf(container, ".inbox-rows .blank").includes("Start a session in a directory and it shows here, grouped by that directory."),
      true,
      "the empty inbox does not say where a started session shows",
    );
  });
});

test("InboxTree Inbox: an empty Archived draws no empty state and keeps Back to the inbox", async () => {
  await withInbox({ view: "archived" }, (container) => {
    assert.equal(container.querySelector(".inbox-rows .blank"), null, "the empty Archived draws the inbox's empty state");
    assert.ok(textOf(container, ".inbox-foot").includes("Back to the inbox"), "the empty Archived has no way back");
  });
});

test("InboxTree Inbox: an error says the server did not answer and offers Try now, which calls onRetry", async () => {
  let retries = 0;
  await withInbox({ state: "error", onRetry: () => { retries += 1; } }, (container) => {
    assert.ok(textOf(container, ".inbox-rows .blank").includes("The server did not answer."), "the error does not say so");
    assert.ok(textOf(container, ".inbox-rows .blank").includes("Trying again"), "the error does not say it is trying again");
    buttonNamed(container, "Try now").click();
  });
  assert.equal(retries, 1, "Try now does not call onRetry once");
});

test("InboxTree Inbox: the chips draw only with the tree, not while loading or after a failed read", async () => {
  const counts = { waiting: 2, fresh: 3 };
  const chipsDrawn: Record<string, boolean> = {};
  for (const state of ["ready", "loading", "error"] as const) {
    await withInbox({ state, counts }, (container) => {
      chipsDrawn[state] = container.querySelector(".inbox-attn") !== null;
    });
  }
  assert.deepEqual(chipsDrawn, { ready: true, loading: false, error: false });
});

test("InboxTree Inbox: a search with no project names the words searched", () =>
  withInbox({ search: "ship it", projects: [] }, (container) => {
    assert.ok(
      textOf(container, ".inbox-rows .blank").includes("Nothing matches “ship it”."),
      "a search with no match does not name its words",
    );
    assert.ok(
      textOf(container, ".inbox-rows .blank").includes("titles, directories, hosts, states, and the names of epics and contracts"),
      "a search with no match does not say what search looks at",
    );
  }));

test("InboxTree Inbox: each project is a card, with its conversations drawn through renderConvo", () => {
  const api = makeProject({ key: "api", name: "api", convos: [convo({ key: "a1", state: "working" })] });
  const web = makeProject({ key: "web", name: "web", convos: [convo({ key: "w1", state: "working" })] });
  return withInbox({ projects: [api, web] }, (container) => {
    const cards = container.querySelectorAll("section.dir");
    assert.equal(cards.length, 2, "the inbox does not draw one card per project");
    assert.ok(cards[0].querySelector('[data-key="a1"]') !== null, "api's conversation is not under api's card");
    assert.ok(cards[1].querySelector('[data-key="w1"]') !== null, "web's conversation is not under web's card");
  });
});

test("InboxTree Inbox: a folded project counts a proposed contract hung under a working conversation as waiting", () => {
  const contract = create(ContractSchema, { id: "api/alpha", state: "proposed", conversation: "a1" });
  const writer: Convo = { ...convo({ key: "a1", state: "working" }), hung: [{ kind: "contract", key: "contract:api/alpha", contract }] };
  const api = makeProject({ key: "api", name: "api", convos: [writer] });
  return withInbox({ projects: [api], folded: new Set(["api"]) }, (container) => {
    assert.equal(textOf(container, "section.dir .dir-head .is-attention"), "1 waiting", "the folded count leaves out the contract");
  });
});

test("InboxTree Inbox: a folded project counts what waits on it, and its fold press calls onFold with its key", async () => {
  const api = makeProject({
    key: "api",
    name: "api",
    convos: [convo({ key: "a1", state: "waiting" }), convo({ key: "a2", state: "working" })],
  });
  let folds: string[] = [];
  await withInbox({ projects: [api], folded: new Set(["api"]) }, (container) => {
    assert.equal(textOf(container, "section.dir .dir-head .is-attention"), "1 waiting", "the folded count is not what waits");
  });
  await withInbox({ projects: [api], onFold: (key) => { folds = [...folds, key]; } }, (container) => {
    container.querySelector<HTMLButtonElement>("section.dir .fold")?.click();
  });
  assert.deepEqual(folds, ["api"], "the fold press does not call onFold with the project's key");
});

test("InboxTree Inbox: a folded project says the waiting of its whole project, not of the narrowed project it draws", () => {
  const api = makeProject({ key: "api", name: "api", convos: [convo({ key: "a2", state: "working" })] });
  return withInbox({ projects: [api], folded: new Set(["api"]), waitingOf: new Map([["api", 1]]) }, (container) => {
    assert.equal(textOf(container, "section.dir .dir-head .is-attention"), "1 waiting", "the folded count is the narrowed project's, not the whole project's");
  });
});

test("InboxTree Inbox: a project's ended line calls onEnded with its key, and opens to its ended conversations", async () => {
  const api = makeProject({
    key: "api",
    name: "api",
    convos: [convo({ key: "a1", state: "working" })],
    ended: [convo({ key: "old", state: "stopped" })],
  });
  let ended: string[] = [];
  await withInbox({ projects: [api], onEnded: (key) => { ended = [...ended, key]; } }, (container) => {
    assert.equal(container.querySelector('[data-key="old"]'), null, "a closed ended line draws its conversations");
    container.querySelector<HTMLButtonElement>(".fold-line")?.click();
  });
  assert.deepEqual(ended, ["api"], "the ended line does not call onEnded with the project's key");
  await withInbox({ projects: [api], endedOpen: new Set(["api"]) }, (container) => {
    assert.ok(container.querySelector('[data-key="old"]') !== null, "an open ended line does not draw its conversations");
  });
});

test("InboxTree Inbox: archived draws its title with the count, and Back to the inbox calls onView with inbox", async () => {
  const views: string[] = [];
  await withInbox({ view: "archived", archivedCount: 4, onView: (view) => { views.push(view); } }, (container) => {
    assert.equal(textOf(container, ".sec .name"), "Archived");
    assert.equal(textOf(container, ".sec .n"), "4");
    buttonNamed(container, "Back to the inbox").click();
  });
  assert.deepEqual(views, ["inbox"], "Back to the inbox does not call onView with inbox");
});

test("InboxTree Inbox: the foot's Archived calls onView with archived", async () => {
  const views: string[] = [];
  await withInbox({ onView: (view) => { views.push(view); } }, (container) => {
    assert.equal(container.querySelector(".sec"), null, "the inbox draws the archived title");
    const foot = container.querySelector<HTMLElement>(".inbox-foot");
    assert.ok(foot !== null, "the inbox draws no foot");
    assert.equal(buttonNamed(foot, "Archived").querySelector("kbd")?.textContent, "v", "the foot's Archived does not name key v");
    buttonNamed(foot, "Archived").click();
  });
  assert.deepEqual(views, ["archived"], "the foot's Archived does not call onView with archived");
});

test("InboxTree Inbox: the notes draw under the head", () =>
  withInbox({ notes: <p class="note">the server is slow</p> }, (container) => {
    const head = container.querySelector(".inbox-head");
    assert.equal(head?.nextElementSibling?.classList.contains("note"), true, "the notes do not draw under the head");
  }));
