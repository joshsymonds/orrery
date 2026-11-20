// The fixture slices: the data the in-memory fixture transport answers with, one field per answer. shippedSlices is the
// set the transport serves when it is given none.

import type { MessageInitShape } from "@bufbuild/protobuf";
import type { Code } from "@connectrpc/connect";

import type {
  EpicsGetResponse,
  EpicsHistoryResponse,
  EpicsListResponse,
} from "../gen/orrery/v1/epics_pb";
import type {
  HostStatusResponse,
  ListHeadlessResponse,
  ListResponse,
  PanesListResponse,
  PickerRowSchema,
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
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
// with, by run id. asks holds the answer Picker.Ask gives for each words, and words with no entry are refused.
export interface Slices {
  readonly epics: EpicsListResponse;
  readonly gets: ReadonlyMap<string, EpicsGetResponse>;
  readonly histories: ReadonlyMap<string, EpicsHistoryResponse>;
  readonly panes: PanesListResponse;
  readonly sessions: ListResponse;
  readonly headless: ListHeadlessResponse;
  readonly host: HostStatusResponse;
  readonly paneStreams: ReadonlyMap<string, WatchConversationResponse>;
  readonly sessionStreams: ReadonlyMap<string, SessionsWatchConversationResponse>;
  readonly evidenceLogs: ReadonlyMap<string, string>;
  readonly asks: ReadonlyMap<string, AskAnswer>;
}

// shippedSlices is the data the fixture transport serves when it is given no slices.
export const shippedSlices: Slices = {
  epics,
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
