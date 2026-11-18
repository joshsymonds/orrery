import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { installTestDom, removeTestDom } from "./testdom";
import { type Crumb, TitleRow } from "./titlerow";

// withRow renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withRow(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function countOf(root: ParentNode, selector: string): number {
  return root.querySelectorAll(selector).length;
}

test("‹ is a button labelled back that calls onBack once, and calls no crumb", async () => {
  let backs = 0;
  let goes = 0;
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => (goes += 1) }, { label: "rota-export" }];
  await withRow(
    <TitleRow
      crumbs={crumbs}
      onBack={() => {
        backs += 1;
      }}
    />,
    (container) => {
      const back = container.querySelector(".back");
      assert.equal(textOf(container, ".back"), "‹");
      assert.equal(back?.getAttribute("type") ?? null, "button");
      assert.equal(back?.getAttribute("aria-label") ?? null, "back");
      container.querySelector<HTMLButtonElement>(".back")?.click();
      assert.equal(backs, 1);
      assert.equal(goes, 0);
    },
  );
});

test("each crumb but the last calls its own onGo, and the last crumb calls nothing", async () => {
  const went: string[] = [];
  let backs = 0;
  const crumbs: Crumb[] = [
    { label: "orrery", onGo: () => went.push("orrery") },
    { label: "rota-export", onGo: () => went.push("rota-export") },
    { label: "contract", onGo: () => went.push("contract") },
  ];
  await withRow(
    <TitleRow
      crumbs={crumbs}
      onBack={() => {
        backs += 1;
      }}
    />,
    (container) => {
      const buttons = container.querySelectorAll<HTMLButtonElement>(".crumbs button.crumb");
      assert.equal(buttons.length, 2);
      buttons[0]?.click();
      assert.deepEqual(went, ["orrery"]);
      buttons[1]?.click();
      assert.deepEqual(went, ["orrery", "rota-export"]);
      assert.equal(backs, 0);
    },
  );
});

test("the last crumb is the page: a span marked aria-current page, not a button", async () => {
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => {} }, { label: "contract" }];
  await withRow(<TitleRow crumbs={crumbs} onBack={() => {}} />, (container) => {
    assert.equal(textOf(container, ".crumbs .here"), "contract");
    assert.equal(container.querySelector(".crumbs .here")?.tagName ?? null, "SPAN");
    assert.equal(container.querySelector(".crumbs .here")?.getAttribute("aria-current") ?? null, "page");
    assert.equal(countOf(container, ".crumbs button.crumb"), 1);
  });
});

test("a crumb with no handler is a plain label, and a crumb with one is a button", async () => {
  const crumbs: Crumb[] = [{ label: "first › builder" }, { label: "rota-export", onGo: () => {} }, { label: "s1" }];
  await withRow(<TitleRow crumbs={crumbs} onBack={() => {}} />, (container) => {
    assert.equal(textOf(container, ".crumbs span.crumb:not(.here)"), "first › builder");
    assert.equal(countOf(container, ".crumbs button.crumb"), 1);
    assert.equal(textOf(container, ".crumbs button.crumb"), "rota-export");
  });
});

test("the crumbs read as one line, each after a › separator", async () => {
  const crumbs: Crumb[] = [
    { label: "orrery", onGo: () => {} },
    { label: "rota-export", onGo: () => {} },
    { label: "contract" },
  ];
  await withRow(<TitleRow crumbs={crumbs} onBack={() => {}} />, (container) => {
    assert.equal(textOf(container, ".crumbs")?.trim(), "› orrery › rota-export › contract");
    assert.equal(countOf(container, ".crumbs .sep"), 3);
    assert.equal(textOf(container, ".crumbs .sep"), "›");
  });
});

test("state and blocker show when they are given", async () => {
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => {} }, { label: "contract" }];
  await withRow(
    <TitleRow crumbs={crumbs} onBack={() => {}} state="running" blocker="waiting on rota-export · 4m" />,
    (container) => {
      assert.equal(textOf(container, ".state"), "running");
      assert.equal(textOf(container, ".blocker"), "waiting on rota-export · 4m");
    },
  );
});

test("state and blocker show nothing when they are empty or not given", async () => {
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => {} }, { label: "contract" }];
  await withRow(<TitleRow crumbs={crumbs} onBack={() => {}} state="" blocker="" />, (container) => {
    assert.equal(container.querySelector(".state") !== null, false);
    assert.equal(container.querySelector(".blocker") !== null, false);
  });
  await withRow(<TitleRow crumbs={crumbs} onBack={() => {}} />, (container) => {
    assert.equal(container.querySelector(".state") !== null, false);
    assert.equal(container.querySelector(".blocker") !== null, false);
  });
});

test("children show inside .acts", async () => {
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => {} }, { label: "contract" }];
  await withRow(
    <TitleRow crumbs={crumbs} onBack={() => {}}>
      <button type="button" class="pause">
        Pause
      </button>
      <button type="button" class="stop">
        Stop
      </button>
    </TitleRow>,
    (container) => {
      assert.equal(container.querySelector(".title-row .acts") !== null, true);
      assert.equal(textOf(container, ".acts .pause"), "Pause");
      assert.equal(textOf(container, ".acts .stop"), "Stop");
      assert.equal(countOf(container, ".acts button"), 2);
    },
  );
});

test("the row holds its parts in order, under a header", async () => {
  const crumbs: Crumb[] = [{ label: "orrery", onGo: () => {} }, { label: "contract" }];
  await withRow(
    <TitleRow crumbs={crumbs} onBack={() => {}} state="running" blocker="waiting">
      <button type="button">Pause</button>
    </TitleRow>,
    (container) => {
      const header = container.querySelector(".title-row");
      assert.equal(header?.tagName ?? null, "HEADER");
      assert.deepEqual(
        [...(header?.children ?? [])].map((child) => child.className),
        ["back", "crumbs", "state", "blocker", "acts"],
      );
    },
  );
});

test("markup in a label, state, or blocker shows as text, not as elements", async () => {
  const crumbs: Crumb[] = [{ label: "<b>orrery</b>", onGo: () => {} }, { label: "<i>contract</i>" }];
  await withRow(
    <TitleRow crumbs={crumbs} onBack={() => {}} state="<u>running</u>" blocker="<em>waiting</em>" />,
    (container) => {
      assert.equal(textOf(container, ".crumb"), "<b>orrery</b>");
      assert.equal(textOf(container, ".here"), "<i>contract</i>");
      assert.equal(textOf(container, ".state"), "<u>running</u>");
      assert.equal(textOf(container, ".blocker"), "<em>waiting</em>");
      assert.equal(countOf(container, "b, i, u, em"), 0);
    },
  );
});
