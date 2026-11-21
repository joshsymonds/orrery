// The stories of NoticeCentre, as round 6 draws it: the centre by day, and the empty centre. The rows are the notice
// history the fixtures serve, and each head is noticeHead's for a notice with no listed project or row, at the fixtures'
// instant.

import type { Stories } from "./catalog/story";
import type { ListedNotice } from "./gen/orrery/v1/notice_pb";
import { liveEpoch } from "./fixtures/clock";
import { noticeHistory } from "./fixtures/notices";
import { NoticeCentre as Centre } from "./noticecentre";
import { type NoticeHead, noticeHead } from "./noticewords";

const noop = () => {};

const now = Date.parse(liveEpoch) / 1000;

const head = (row: ListedNotice): NoticeHead => noticeHead(row, [], [], now);

export const NoticeCentre: Stories = {
  days: () => (
    <Centre
      rows={noticeHistory}
      failed={false}
      head={head}
      now={now}
      timeZone="UTC"
      narrow={false}
      onOpen={noop}
      onReadAll={noop}
      onClose={noop}
    />
  ),
  empty: () => (
    <Centre
      rows={[]}
      failed={false}
      head={head}
      now={now}
      timeZone="UTC"
      narrow={false}
      onOpen={noop}
      onReadAll={noop}
      onClose={noop}
    />
  ),
};
