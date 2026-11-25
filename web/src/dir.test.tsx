import assert from "node:assert/strict";
import { test } from "node:test";

import type { JSX } from "preact";
import { render } from "preact";

import { Dir } from "./dir";
import { shippedSlices } from "./fixtures/slices";
import { rowsFrom, type Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";
import type { Convo, Project } from "./tree";

// rows are the fixture rows, read as load.ts reads them.
const rows = rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions);

// first, second and third are the first three fixture rows, the conversations the cards below draw.
const [first, second, third] = rows;
if (first === undefined || second === undefined || third === undefined) {
  throw new Error("the fixtures hold fewer than three rows");
}

// convo is a conversation of row, drawn in a project, with nothing hung or folded under it.
function convo(row: Row): Convo {
  return { key: row.key, row, hung: [], folded: [] };
}

// makeProject is the lighthouse project with the conversations and ended conversations given.
function makeProject(init: Partial<Project> = {}): Project {
  return {
    key: "|/home/owner/code/lighthouse",
    dir: "/home/owner/code/lighthouse",
    name: "lighthouse",
    path: "~/code/lighthouse",
    host: "",
    convos: [],
    ended: [],
    ...init,
  };
}

// withDir draws the card of project into a fresh DOM, with the props given, and hands check the container it drew into.
async function withDir(
  props: Partial<Omit<Parameters<typeof Dir>[0], "project">> & { project: Project },
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const noop = () => {};
  try {
    render(
      <Dir
        folded={false}
        waiting={0}
        endedOpen={false}
        onFold={noop}
        onEnded={noop}
        ended={null}
        {...props}
      >
        {props.children ?? <p class="kid">kid</p>}
      </Dir>,
      container,
    );
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// textOf is the text of the first element matching selector, which the card must draw.
function textOf(container: HTMLElement, selector: string): string {
  const element = container.querySelector(selector);
  assert.ok(element !== null, `the card draws no ${selector}`);
  return element.textContent ?? "";
}

test("InboxTree Dir: the head names the project and its path", () =>
  withDir({ project: makeProject() }, (container) => {
    assert.equal(textOf(container, ".dir-head .project-name"), "lighthouse");
    assert.equal(textOf(container, ".dir-head .path"), "~/code/lighthouse");
  }));

test("InboxTree Dir: the head carries a host chip only when the project is on another host", async () => {
  await withDir({ project: makeProject({ host: "brine" }) }, (container) => {
    assert.equal(textOf(container, ".dir-head .chip"), "brine");
  });
  await withDir({ project: makeProject({ host: "" }) }, (container) => {
    assert.equal(container.querySelector(".chip"), null, "a project on the serving host draws a host chip");
  });
});

test("InboxTree Dir: folded, the card draws no conversations and no ended line, only its head", () =>
  withDir(
    {
      project: makeProject({ convos: [convo(first)], ended: [convo(second)] }),
      folded: true,
      ended: <p class="ended-kid">ended kid</p>,
      endedOpen: true,
      children: <p class="kid">kid</p>,
    },
    (container) => {
      assert.equal(container.querySelector(".kid"), null, "a folded card draws its conversations");
      assert.equal(container.querySelector(".ended-kid"), null, "a folded card draws its ended conversations");
      assert.equal(container.querySelector(".dir-kids"), null, "a folded card draws its line of conversations");
      assert.equal(container.querySelector(".fold-line"), null, "a folded card draws its ended line");
    },
  ));

test("InboxTree Dir: folded with waiting above zero, the head says how many wait on you and how many conversations it holds", () =>
  withDir(
    {
      project: makeProject({ convos: [convo(first), convo(second)] }),
      folded: true,
      waiting: 3,
    },
    (container) => {
      assert.equal(textOf(container, ".dir-head .is-attention"), "3 waiting");
      assert.equal(textOf(container, ".dir-head .end > span:not(.is-attention)"), "2");
    },
  ));

test("InboxTree Dir: folded with nothing waiting, the head draws no waiting count", () =>
  withDir({ project: makeProject({ convos: [convo(first)] }), folded: true, waiting: 0 }, (container) => {
    assert.equal(container.querySelector(".is-attention"), null, "a folded card with nothing waiting draws a waiting count");
    assert.equal(textOf(container, ".dir-head .end > span:not(.is-attention)"), "1");
  }));

test("InboxTree Dir: the conversation count is each conversation the project holds once, in the tree and behind the ended line", () =>
  withDir(
    {
      project: makeProject({
        convos: [convo(first), convo(second)],
        ended: [convo(third)],
      }),
      folded: true,
    },
    (container) => {
      assert.equal(textOf(container, ".dir-head .end > span"), "3");
    },
  ));

test("InboxTree Dir: the fold button says whether the card is open and calls onFold on each press", async () => {
  let presses = 0;
  const onFold = () => {
    presses += 1;
  };
  await withDir({ project: makeProject(), onFold }, (container) => {
    const button = container.querySelector<HTMLButtonElement>(".dir-head button.fold");
    assert.ok(button !== null, "the head draws no fold button");
    assert.equal(button.getAttribute("aria-expanded"), "true");
    assert.equal(button.getAttribute("aria-label"), "Fold lighthouse");
    button.click();
    assert.equal(presses, 1, "one press calls onFold once");
  });
  await withDir({ project: makeProject(), folded: true, onFold }, (container) => {
    const button = container.querySelector<HTMLButtonElement>(".dir-head button.fold");
    assert.ok(button !== null, "the folded head draws no fold button");
    assert.equal(button.getAttribute("aria-expanded"), "false");
    assert.equal(button.getAttribute("aria-label"), "Unfold lighthouse");
  });
});

test("InboxTree Dir: open, the conversations hang under the head and the ended line counts the ended ones", () =>
  withDir(
    {
      project: makeProject({ ended: [convo(first), convo(second)] }),
      children: <p class="kid">kid</p>,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".dir-kids > .kid").length, 1, "the conversation does not hang under the head");
      assert.equal(textOf(container, "button.fold-line"), "2 ended");
    },
  ));

test("InboxTree Dir: the ended line reports whether it is open, draws the ended conversations only while open, and calls onEnded on each press", async () => {
  let presses = 0;
  const onEnded = () => {
    presses += 1;
  };
  const project = makeProject({ ended: [convo(first)] });
  await withDir({ project, ended: <p class="ended-kid">ended kid</p>, onEnded }, (container) => {
    const line = container.querySelector<HTMLButtonElement>("button.fold-line");
    assert.ok(line !== null, "the card draws no ended line");
    assert.equal(line.getAttribute("aria-expanded"), "false");
    assert.equal(container.querySelector(".ended-kid"), null, "a closed ended line draws its conversations");
    line.click();
    assert.equal(presses, 1, "one press calls onEnded once");
  });
  await withDir({ project, endedOpen: true, ended: <p class="ended-kid">ended kid</p> }, (container) => {
    assert.equal(container.querySelector("button.fold-line")?.getAttribute("aria-expanded"), "true");
    assert.equal(textOf(container, ".dir-kids > .ended-kid"), "ended kid", "an open ended line does not draw its conversations");
  });
});

test("InboxTree Dir: with nothing ended, the card draws no ended line", () =>
  withDir({ project: makeProject({ convos: [convo(first)] }) }, (container) => {
    assert.equal(container.querySelector("button.fold-line"), null, "a card with nothing ended draws an ended line");
  }));

test("ProjectPage Dir: with onOpen, the head's name is one button that calls onOpen once with the project's key and does not fold", async () => {
  let opened: string[] = [];
  let folds = 0;
  await withDir(
    {
      project: makeProject(),
      onOpen: (key) => {
        opened = [...opened, key];
      },
      onFold: () => {
        folds += 1;
      },
    },
    (container) => {
      const button = container.querySelector<HTMLButtonElement>(".dir-head button.proj");
      assert.ok(button !== null, "the head draws no project button");
      assert.equal(button.querySelector(".project-name")?.textContent, "lighthouse");
      button.click();
      assert.deepEqual(opened, ["|/home/owner/code/lighthouse"], "the project button does not open the project once");
      assert.equal(folds, 0, "the project button folds the card");
    },
  );
});

test("ProjectPage Dir: the fold button still folds and does not open the project", async () => {
  let opened = 0;
  let folds = 0;
  await withDir(
    {
      project: makeProject(),
      onOpen: () => {
        opened += 1;
      },
      onFold: () => {
        folds += 1;
      },
    },
    (container) => {
      container.querySelector<HTMLButtonElement>(".dir-head button.fold")?.click();
      assert.equal(folds, 1, "one press on the fold button calls onFold once");
      assert.equal(opened, 0, "the fold button opens the project");
    },
  );
});

test("ProjectPage Dir: the project button's title names the path, or the name when the path is empty", async () => {
  await withDir({ project: makeProject(), onOpen: () => {} }, (container) => {
    assert.equal(
      container.querySelector(".dir-head button.proj")?.getAttribute("title"),
      "Everything working in ~/code/lighthouse",
    );
  });
  await withDir({ project: makeProject({ path: "" }), onOpen: () => {} }, (container) => {
    assert.equal(
      container.querySelector(".dir-head button.proj")?.getAttribute("title"),
      "Everything working in lighthouse",
    );
  });
});

test("ProjectPage Dir: without onOpen, the head draws no project button and reads as before", () =>
  withDir({ project: makeProject() }, (container) => {
    assert.equal(container.querySelector("button.proj"), null, "a card without onOpen draws a project button");
    assert.equal(textOf(container, ".dir-head .proj > .project-name"), "lighthouse");
    assert.equal(textOf(container, ".dir-head .proj > .path"), "~/code/lighthouse");
    assert.equal(container.querySelector(".dir-head .proj")?.getAttribute("title"), null);
  }));

test("InboxTree Dir: the head's project button sets its name and path from the start of its line, as the inbox draws a project", () => {
  assert.equal(styleDeclaration(".dir-head button.proj", "text-align"), "start");
});
