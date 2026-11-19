import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { CompactHead, type CompactBadge } from "./compacthead";
import { installTestDom, removeTestDom } from "./testdom";

// withHead renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withHead(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

const rota: CompactBadge = { key: "rota", label: "rota", count: 0, asks: true };
const agents: CompactBadge = { key: "agents", label: "agents", count: 2, asks: false };
const running: CompactBadge = { key: "running", label: "running", count: 1, asks: false };

test("PhoneHead: with nothing asking the line has no attention class and no cask", async () => {
  await withHead(<CompactHead asking="" badges={[agents, running]} onOpen={() => {}} />, (container) => {
    const line = container.querySelector(".compact");
    assert.notEqual(line, null);
    assert.equal(line?.classList.contains("attention") ?? true, false);
    assert.equal(container.querySelector(".cask"), null);
  });
});

test("PhoneHead: with asking words the line is attention and draws them in the cask", async () => {
  await withHead(<CompactHead asking="rota-export asks" badges={[rota]} onOpen={() => {}} />, (container) => {
    assert.equal(container.querySelector(".compact")?.classList.contains("attention") ?? false, true);
    assert.equal(container.querySelector(".cask")?.textContent ?? null, "rota-export asks");
  });
});

test("PhoneHead: an asking badge draws its label, a question mark, and the attention class", async () => {
  await withHead(<CompactHead asking="rota-export asks" badges={[rota, agents]} onOpen={() => {}} />, (container) => {
    const badge = container.querySelector("[data-badge='rota']");
    assert.equal(badge?.textContent ?? null, "rota?");
    assert.equal(badge?.classList.contains("attention") ?? false, true);
    const other = container.querySelector("[data-badge='agents']");
    assert.equal(other?.classList.contains("attention") ?? true, false);
  });
});

test("PhoneHead: a counted badge draws its label and its count", async () => {
  await withHead(<CompactHead asking="" badges={[agents]} onOpen={() => {}} />, (container) => {
    assert.equal(container.querySelector("[data-badge='agents']")?.textContent ?? null, "agents 2");
  });
});

test("PhoneHead: a zero count draws the label alone", async () => {
  const idle: CompactBadge = { key: "agents", label: "agents", count: 0, asks: false };
  await withHead(<CompactHead asking="" badges={[idle]} onOpen={() => {}} />, (container) => {
    assert.equal(container.querySelector("[data-badge='agents']")?.textContent ?? null, "agents");
  });
});

test("PhoneHead: badges keep the order they are given in", async () => {
  await withHead(<CompactHead asking="rota-export asks" badges={[rota, agents, running]} onOpen={() => {}} />, (container) => {
    const keys = Array.from(container.querySelectorAll("[data-badge]"), (badge) => badge.getAttribute("data-badge"));
    assert.deepEqual(keys, ["rota", "agents", "running"]);
  });
});

test("PhoneHead: a click calls onOpen once", async () => {
  let opens = 0;
  await withHead(
    <CompactHead
      asking=""
      badges={[agents]}
      onOpen={() => {
        opens += 1;
      }}
    />,
    (container) => {
      container.querySelector<HTMLButtonElement>(".compact")?.click();
      assert.equal(opens, 1);
    },
  );
});

test("PhoneHead: the control is labelled Show the header", async () => {
  await withHead(<CompactHead asking="" badges={[]} onOpen={() => {}} />, (container) => {
    const line = container.querySelector(".compact");
    assert.equal(line?.tagName ?? null, "BUTTON");
    assert.equal(line?.getAttribute("type") ?? null, "button");
    assert.equal(line?.getAttribute("aria-label") ?? null, "Show the header");
  });
});
