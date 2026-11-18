// The fixture slices: the data the in-memory fixture transport answers with, one field per answer. shippedSlices is the
// set the transport serves when it is given none.

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
  SessionsWatchConversationResponse,
  WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { evidenceLogs, epics, gets, histories } from "./epics";
import { host } from "./host";
import { panes, paneStreams } from "./panes";
import { headless, sessions, sessionStreams } from "./sessions";

// Slices is the data a fixture transport answers with. evidenceLogs holds the log each run's Evidence.Artifact answers
// with, by run id.
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
};
