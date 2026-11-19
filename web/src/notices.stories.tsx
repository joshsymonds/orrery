// The stories of NoticeBanner: the stack of notices a banner shows, one notice, three, and more than three with its
// "+n more" line. The notices come from applyNotice, the function the app folds its notice stream with, so each is shown
// as the stream would show it. The banner's open and dismiss handlers do nothing.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { type InAppNotice, InAppNoticeSchema, NoticesWatchResponseSchema } from "./gen/orrery/v1/notice_pb";
import { applyNotice, NoticeBanner as Banner } from "./notices";

const noop = () => {};

// stack is the notices a banner shows after each of them is shown in order.
function stack(...notices: MessageInitShape<typeof InAppNoticeSchema>[]): InAppNotice[] {
  return notices.reduce<InAppNotice[]>(
    (showing, value) => applyNotice(showing, create(NoticesWatchResponseSchema, { change: { case: "shown", value } })),
    [],
  );
}

const waiting = { key: { pane: "%12" }, state: "waiting", title: "beacon-schedule", body: "asks which index to add" };
const interrupted = {
  key: { session: "4be0c6d2-8f3a-4c71-9e25-ephemeris001" },
  state: "interrupted",
  title: "ephemeris-import",
  body: "orreryd restarted during the turn",
};
const question = {
  key: { epic: "lighthouse/rota-export" },
  state: "waiting",
  title: "rota-export",
  body: "Bump the embedded tz database?",
};
const limited = { key: { limit: "weekly@1794700000" }, state: "limited", title: "halyard", body: "usage limit reached" };
const finished = { key: { epic: "tidepool/tide-alerts" }, state: "done", title: "tide-alerts", body: "epic integrated" };
const holder = { key: { epic: "orrery/holder-scopes" }, state: "done", title: "holder-scopes", body: "epic integrated" };

export const NoticeBanner: Stories = {
  "one-notice": () => <Banner notices={stack(waiting)} onOpen={noop} onDismiss={noop} />,
  "three-notices": () => <Banner notices={stack(waiting, interrupted, question)} onOpen={noop} onDismiss={noop} />,
  "over-the-limit": () => (
    <Banner notices={stack(waiting, interrupted, question, limited, finished, holder)} onOpen={noop} onDismiss={noop} />
  ),
};
