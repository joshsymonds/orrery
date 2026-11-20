import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { at } from "./fixtures/clock";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { ContractBlock } from "./contractblock";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

// withBlock renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withBlock(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

const title = "The harbour office hears within a minute when a published tide table changes";

// contractOf is a contract the list holds, in the state and care the test names.
function contractOf(state: string, care: string, changed: bigint): Contract {
  return create(ContractSchema, {
    id: "tidepool/tide-alerts",
    repository: "tidepool",
    title,
    care,
    state,
    version: 1n,
    openDecisions: 1n,
    conversation: "session-1",
    created: at("@13:02"),
    changed,
  });
}

test("ContractLines: a proposed contract draws its title, care and the waiting words in the attention tone", async () => {
  const contract = contractOf("proposed", "severe", at("@14:15"));
  await withBlock(<ContractBlock id="tidepool/tide-alerts" contract={contract} onOpen={() => {}} />, (container) => {
    const block = container.querySelector("button.block");
    assert.equal(block?.classList.contains("attention") ?? false, true);
    assert.equal(textOf(container, ".lbl"), "Contract");
    assert.equal(textOf(container, ".body"), `${title} · severe care · waits for you`);
  });
});

test("ContractLines: an accepted contract draws its title, the accepted time and care in the ok tone", async () => {
  const contract = contractOf("accepted", "serious", at("@13:50"));
  await withBlock(<ContractBlock id="tidepool/tide-alerts" contract={contract} onOpen={() => {}} />, (container) => {
    const block = container.querySelector("button.block");
    assert.equal(block?.classList.contains("ok") ?? false, true);
    assert.equal(textOf(container, ".body"), `${title} · accepted ${clock(at("@13:50"))} · serious care`);
  });
});

test("ContractLines: a set-aside contract draws its title and set aside in the quiet tone", async () => {
  const contract = contractOf("set_aside", "limited", at("@12:00"));
  await withBlock(<ContractBlock id="tidepool/tide-alerts" contract={contract} onOpen={() => {}} />, (container) => {
    const block = container.querySelector("button.block");
    assert.equal(block?.classList.contains("quiet") ?? false, true);
    assert.equal(textOf(container, ".body"), `${title} · set aside`);
  });
});

test("ContractLines: a contract the list does not hold draws its id alone in the quiet tone", async () => {
  await withBlock(<ContractBlock id="tidepool/gone" contract={undefined} onOpen={() => {}} />, (container) => {
    const block = container.querySelector("button.block");
    assert.equal(block?.classList.contains("quiet") ?? false, true);
    assert.equal(textOf(container, ".body"), "tidepool/gone");
  });
});

test("ContractLines: a title is drawn as text, never as markup", async () => {
  const contract = create(ContractSchema, {
    id: "tidepool/tide-alerts",
    title: "<b>bold</b> title",
    care: "serious",
    state: "proposed",
    changed: at("@14:15"),
  });
  await withBlock(<ContractBlock id="tidepool/tide-alerts" contract={contract} onOpen={() => {}} />, (container) => {
    assert.equal(container.querySelector(".body b"), null);
    assert.equal(textOf(container, ".body"), "<b>bold</b> title · serious care · waits for you");
  });
});

test("ContractLines: pressing the line calls onOpen with the contract's id", async () => {
  const opened: string[] = [];
  const contract = contractOf("proposed", "serious", at("@14:15"));
  await withBlock(
    <ContractBlock
      id="tidepool/tide-alerts"
      contract={contract}
      onOpen={(id) => {
        opened.push(id);
      }}
    />,
    (container) => {
      container.querySelector<HTMLButtonElement>("button.block")?.click();
      assert.deepEqual(opened, ["tidepool/tide-alerts"]);
    },
  );
});
