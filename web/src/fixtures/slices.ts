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
  // say is how the fixture answers Control.Say. refused fails it as a session that takes no messages does, and unanswered
  // fails it as a call with no answer does. Unset, the write is absorbed.
  readonly say?: "refused" | "unanswered";
  // interrupt is how the fixture answers Control.Interrupt: with texts, the messages it cancels from the queue. then is the
  // next frame each open conversation stream of the session sends once the interrupt is called, after which the stream
  // stays open until its call aborts. Unset, the interrupt is absorbed and no texts come back.
  readonly interrupt?: { texts: string[]; then?: SessionsWatchConversationResponse };
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
