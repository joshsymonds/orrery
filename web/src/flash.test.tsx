import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test, type TestContext } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";
import { act } from "preact/test-utils";

import { flashMillis, useFlash } from "./flash";
import { installTestDom, removeTestDom } from "./testdom";

// Probe is a control that uses the hook: a click starts the flash, and the class follows the flag.
function Probe(): JSX.Element {
  const [flashing, flash] = useFlash();
  return <button type="button" class={flashing ? "flash" : undefined} onClick={flash} />;
}

async function withProbe(check: (control: HTMLButtonElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Probe />, container);
    });
    await check(button(container));
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

function button(container: HTMLElement): HTMLButtonElement {
  const control = container.querySelector("button");
  assert.ok(control, "the probe renders a button");
  return control;
}

async function tap(control: HTMLButtonElement): Promise<void> {
  await act(() => {
    control.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

function flashing(control: HTMLButtonElement): boolean {
  return control.classList.contains("flash");
}

test("a use flashes at once and clears 300 ms later", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  assert.equal(flashMillis, 300);
  await withProbe(async (control) => {
    assert.equal(flashing(control), false, "not flashing before any use");
    await tap(control);
    assert.equal(flashing(control), true, "flashing at once after a use");
    await advance(t, flashMillis - 1);
    assert.equal(flashing(control), true, "still flashing 1 ms before the window ends");
    await advance(t, 1);
    assert.equal(flashing(control), false, "cleared when the window ends");
  });
});

test("a use during a flash restarts the window", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await withProbe(async (control) => {
    await tap(control);
    await advance(t, 200);
    await tap(control);
    await advance(t, 200);
    assert.equal(flashing(control), true, "still flashing at 400 ms, inside the restarted window");
    await advance(t, 99);
    assert.equal(flashing(control), true, "still flashing 1 ms before the restarted window ends");
    await advance(t, 1);
    assert.equal(flashing(control), false, "cleared 300 ms after the second use");
  });
});

test("unmounting while flashing clears the pending flash timer", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const scheduled = t.mock.method(globalThis, "setTimeout");
  const cleared = t.mock.method(globalThis, "clearTimeout");
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Probe />, container);
    });
    await tap(button(container));
    const flash = scheduled.mock.calls.find((call) => call.arguments[1] === flashMillis);
    assert.ok(flash, "a use schedules the flash timer");
    await act(() => {
      render(null, container);
    });
    assert.ok(
      cleared.mock.calls.some((call) => call.arguments[0] === flash.result),
      "unmounting clears the flash timer, so no state is set after it",
    );
  } finally {
    container.remove();
    await removeTestDom(window);
  }
});

test("style.css gives .flash a visible highlight", () => {
  const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8");
  const rule = css.match(/^\.flash \{([^}]*)\}/m);
  assert.ok(rule, "style.css has a .flash rule");
  assert.match(rule[1], /outline:|box-shadow:/);
});
