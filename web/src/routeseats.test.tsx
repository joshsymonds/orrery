import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { registry as fixtureRegistry } from "./fixtures/accounts";
import { type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { RouteSeats } from "./routeseats";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// The seats are the invented ones of the fixture's registry, or of a registry a test makes with invented hosts and profiles.

// withSeats mounts RouteSeats over registry in a fresh DOM, runs check with the container it draws into, and removes the DOM
// when check returns or throws.
async function withSeats(t: TestContext, registry: RouteRegistry, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
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
      render(<RouteSeats registry={registry} />, container);
    });
    check(container);
  } finally {
    await teardown();
  }
}

// rowOf is the row the table draws for the seat with id, which must be drawn.
function rowOf(container: HTMLElement, id: string): HTMLTableRowElement {
  const row = container.querySelector<HTMLTableRowElement>(`tr[data-seat="${id}"]`);
  assert.ok(row !== null, `the table draws no row for ${id}`);
  return row;
}

// cellsOf is the text of each cell of a seat row, in column order.
function cellsOf(row: HTMLTableRowElement): string[] {
  return [...row.querySelectorAll("td")].map((cell) => cell.textContent ?? "");
}

test("RoutesWebSeats: the table's head names the columns seat, kind, upstream, model and notes, and draws one row per seat in the registry's order", async (t) => {
  await withSeats(t, fixtureRegistry, (container) => {
    assert.deepEqual(
      [...container.querySelectorAll("thead th")].map((head) => head.textContent),
      ["seat", "kind", "upstream", "model", "notes"],
    );
    assert.deepEqual(
      [...container.querySelectorAll("tbody tr")].map((row) => row.getAttribute("data-seat")),
      ["anthropic", "anthropic-haiku", "work-bedrock", "chatgpt-luna", "openrouter-sol"],
    );
  });
});

test("RoutesWebSeats: a forward seat draws its id, forward, its upstream, its model or as asked when it names none, and the account-or-login note", async (t) => {
  await withSeats(t, fixtureRegistry, (container) => {
    assert.deepEqual(cellsOf(rowOf(container, "anthropic")), [
      "anthropic",
      "forward",
      "https://api.anthropic.com",
      "as asked",
      "the context's account, else the session's login",
    ]);
    assert.deepEqual(cellsOf(rowOf(container, "anthropic-haiku")), [
      "anthropic-haiku",
      "forward",
      "https://api.anthropic.com",
      "example-haiku",
      "the context's account, else the session's login",
    ]);
  });
});

test("RoutesWebSeats: a sigv4 seat draws its region as its upstream, and its model map and profile as its notes", async (t) => {
  await withSeats(t, fixtureRegistry, (container) => {
    assert.deepEqual(cellsOf(rowOf(container, "work-bedrock")), [
      "work-bedrock",
      "sigv4",
      "region example-region-1",
      "example-bedrock",
      "model map · profile example-profile",
    ]);
  });
});

test("RoutesWebSeats: an inject seat draws its billing and its speed as its notes, its upstream and its model", async (t) => {
  await withSeats(t, fixtureRegistry, (container) => {
    assert.deepEqual(cellsOf(rowOf(container, "chatgpt-luna")), [
      "chatgpt-luna",
      "inject",
      "http://127.0.0.1:8317",
      "example-luna",
      "subscription · fast",
    ]);
    assert.deepEqual(cellsOf(rowOf(container, "openrouter-sol")), [
      "openrouter-sol",
      "inject",
      "https://models.example.test",
      "example-sol",
      "metered · standard",
    ]);
  });
});

test("RoutesWebSeats: an inject seat with no speed draws its billing alone as its notes, and with no model draws as asked", async (t) => {
  const registry = create(RouteRegistrySchema, {
    seats: [{ id: "plain-inject", authMode: "inject", upstream: "https://models.example.test", billing: "metered" }],
  });
  await withSeats(t, registry, (container) => {
    assert.deepEqual(cellsOf(rowOf(container, "plain-inject")), [
      "plain-inject",
      "inject",
      "https://models.example.test",
      "as asked",
      "metered",
    ]);
  });
});

test("RoutesWebSeats: the table draws no button, field or form, so no seat is added or edited from it", async (t) => {
  await withSeats(t, fixtureRegistry, (container) => {
    assert.equal(container.querySelectorAll("tbody tr").length, fixtureRegistry.seats.length, "the table draws no rows");
    assert.equal(container.querySelectorAll("button, input, select, textarea, form").length, 0);
  });
});

test("RoutesWebSeats: a registry with no seats draws the table's head and no rows", async (t) => {
  await withSeats(t, create(RouteRegistrySchema), (container) => {
    assert.ok(container.querySelector("thead") !== null, "the table draws no head");
    assert.equal(container.querySelectorAll("tbody tr").length, 0);
  });
});
