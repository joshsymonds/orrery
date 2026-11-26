import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { HostEntrySchema, type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { HostsView } from "./hostsview";
import { type Address, formatAddress } from "./router";
import type { SeenClock } from "./seen";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token HostsWebHostsView: the view draws each host of Host.Status, and the Hosts control in the
// inbox head opens it at #/hosts.

// wideWidth and narrowWidth are the window widths on either side of the 760px query the layout follows.
const wideWidth = 1024;
const narrowWidth = 700;
const now = 1000;

// status is an invented status of three hosts, one of which does not answer: halyard, brine and sextant, in that order.
// sextant's load is past 100%, which the bar stops at while its value shows the real percent.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "halyard",
  hosts: [
    create(HostEntrySchema, { name: "halyard", loadPercent: 12.4, memoryPercent: 44.2, reachable: true }),
    create(HostEntrySchema, { name: "brine", reachable: false }),
    create(HostEntrySchema, { name: "sextant", loadPercent: 171.5, memoryPercent: 61.8, reachable: true }),
  ],
});

// stillClock is a seen clock whose timers never fire, so no row is marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// Report is one address App reports: the hash it writes, and whether it pushed it.
interface Report {
  form: string;
  mode: "push" | "replace";
}

// fakeFetch answers each unary call with an empty message, so the App's reads land and no call holds the test open.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

// drawnHosts draws HostsView over status in a fresh DOM, runs check on its container, and removes the DOM again.
async function drawnHosts(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<HostsView status={status} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// rowsOf is the host rows a drawing holds, in order.
function rowsOf(container: ParentNode): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".host-row")];
}

// textOf is the text of the first element under row that matches selector, which row must hold.
function textOf(row: HTMLElement, selector: string): string {
  const found = row.querySelector(selector);
  assert.ok(found !== null, `the row holds no ${selector}`);
  return found.textContent ?? "";
}

// meterValuesOf is the value each meter of a host row draws, in order.
function meterValuesOf(row: HTMLElement): string[] {
  return [...row.querySelectorAll(".meter .value")].map((value) => value.textContent ?? "");
}

// press clicks element, and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// Mounted is the App drawn over the fake, with what a test checks: its container and the addresses App reported.
interface Mounted {
  container: HTMLElement;
  reports: Report[];
}

// withHostsApp mounts App over the fake transport, holding status as its host, at width and at address when one is given,
// in a fresh DOM whose timers run on the fake clock, and runs check. App is unmounted and the DOM removed when check
// returns or throws.
async function withHostsApp(
  t: TestContext,
  options: { width: number; address?: Address },
  check: (mounted: Mounted) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout", "setInterval"]);
  const window = installTestDom();
  window.innerWidth = options.width;
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
  const reports: Report[] = [];
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App
          rows={[]}
          now={now}
          clients={clients}
          seenClock={stillClock}
          host={status}
          address={options.address}
          onAddress={(address, mode) => reports.push({ form: formatAddress(address), mode })}
        />,
        container,
      );
    });
    await check({ container, reports });
  } finally {
    await teardown();
  }
}

test("HostsWebHostsView: each reachable host's row draws its load and memory percent and the word reachable, in the order listed", () =>
  drawnHosts((container) => {
    const rows = rowsOf(container);
    assert.deepEqual(
      rows.map((row) => textOf(row, ".host-label .name")),
      ["halyard", "brine", "sextant"],
      "one row for each host, in the order the status lists them",
    );
    assert.deepEqual(meterValuesOf(rows[0]), ["12%", "44%"], "halyard's load and memory are rounded whole percents");
    assert.equal(textOf(rows[0], ".reach"), "reachable", "halyard says it is reachable");
    assert.deepEqual(meterValuesOf(rows[2]), ["172%", "62%"], "sextant's load shows its real percent, and its memory is rounded");
    assert.equal(textOf(rows[2], ".reach"), "reachable", "sextant says it is reachable");
  }));

test("HostsWebHostsView: the unreachable host's row draws the unreachable glyph and word, and no numbers", () =>
  drawnHosts((container) => {
    const brine = rowsOf(container)[1];
    assert.ok(brine.querySelector('svg.glyph[data-state="unreachable"]') !== null, "the row draws the unreachable glyph");
    assert.equal(textOf(brine, ".unreach"), "unreachable", "the word unreachable sits beside its name");
    assertNone(brine.querySelector(".meter"), "the row draws no meters");
    assertNone(brine.querySelector(".reach"), "the row does not say reachable");
    assert.doesNotMatch(brine.textContent ?? "", /[0-9%]/, "the row shows no numbers");
  }));

test("HostsWebHostsView: a load past 100% fills its bar to 100 while its value shows the real percent", () =>
  drawnHosts((container) => {
    const [, , sextant] = rowsOf(container);
    const fills = [...sextant.querySelectorAll<HTMLElement>(".meter .bar i")].map((fill) => fill.style.width);
    assert.deepEqual(fills, ["100%", "62%"], "the load bar stops at 100 and the memory bar is its percent");
  }));

test("HostsWebHostsView: the Hosts control in the inbox head opens #/hosts, which draws the hosts view under a title row that reads Hosts", (t) =>
  withHostsApp(t, { width: wideWidth }, async ({ container, reports }) => {
    assertNone(container.querySelector(".hosts-read"), "the hosts view is not drawn before the control is pressed");
    const head = container.querySelector<HTMLElement>(".inbox-head");
    assert.ok(head !== null, "the inbox head is drawn");
    const control = head.querySelector<HTMLButtonElement>('button[aria-label="Hosts"]');
    assert.ok(control !== null, "the inbox head holds a Hosts control");
    assert.equal(control.title, "Hosts", "the control is titled Hosts");
    assert.equal(control.nextElementSibling?.getAttribute("aria-label"), "Notices", "the control sits beside the bell");

    await press(control);
    assert.deepEqual(reports.at(-1), { form: "#/hosts", mode: "push" }, "the App reports the hosts address");
    const page = container.querySelector<HTMLElement>(".hosts-read");
    assert.ok(page !== null, "the hosts view is drawn");
    assert.equal(page.querySelector(".crumb.here")?.textContent, "Hosts", "the title row reads Hosts");
    assert.deepEqual(
      rowsOf(page).map((row) => textOf(row, ".host-label .name")),
      ["halyard", "brine", "sextant"],
      "the view lists each host of the status",
    );
    assertNone(page.querySelector('button[aria-label="back"]'), "the wide view has no Back");
  }));

test("HostsWebHostsView: loading #/hosts under 760px draws the view alone, and its Back returns to the inbox", (t) =>
  withHostsApp(t, { width: narrowWidth, address: { view: "inbox", entry: { kind: "hosts" } } }, async ({ container, reports }) => {
    const page = container.querySelector<HTMLElement>(".hosts-read");
    assert.ok(page !== null, "the hosts view is drawn from its address");
    assert.equal(container.querySelector<HTMLElement>(".list")?.hidden, true, "the inbox list is hidden while the view draws alone");
    assert.equal(rowsOf(page).length, 3, "the view lists the three hosts");

    const back = page.querySelector<HTMLButtonElement>('button[aria-label="back"]');
    assert.ok(back !== null, "the view under 760px has a Back");
    await press(back);
    assertNone(container.querySelector(".hosts-read"), "Back draws the inbox again, with no hosts view");
    assert.equal(reports.at(-1)?.form, "#/inbox", "Back reports the inbox address");
  }));
