import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import type { JSX } from "preact";

import { HostStatusResponseSchema, type HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { MetersButton, MetersPanel } from "./meters";
import { installTestDom, removeTestDom } from "./testdom";

const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "box",
  stats: [
    { label: "cpu", value: "12%", percent: 12, level: "ok" },
    { label: "uptime", value: "3d", level: "warn" },
    { label: "mem", value: "7.1 GB", percent: 88, level: "crit" },
  ],
});

const bare: HostStatusResponse = create(HostStatusResponseSchema, { host: "box" });

const noop = () => undefined;

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(container: HTMLElement, selector: string): string {
  return container.querySelector(selector)?.textContent ?? "";
}

function textsOf(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

test("the button reads the host and the first stat's label and value, under an m key cap", () =>
  withRender(<MetersButton status={status} onToggle={noop} />, (container) => {
    assert.equal(textOf(container, ".meters-host"), "box");
    assert.equal(textOf(container, ".meter-label"), "cpu");
    assert.equal(textOf(container, ".meter-value"), "12%");
    assert.equal(textOf(container, ".meters kbd"), "m");
  }));

test("the button of a host with no stats reads only the host", () =>
  withRender(<MetersButton status={bare} onToggle={noop} />, (container) => {
    assert.equal(textOf(container, ".meters-host"), "box");
    assert.equal(container.querySelector(".meter-label") === null, true);
    assert.equal(container.querySelector(".meter-value") === null, true);
  }));

test("the panel draws one entry per stat, with its label and value in order", () =>
  withRender(<MetersPanel stats={status.stats} />, (container) => {
    assert.equal(container.querySelectorAll(".meter").length, 3);
    assert.deepEqual(textsOf(container, ".meter-label"), ["cpu", "uptime", "mem"]);
    assert.deepEqual(textsOf(container, ".meter-value"), ["12%", "3d", "7.1 GB"]);
  }));

test("only a stat with a percent gets a bar, at that width, with its level as a class", () =>
  withRender(<MetersPanel stats={status.stats} />, (container) => {
    const entries = [...container.querySelectorAll(".meter")];
    assert.deepEqual(
      entries.map((entry) => entry.querySelector(".bar") !== null),
      [true, false, true],
    );
    const bars = [...container.querySelectorAll<HTMLElement>(".bar")];
    assert.deepEqual(
      bars.map((bar) => bar.getAttribute("class")),
      ["bar level-ok", "bar level-crit"],
    );
    assert.deepEqual(
      bars.map((bar) => bar.style.width),
      ["12%", "88%"],
    );
  }));
