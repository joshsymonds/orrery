// The fixture slices: the data the in-memory fixture transport answers with, one field per answer. shippedSlices is the
// set the transport serves when it is given none.

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { Code } from "@connectrpc/connect";

import { type ContractsListResponse, ContractsListResponseSchema } from "../gen/orrery/v1/contracts_pb";
import {
  type EpicsGetResponse,
  type EpicsHistoryResponse,
  type EpicsListResponse,
  EpicsListResponseSchema,
} from "../gen/orrery/v1/epics_pb";
import {
  type HostStatusResponse,
  type ListHeadlessResponse,
  ListHeadlessResponseSchema,
  type ListResponse,
  type PanesListResponse,
  PanesListResponseSchema,
  type PickerRowSchema,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { contracts } from "./contracts";
import { evidenceLogs, epics, gets, histories } from "./epics";
import { host } from "./host";
import { panes, paneStreams } from "./panes";
import { headless, sessions, sessionStreams } from "./sessions";

// PickerRowInit is one row of a Picker.Ask answer, as a plain object.
export type PickerRowInit = MessageInitShape<typeof PickerRowSchema>;

// AskAnswer is what Picker.Ask answers with for the words it is asked: the rows it finds, a call held open until it
// aborts, or a failure with the Connect code the call fails with.
export type AskAnswer =
  | { readonly rows: readonly PickerRowInit[] }
  | { readonly held: true }
  | { readonly failure: Code };

// Slices is the data a fixture transport answers with. evidenceLogs holds the log each run's Evidence.Artifact answers
// with, by run id. asks holds the answer Picker.Ask gives for each words, and words with no entry are refused. fail
// holds the procedures that answer Unavailable, and hold the procedures that stay unanswered until their call aborts,
// each named as "orrery.v1.Panes/List".
export interface Slices {
  readonly epics: EpicsListResponse;
  readonly contracts: ContractsListResponse;
  readonly gets: ReadonlyMap<string, EpicsGetResponse>;
  readonly histories: ReadonlyMap<string, EpicsHistoryResponse>;
  readonly panes: PanesListResponse;
  readonly sessions: ListResponse;
  readonly headless: ListHeadlessResponse;
  readonly host: HostStatusResponse;
  readonly paneStreams: ReadonlyMap<string, WatchConversationResponse>;
  readonly sessionStreams: ReadonlyMap<string, SessionsWatchConversationResponse>;
  readonly evidenceLogs: ReadonlyMap<string, string>;
  // say is how the fixture answers Control.Say. refused fails it as a session that takes no messages does, and unanswered
  // fails it as a call with no answer does. Unset, the write is absorbed.
  readonly say?: "refused" | "unanswered";
  // interrupt is how the fixture answers Control.Interrupt: with texts, the messages it cancels from the queue. then is the
  // next frame each open conversation stream of the session sends once the interrupt is called, after which the stream
  // stays open until its call aborts. Unset, the interrupt is absorbed and no texts come back.
  readonly interrupt?: { texts: string[]; then?: SessionsWatchConversationResponse };
  readonly asks: ReadonlyMap<string, AskAnswer>;
  readonly fail?: ReadonlySet<string>;
  readonly hold?: ReadonlySet<string>;
}

// shippedSlices is the data the fixture transport serves when it is given no slices.
export const shippedSlices: Slices = {
  epics,
  contracts,
  gets,
  histories,
  panes,
  sessions,
  headless,
  host,
  paneStreams,
  sessionStreams,
  evidenceLogs,
  asks: new Map<string, AskAnswer>(),
};

// emptySlices is the world with nothing running: no panes, no headless sessions, no epics and no contracts. Every other
// answer is the shipped one.
export const emptySlices: Slices = {
  ...shippedSlices,
  epics: create(EpicsListResponseSchema, { epics: [] }),
  contracts: create(ContractsListResponseSchema, { contracts: [] }),
  panes: create(PanesListResponseSchema, { panes: [] }),
  headless: create(ListHeadlessResponseSchema, { sessions: [] }),
};
