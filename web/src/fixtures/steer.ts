// The fixture worlds the steering pages draw: the handover, the move, and the ended conversation. Each is built from the
// shipped slices, which are never changed. A world replaces only the messages its story changes, with copies made by
// create or clone, so the shipped messages and maps stay as they are.

import { clone, create } from "@bufbuild/protobuf";

import {
  EpicActionSchema,
  EpicHistoryEntrySchema,
  EpicSchema,
  EpicsGetResponseSchema,
  EpicsHistoryResponseSchema,
  EpicsListResponseSchema,
  type EpicsHistoryResponse,
} from "../gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  ListHeadlessResponseSchema,
  type Pane,
  PaneSchema,
  PanesListResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { at } from "./clock";
import { prompt } from "./panes";
import { type Slices, shippedSlices } from "./slices";

// lampSession is pane %11's orrery id in these worlds, and the session the owner moves the rota epic to.
export const lampSession = "sess-lamp-11";
// endedSession is the stopped headless session that steers the rota epic in endedSlices.
export const endedSession = "sess-rota-plan-ended";

const rota = "lighthouse/rota-export";
const rotaSession = "sess-rota-14";
const movedAt = at("@14:44");

// shippedOf returns the shipped message under key, and fails when the shipped slices hold none.
function shippedOf<V>(map: ReadonlyMap<string, V>, key: string): V {
  const value = map.get(key);
  if (value === undefined) {
    throw new Error(`fixtures: the shipped slices hold no entry for ${key}`);
  }
  return value;
}

// paneAs is a copy of pane that holds the orrery id: the shipped pane is left as it is.
function paneAs(pane: Pane, orreryId: string): Pane {
  const copy = clone(PaneSchema, pane);
  copy.orreryId = orreryId;
  return copy;
}

// withLampSession gives pane %11 the orrery id lampSession, in Panes.List and in its own stream's state.
function withLampSession(slices: Slices): Pick<Slices, "panes" | "paneStreams"> {
  const panes = create(PanesListResponseSchema, {
    panes: slices.panes.panes.map((pane) => (pane.id === "%11" ? paneAs(pane, lampSession) : pane)),
  });
  const stream = shippedOf(slices.paneStreams, "%11");
  const state = stream.state;
  if (state?.pane === undefined) {
    throw new Error("fixtures: pane %11's stream holds no pane");
  }
  const paneStreams = new Map(slices.paneStreams);
  paneStreams.set("%11", create(WatchConversationResponseSchema, {
    ...stream,
    state: { ...state, pane: paneAs(state.pane, lampSession) },
  }));
  return { panes, paneStreams };
}

// withRotaSteering names session as the steerer of the rota epic, in Epics.List and in its Epics.Get answer.
function withRotaSteering(slices: Slices, session: string): Pick<Slices, "epics" | "gets"> {
  const get = shippedOf(slices.gets, rota);
  if (get.epic === undefined) {
    throw new Error("fixtures: the rota epic's Epics.Get holds no epic");
  }
  const gets = new Map(slices.gets);
  gets.set(rota, create(EpicsGetResponseSchema, { ...get, epic: { ...get.epic, steering: session } }));
  const epics = create(EpicsListResponseSchema, {
    epics: slices.epics.epics.map((epic) => (epic.id === rota ? create(EpicSchema, { ...epic, steering: session }) : epic)),
  });
  return { epics, gets };
}

// withoutPane14 is the pane streams with pane %14's stream taken out.
function withoutPane14(paneStreams: ReadonlyMap<string, WatchConversationResponse>): Map<string, WatchConversationResponse> {
  const next = new Map(paneStreams);
  next.delete("%14");
  return next;
}

// movedSentence is the server's sentence for the owner's move, the line the epic page's log draws for it.
const movedSentence = "moved to “Retry backoff and jitter for the telemetry uploader” from “Planned the rota export, and steers its epic”, by you";

// withMoveHistory appends the owner's move of the rota epic to lampSession, which is the epic's latest entry.
function withMoveHistory(histories: ReadonlyMap<string, EpicsHistoryResponse>): Map<string, EpicsHistoryResponse> {
  const history = shippedOf(histories, rota);
  const entry = create(EpicHistoryEntrySchema, {
    time: movedAt,
    cursor: "c13",
    sentence: movedSentence,
    item: {
      case: "action",
      value: create(EpicActionSchema, {
        seq: 1n, kind: "move", time: movedAt, by: "owner", session: "", from: rotaSession, to: lampSession,
      }),
    },
  });
  const next = new Map(histories);
  next.set(rota, create(EpicsHistoryResponseSchema, { entries: [...history.entries, entry], next: history.next }));
  return next;
}

// movedEvent is the moved block orreryd writes into a transcript for the epic's event, with the body given.
const movedEvent = (body: string) => `<orrery-event kind="moved" id="epic:${rota}:A1">\n${body}\n</orrery-event>`;

// withMoveEvents appends the left block to pane %14's stream and the taker block to pane %11's, both at the move.
function withMoveEvents(paneStreams: ReadonlyMap<string, WatchConversationResponse>): Map<string, WatchConversationResponse> {
  const appendEvent = (stream: WatchConversationResponse, text: string) =>
    create(WatchConversationResponseSchema, {
      ...stream,
      items: [...stream.items, create(ConversationItemSchema, prompt(movedAt, "event", text))],
    });
  const next = new Map(paneStreams);
  next.set("%11", appendEvent(shippedOf(paneStreams, "%11"), movedEvent([
    "orreryd builds this epic; this session steers it by answering its questions and events, and changes nothing in the repository.",
    `Epic: ${rota}`,
  ].join("\n"))));
  next.set("%14", appendEvent(shippedOf(paneStreams, "%14"), movedEvent(
    `Epic ${rota} (rota-export) moved to session ${lampSession}, by the owner.`,
  )));
  return next;
}

const handover = withLampSession(shippedSlices);

export const handoverSlices: Slices = { ...shippedSlices, ...handover };

export const movedSlices: Slices = {
  ...handoverSlices,
  ...withRotaSteering(handoverSlices, lampSession),
  histories: withMoveHistory(handoverSlices.histories),
  paneStreams: withMoveEvents(handoverSlices.paneStreams),
};

// endedSession's stream is pane %14's stream, and its row is a stopped headless session in the lighthouse.
const endedStream = (planning: WatchConversationResponse) =>
  create(SessionsWatchConversationResponseSchema, {
    transcript: `${endedSession}.jsonl`,
    seq: 1n,
    reset: true,
    items: [...planning.items],
    state: {
      goal: "",
      waiting: [],
      session: {
        id: endedSession, dir: "/home/owner/code/lighthouse", label: "Planned the rota export, and steers its epic",
        state: "stopped", changed: at("@-2d 14:41"), seen: at("@-2d 14:42"), summary: "You stopped it.",
      },
    },
  });

// endedSlices drops pane %14, whose conversation is now that stopped headless session, which steers the rota epic.
export const endedSlices: Slices = {
  ...shippedSlices,
  ...withRotaSteering(shippedSlices, endedSession),
  panes: create(PanesListResponseSchema, { panes: shippedSlices.panes.panes.filter((pane) => pane.id !== "%14") }),
  paneStreams: withoutPane14(shippedSlices.paneStreams),
  sessionStreams: new Map([
    ...shippedSlices.sessionStreams,
    [endedSession, endedStream(shippedOf(shippedSlices.paneStreams, "%14"))],
  ]),
  headless: create(ListHeadlessResponseSchema, {
    sessions: [
      ...shippedSlices.headless.sessions,
      {
        id: endedSession, dir: "/home/owner/code/lighthouse", label: "Planned the rota export, and steers its epic",
        headline: "Planned the rota export, and steers its epic", state: "stopped",
        changed: at("@-2d 14:41"), seen: at("@-2d 14:42"), summary: "You stopped it.",
      },
    ],
  }),
};
