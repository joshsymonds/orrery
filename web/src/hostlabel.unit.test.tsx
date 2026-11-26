import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { HostEntrySchema, HostStatusResponseSchema, type HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { StateGlyph } from "./glyphs";
import { HostLabel } from "./hostlabel";
import { hostDown, reachableHosts, reachOf } from "./hostreach";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token HostLabel seam: the seam draws on its own, outside any page.

// drawn renders element into a fresh DOM, runs check on the container, and removes the DOM again.
async function drawn(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

// one returns the first element of container that matches selector, which the drawing must hold.
function one(container: ParentNode, selector: string): Element {
  const found = container.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// status is an invented status: halyard answers, and brine, the second host, does not.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "halyard",
  hosts: [
    create(HostEntrySchema, { name: "halyard", loadPercent: 120, memoryPercent: 40, reachable: true }),
    create(HostEntrySchema, { name: "brine", reachable: false }),
  ],
});

test("HostLabel seam: an inline host draws its icon and its name in a host-label span, with neither a heading class nor the word unreachable", () =>
  drawn(<HostLabel name="halyard" />, (container) => {
    const label = one(container, "span.host-label");
    assert.equal(label.classList.contains("heading"), false, "an inline label is not the heading form");
    assert.ok(label.querySelector("svg.icon") !== null, "the label draws the host icon");
    assert.equal(one(label, "span.name").textContent, "halyard");
    assertNone(container.querySelector(".unreach"), "a host that answers draws no unreachable word");
  }));

test("HostLabel seam: a heading host carries the heading class on the same icon and name", () =>
  drawn(<HostLabel name="halyard" heading />, (container) => {
    const label = one(container, "span.host-label");
    assert.equal(label.classList.contains("heading"), true, "the heading form carries the heading class");
    assert.ok(label.querySelector("svg.icon") !== null, "the heading form draws the host icon");
    assert.equal(one(label, "span.name").textContent, "halyard");
  }));

test("HostLabel seam: a down host draws the word unreachable as the sibling after its host-label span", () =>
  drawn(<HostLabel name="brine" down />, (container) => {
    const label = one(container, "span.host-label");
    const word = one(container, "span.unreach");
    assert.equal(word.textContent, "unreachable");
    assertSame(word.parentElement, label.parentElement, "the word is a sibling of the label");
    assertSame(label.nextElementSibling, word, "the word follows the label");
  }));

test("HostLabel seam: an empty name draws nothing, down or not", async () => {
  await drawn(<HostLabel name="" />, (container) => {
    assert.equal(container.childNodes.length, 0, "an empty name draws no node");
  });
  await drawn(<HostLabel name="" down />, (container) => {
    assert.equal(container.childNodes.length, 0, "an empty down name draws no node");
  });
});

test("HostLabel seam: reachOf maps each listed host to its reachable flag, and a null status lists none", () => {
  const reach = reachOf(status);
  assert.equal(reach.size, 2, "both listed hosts are mapped");
  assert.equal(reach.get("halyard"), true, "halyard answers");
  assert.equal(reach.get("brine"), false, "brine does not answer");
  assert.equal(reachOf(null).size, 0, "a null status maps no host");
});

test("HostLabel seam: hostDown is true only for a listed host that does not answer", () => {
  const reach = reachOf(status);
  assert.equal(hostDown(reach, "brine"), true, "a listed unreachable host is down");
  assert.equal(hostDown(reach, "halyard"), false, "a listed reachable host is not down");
  assert.equal(hostDown(reach, "kelp"), false, "an unlisted host is not down");
  assert.equal(hostDown(reach, ""), false, "an empty name is not down");
  assert.equal(hostDown(new Map(), "brine"), false, "an empty map holds no host down");
});

test("HostLabel seam: reachableHosts names the hosts that answer, and a null status names none", () => {
  assert.deepEqual(reachableHosts(status), ["halyard"]);
  assert.deepEqual(reachableHosts(null), []);
});

test("HostLabel seam: StateGlyph unreachable names its word, carries data-state unreachable, and draws a dashed ring with no stopped square", () =>
  drawn(<StateGlyph state="unreachable" />, (container) => {
    const glyph = one(container, "svg.glyph");
    assert.equal(glyph.getAttribute("data-state"), "unreachable");
    assert.equal(glyph.getAttribute("aria-label"), "unreachable");
    assert.equal(one(glyph, "title").textContent, "unreachable");
    assert.equal(one(glyph, "circle").getAttribute("stroke-dasharray"), "2.45 2.45", "the ring is dashed");
    assertNone(glyph.querySelector("rect"), "unreachable is not stopped's square");
  }));
