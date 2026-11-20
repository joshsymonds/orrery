import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { Ctl } from "./ctl";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof Ctl>[0];

async function withCtl(
  props: Props,
  check: (button: HTMLButtonElement, container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Ctl {...props} />, container);
    });
    const button = container.querySelector("button");
    assert.ok(button, "the Ctl renders a button");
    await check(button, container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

async function click(button: HTMLButtonElement): Promise<void> {
  await act(() => {
    button.click();
  });
}

test("a click calls onClick", async () => {
  let clicks = 0;
  await withCtl({ label: "Resume", onClick: () => clicks++ }, async (button) => {
    await click(button);
    assert.equal(clicks, 1);
  });
});

test("a disabled Ctl is disabled and ignores clicks", async () => {
  let clicks = 0;
  await withCtl({ label: "Interrupting…", disabled: true, onClick: () => clicks++ }, async (button) => {
    assert.equal(button.disabled, true);
    await click(button);
    assert.equal(clicks, 0);
  });
});

test("an icon-only Ctl draws no visible text and names itself by the label", async () => {
  await withCtl({ label: "More", icon: "more", only: true }, async (button) => {
    assert.equal(button.textContent, "");
    assert.equal(button.getAttribute("aria-label"), "More");
    assert.equal(button.getAttribute("title"), "More");
  });
});

test("kbd draws a kbd element with the hint after the label", async () => {
  await withCtl({ label: "Interrupt", kbd: "esc" }, async (button) => {
    const kbd = button.querySelector("kbd");
    assert.ok(kbd, "the Ctl draws a kbd element");
    assert.equal(kbd.textContent, "esc");
    assert.equal(button.querySelector("span")?.textContent, "Interrupt");
  });
});

test("a Ctl with no kbd draws no kbd element", async () => {
  await withCtl({ label: "Resume", icon: "play", tone: "go" }, async (button) => {
    assertNone(button.querySelector("kbd"), "expected no kbd");
  });
});

test("tone sets the bad or go class", async () => {
  await withCtl({ label: "Interrupt", tone: "bad" }, async (button) => {
    assert.equal(button.classList.contains("bad"), true);
    assert.equal(button.classList.contains("go"), false);
  });
  await withCtl({ label: "Resume", tone: "go" }, async (button) => {
    assert.equal(button.classList.contains("go"), true);
    assert.equal(button.classList.contains("bad"), false);
  });
});

test("expanded sets aria-expanded for a control that opens something", async () => {
  await withCtl({ label: "More", icon: "more", only: true, expanded: true }, async (button) => {
    assert.equal(button.getAttribute("aria-expanded"), "true");
  });
  await withCtl({ label: "More", icon: "more", only: true, expanded: false }, async (button) => {
    assert.equal(button.getAttribute("aria-expanded"), "false");
  });
  await withCtl({ label: "Resume" }, async (button) => {
    assert.equal(button.hasAttribute("aria-expanded"), false);
  });
});
