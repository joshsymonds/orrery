import { Fragment, type JSX } from "preact";

import type { ListedNotice } from "./gen/orrery/v1/notice_pb";
import { Icon } from "./glyphs";
import { Notice } from "./notices";
import { type NoticeHead, noticeClock, noticeDays, noticeKey } from "./noticewords";
import { TitleRow } from "./titlerow";

export interface NoticeCentreProps {
  rows: readonly ListedNotice[];
  failed: boolean;
  head: (row: ListedNotice) => NoticeHead;
  now: number;
  timeZone?: string;
  narrow: boolean;
  onOpen: (row: ListedNotice) => void;
  onReadAll: () => void;
  onClose: () => void;
}

// NoticeCentre draws where notices collect: the notice log's rows by day, newest first, each a flat Notice with its clock
// and unread marked. Mark all read shows while any row is unread, and Close calls onClose; esc reaches it through App.
// With no rows it says what collects there. A failed read says so above the rows. Wide, it is an overlay under the title
// row; under 760px, narrow, it is a page titled Notices with Back, which calls onClose.
export function NoticeCentre({
  rows,
  failed,
  head,
  now,
  timeZone,
  narrow,
  onOpen,
  onReadAll,
  onClose,
}: NoticeCentreProps): JSX.Element {
  const unread = rows.some((listed) => !listed.read);
  const markAll = unread ? (
    <button type="button" class="btn quiet" onClick={onReadAll}>
      Mark all read
    </button>
  ) : null;

  const list = (
    <>
      {failed && (
        <p class="side-note unread" role="alert">
          The notices could not be read.
        </p>
      )}
      {rows.length === 0 ? (
        <div class="blank">
          <span class="t-body">No notices.</span>
          <span>Questions, endings and failures from sessions and epics you are not looking at collect here.</span>
        </div>
      ) : (
        noticeDays(rows, now, timeZone).map((day, index) => (
          <Fragment key={index}>
            <div class="day">{day.head}</div>
            <div class="nlist">
              {day.rows.map((listed) => (
                <Notice
                  key={`${noticeKey(listed.key)}|${String(listed.time)}`}
                  head={head(listed)}
                  when={noticeClock(listed.time, timeZone)}
                  body={listed.body}
                  flat
                  unread={!listed.read}
                  onClick={() => onOpen(listed)}
                />
              ))}
            </div>
          </Fragment>
        ))
      )}
    </>
  );

  if (narrow) {
    return (
      <section class="ncentre flat" aria-label="notices">
        <TitleRow crumbs={[{ label: "Notices" }]} onBack={onClose}>
          {markAll}
        </TitleRow>
        {list}
      </section>
    );
  }
  return (
    <section class="ncentre" role="dialog" aria-label="notices">
      <div class="head">
        <h2 class="t-title">Notices</h2>
        {markAll}
        <button type="button" aria-label="Close" title="Close  esc" onClick={onClose}>
          <Icon name="close" />
        </button>
      </div>
      {list}
    </section>
  );
}
