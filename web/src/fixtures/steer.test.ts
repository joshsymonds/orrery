import assert from "node:assert/strict";
import { test } from "node:test";

import { create, equals } from "@bufbuild/protobuf";

import {
  EpicActionSchema,
  EpicHistoryEntrySchema,
} from "../gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { shippedSlices } from "./slices";
import { endedSession, endedSlices, handoverSlices, lampSession, movedSlices } from "./steer";
import { createFixtureTransport, type FixtureTransport } from "./transport";

const rota = "lighthouse/rota-export";

// paneFrame reads the first frame Panes.WatchConversation sends for a pane, then ends the call.
async function paneFrame(fixture: FixtureTransport, pane: string): Promise<WatchConversationResponse> {
  const call = new AbortController();
  const frames = fixture.clients.panes.watchConversation({ pane }, { signal: call.signal })[Symbol.asyncIterator]();
  const frame = await frames.next();
  call.abort();
  assert.ok(!frame.done, `pane ${pane} sends its frame`);
  return frame.value;
}

// sessionFrame reads the first frame Sessions.WatchConversation sends for a session, then ends the call.
async function sessionFrame(fixture: FixtureTransport, id: string): Promise<SessionsWatchConversationResponse> {
  const call = new AbortController();
  const frames = fixture.clients.sessions.watchConversation({ id }, { signal: call.signal })[Symbol.asyncIterator]();
  const frame = await frames.next();
  call.abort();
  assert.ok(!frame.done, `session ${id} sends its frame`);
  return frame.value;
}

// eventOf is the part of a conversation item a moved block is checked by.
function eventOf(item: { kind: string; sender: string; time: bigint; text: string } | undefined) {
  return item === undefined ? undefined : { kind: item.kind, sender: item.sender, time: item.time, text: item.text };
}

test("SteerWorld: handover gives pane %11 the orrery id lampSession in Panes.List and in its stream's state", async () => {
  const fixture = createFixtureTransport(handoverSlices);
  const list = await fixture.clients.panes.list({});
  assert.equal(list.panes.find((pane) => pane.id === "%11")?.orreryId, lampSession, "Panes.List names pane %11's orrery id");
  const frame = await paneFrame(fixture, "%11");
  assert.equal(frame.state?.pane?.orreryId, lampSession, "the stream's state names it too");
  assert.deepEqual([...fixture.unserved], [], "no call of these was refused");
});

test("SteerWorld: rota is steered by sess-rota-14 in the handover world, by lampSession once moved, and by the ended session after its conversation ended", async () => {
  const worlds = [
    ["handover", handoverSlices, "sess-rota-14"],
    ["moved", movedSlices, lampSession],
    ["ended", endedSlices, endedSession],
  ] as const;
  for (const [name, slices, steering] of worlds) {
    const fixture = createFixtureTransport(slices);
    const row = (await fixture.clients.epics.list({})).epics.find((epic) => epic.id === rota);
    const detail = await fixture.clients.epics.get({ id: rota });
    assert.equal(row?.steering, steering, `${name}: Epics.List names the steering`);
    assert.equal(detail.epic?.steering, steering, `${name}: Epics.Get names the steering`);
  }
});

test("SteerWorld: the moved world's rota history ends with the owner's move to lampSession at @14:44, after the shipped entries", async () => {
  const fixture = createFixtureTransport(movedSlices);
  const history = await fixture.clients.epics.history({ id: rota });
  const shipped = shippedSlices.histories.get(rota);
  if (shipped === undefined) {
    throw new Error("fixtures: the shipped slices hold no history for rota");
  }
  assert.equal(history.entries.length, shipped.entries.length + 1, "the move adds one entry");
  assert.ok(
    history.entries.slice(0, -1).every((entry, index) => {
      const wanted = shipped.entries[index];
      return wanted !== undefined && equals(EpicHistoryEntrySchema, entry, wanted);
    }),
    "the shipped entries come first, unchanged",
  );
  const last = history.entries[history.entries.length - 1];
  assert.equal(last?.time, at("@14:44"), "the move entry is at @14:44");
  const expected = create(EpicActionSchema, {
    seq: 1n, kind: "move", time: at("@14:44"), by: "owner", session: "", from: "sess-rota-14", to: lampSession,
  });
  assert.ok(last?.item.case === "action" && equals(EpicActionSchema, last.item.value, expected), "the entry is the owner's move");
});

test("SteerWorld: pane %14's stream ends with the left block and pane %11's with the taker block, as written", async () => {
  const fixture = createFixtureTransport(movedSlices);
  const left = await paneFrame(fixture, "%14");
  const taker = await paneFrame(fixture, "%11");
  const leftBlock = [
    '<orrery-event kind="moved" id="epic:lighthouse/rota-export:A1">',
    "Epic lighthouse/rota-export (rota-export) moved to session sess-lamp-11, by the owner.",
    "</orrery-event>",
  ].join("\n");
  const takerBlock = [
    '<orrery-event kind="moved" id="epic:lighthouse/rota-export:A1">',
    "orreryd builds this epic; this session steers it by answering its questions and events, and changes nothing in the repository.",
    "Epic: lighthouse/rota-export",
    "</orrery-event>",
  ].join("\n");
  const leftItem = left.items[left.items.length - 1];
  const takerItem = taker.items[taker.items.length - 1];
  assert.deepEqual(eventOf(leftItem), { kind: "prompt", sender: "event", time: at("@14:44"), text: leftBlock }, "pane %14 ends with the left block");
  assert.deepEqual(eventOf(takerItem), { kind: "prompt", sender: "event", time: at("@14:44"), text: takerBlock }, "pane %11 ends with the taker block");
});

test("SteerWorld: the ended world has no pane %14, and a stopped headless session that steers rota with pane %14's stream", async () => {
  const fixture = createFixtureTransport(endedSlices);
  const panes = await fixture.clients.panes.list({});
  assert.ok(!panes.panes.some((pane) => pane.id === "%14"), "pane %14 is gone");
  const headless = await fixture.clients.sessions.listHeadless({});
  const row = headless.sessions.find((session) => session.id === endedSession);
  assert.equal(row?.state, "stopped", "the row is stopped");
  assert.equal(row?.dir, "/home/owner/code/lighthouse", "the row's directory is the lighthouse");
  assert.equal(row?.headline, "Planned the rota export, and steers its epic", "the row's headline");
  assert.equal(row?.changed, at("@-2d 14:41"), "the row changed two days ago");
  assert.equal(row?.seen, at("@-2d 14:42"), "the row was seen a minute later");
  const planning = shippedSlices.paneStreams.get("%14");
  if (planning === undefined) {
    throw new Error("fixtures: the shipped slices hold no stream for pane %14");
  }
  const frame = await sessionFrame(fixture, endedSession);
  assert.equal(frame.items.length, planning.items.length, "the stream holds pane %14's items");
  assert.ok(
    frame.items.every((item, index) => {
      const wanted = planning.items[index];
      return wanted !== undefined && equals(ConversationItemSchema, item, wanted);
    }),
    "each item is pane %14's, in order",
  );
});

test("SteerWorld: Epics.Move and Epics.Continue are answered with no unserved call, and Continue's session is empty", async () => {
  const fixture = createFixtureTransport(handoverSlices);
  await fixture.clients.epics.move({ id: rota, session: lampSession });
  const started = await fixture.clients.epics.continue({ id: rota });
  assert.equal(started.session, "", "Continue names no session");
  assert.deepEqual([...fixture.unserved], [], "neither call is refused");
});

test("SteerWorld: building the worlds leaves the shipped slices unchanged", () => {
  const lamp = shippedSlices.panes.panes.find((pane) => pane.id === "%11");
  assert.equal(lamp?.orreryId, "", "pane %11 has no orrery id in the shipped slices");
  assert.ok(shippedSlices.panes.panes.some((pane) => pane.id === "%14"), "pane %14 is still shipped");
  assert.equal(shippedSlices.gets.get(rota)?.epic?.steering, "sess-rota-14", "rota is still steered by sess-rota-14");
  const entries = shippedSlices.histories.get(rota)?.entries ?? [];
  assert.ok(!entries.some((entry) => entry.item.case === "action"), "the shipped history has no move");
  const stream = shippedSlices.paneStreams.get("%11");
  assert.ok(stream !== undefined && !stream.items.some((item) => item.sender === "event"), "pane %11's shipped stream has no event");
});
