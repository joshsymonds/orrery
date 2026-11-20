import type { JSX, RefObject } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { createClients } from "./client";
import type { InAppNotice, NoticesWatchResponse } from "./gen/orrery/v1/notice_pb";
import { Gauge, Icon, StateGlyph } from "./glyphs";
import { type NoticeAct, type NoticeHead, noticeAct, noticeKey, type NoticeLead } from "./noticewords";

// applyNotice applies one change to the notices showing. A shown notice replaces the one under its key in place, or
// joins the end of the list in the order it was first shown. An ended notice leaves the list. The list it is given is
// not changed.
export function applyNotice(showing: InAppNotice[], change: NoticesWatchResponse): InAppNotice[] {
  const oneof = change.change;
  switch (oneof.case) {
    case "shown": {
      const key = noticeKey(oneof.value.key);
      const index = showing.findIndex((notice) => noticeKey(notice.key) === key);
      if (index === -1) {
        return [...showing, oneof.value];
      }
      return showing.map((notice, i) => (i === index ? oneof.value : notice));
    }
    case "ended": {
      const key = noticeKey(oneof.value);
      return showing.filter((notice) => noticeKey(notice.key) !== key);
    }
    default:
      return showing;
  }
}

// pause resolves after ms, or as soon as signal aborts. It takes its abort listener off when the timer fires, so a
// long-lived signal does not collect one listener per retry.
function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal.addEventListener("abort", done);
  });
}

// watchNotices streams Notices.Watch into onChange, which receives the notices showing after each change. A stream that
// ends or fails clears the list, waits retryMs and reopens, until signal aborts. Once signal aborts the stream and any
// wait end, and nothing reaches onChange after it.
export async function watchNotices(
  clients: ReturnType<typeof createClients>,
  onChange: (notices: InAppNotice[]) => void,
  retryMs: number,
  signal: AbortSignal,
): Promise<void> {
  let showing: InAppNotice[] = [];
  while (!signal.aborted) {
    try {
      for await (const change of clients.notices.watch({}, { signal })) {
        if (signal.aborted) {
          return;
        }
        showing = applyNotice(showing, change);
        onChange(showing);
      }
    } catch {
      // A failed stream clears the list below, the same as one that ends.
    }
    if (signal.aborted) {
      return;
    }
    showing = [];
    onChange(showing);
    await pause(retryMs, signal);
  }
}

// noticeLifetimeMillis is how long a row stays on the stack after it first shows. The row then leaves without a seen
// mark, so the notice stays unseen where the page lists what needs the owner.
export const noticeLifetimeMillis = 8000;

// noticeRowLimit is how many rows the stack draws. The notices past it are counted in its "N more in Notices" link.
const noticeRowLimit = 3;

// placeUnderTitleRow sets the element's top to the bottom of the page's title row, measured from .panes, so an overlay
// in .panes sits under the title row and never over it. The page's title row is the visible title row outside .list
// whose right edge is furthest right, and its bottom takes in its strip, which the row draws. With no such row, or no
// .panes around the element, the element keeps the top style.css gives it.
export function placeUnderTitleRow(element: HTMLElement): void {
  const panes = element.closest<HTMLElement>(".panes");
  if (panes === null) {
    return;
  }
  let page: DOMRect | undefined;
  for (const row of panes.querySelectorAll<HTMLElement>(".title-row")) {
    if (row.closest(".list, [hidden]") !== null) {
      continue;
    }
    const rect = row.getBoundingClientRect();
    if (page === undefined || rect.right > page.right) {
      page = rect;
    }
  }
  element.style.top = page === undefined ? "" : `${page.bottom - panes.getBoundingClientRect().top}px`;
}

// useUnderTitleRow places the element that ref holds under the page's title row after every commit, so it follows the
// title row as the page draws it, and again when the reading area resizes: through a ResizeObserver where one is
// present, else on the window's resize. While ref holds nothing, the element is not drawn and nothing is placed.
export function useUnderTitleRow(ref: RefObject<HTMLElement>): void {
  useLayoutEffect(() => {
    const element = ref.current;
    if (element === null) {
      return;
    }
    placeUnderTitleRow(element);
    const panes = element.closest<HTMLElement>(".panes");
    if (panes === null) {
      return;
    }
    if (typeof ResizeObserver === "function") {
      const observer = new ResizeObserver(() => placeUnderTitleRow(element));
      observer.observe(panes);
      return () => observer.disconnect();
    }
    const onResize = () => placeUnderTitleRow(element);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  });
}

interface NoticeStackProps {
  notices: InAppNotice[];
  head: (notice: InAppNotice) => NoticeHead;
  age: (notice: InAppNotice) => string;
  onOpen: (notice: InAppNotice) => void;
  onRead: (notice: InAppNotice) => void;
  onMore: () => void;
}

// NoticeStack shows the notices as a stack of two-line rows under the page's title row: the first three that are still on
// it, then an "N more in Notices" link for the rest, which calls onMore. A row's Answer or Open calls onOpen with its
// notice, and its Mark read calls onRead and drops the row at once. A row also leaves the stack noticeLifetimeMillis
// after it first shows, and nothing is told of that. Each row's clock starts when it is drawn, and a row that leaves the
// stack is forgotten, so a notice that is shown again under its key shows again. The stack makes no sound.
export function NoticeStack({ notices, head, age, onOpen, onRead, onMore }: NoticeStackProps): JSX.Element {
  // gone holds the keys of the notices this page has taken off the stack, by a Mark read or by the lifetime.
  const [gone, setGone] = useState<ReadonlySet<string>>(() => new Set());
  // lives holds the timer of each drawn row, by key, from the render that first draws it until the row leaves.
  const lives = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const stack = useRef<HTMLDivElement>(null);

  const keys = new Set(notices.map((notice) => noticeKey(notice.key)));
  const shown = notices.filter((notice) => !gone.has(noticeKey(notice.key)));
  const rows = shown.slice(0, noticeRowLimit);

  // The timers run in the commit itself, not in an effect that waits for a paint, so a row's clock starts when it
  // draws.
  useLayoutEffect(() => {
    for (const [key, timer] of lives.current) {
      if (!keys.has(key)) {
        clearTimeout(timer);
        lives.current.delete(key);
      }
    }
    for (const notice of rows) {
      const key = noticeKey(notice.key);
      if (!lives.current.has(key)) {
        lives.current.set(
          key,
          setTimeout(() => {
            lives.current.delete(key);
            setGone((before) => new Set(before).add(key));
          }, noticeLifetimeMillis),
        );
      }
    }
    if ([...gone].some((key) => !keys.has(key))) {
      setGone((before) => new Set([...before].filter((key) => keys.has(key))));
    }
  });

  useLayoutEffect(
    () => () => {
      for (const timer of lives.current.values()) {
        clearTimeout(timer);
      }
      lives.current.clear();
    },
    [],
  );

  useUnderTitleRow(stack);

  const readNotice = (notice: InAppNotice) => {
    const key = noticeKey(notice.key);
    clearTimeout(lives.current.get(key));
    lives.current.delete(key);
    setGone((before) => new Set(before).add(key));
    onRead(notice);
  };

  const more = shown.length - rows.length;
  return (
    <div class="notices" ref={stack}>
      {rows.map((notice) => (
        <Notice
          key={noticeKey(notice.key)}
          head={head(notice)}
          when={age(notice)}
          body={notice.body}
          act={noticeAct(notice)}
          onAct={() => onOpen(notice)}
          onRead={() => readNotice(notice)}
        />
      ))}
      {more > 0 && (
        <button type="button" class="link notices-more" onClick={onMore}>
          {`${more} more in Notices`}
        </button>
      )}
    </div>
  );
}

// NoticeLeadMark draws a notice's lead: its epic's gauge, its session's or pane's state glyph, or its icon.
function NoticeLeadMark({ lead }: { lead: NoticeLead }): JSX.Element {
  switch (lead.kind) {
    case "gauge":
      return <Gauge {...lead.gauge} />;
    case "glyph":
      return <StateGlyph state={lead.state} />;
    case "icon":
      return <Icon name={lead.icon} />;
  }
}

interface NoticeProps {
  head: NoticeHead;
  when: string;
  body: string;
  act?: NoticeAct;
  onAct?: () => void;
  onRead?: () => void;
  flat?: boolean;
  unread?: boolean;
  onClick?: () => void;
}

// Notice draws one notice in two lines, as round 6 draws it. The first line is its lead, its directory in the place
// colour and its words, then its age. The second is the body's first line, since a body may hold several lines. A stack
// row is a status block with its primary control, Answer or Open when it has one, and a quiet Mark read. A flat row is
// one button, marked unread while it is unread, and holds no control.
export function Notice({
  head,
  when,
  body,
  act = null,
  onAct,
  onRead,
  flat = false,
  unread = false,
  onClick,
}: NoticeProps): JSX.Element {
  const words = (
    <>
      <NoticeLeadMark lead={head.lead} />
      <span class="t">
        {head.dir !== "" && (
          <>
            <span class="dirn">{head.dir}</span>{" "}
          </>
        )}
        {head.what}
      </span>
      <span class="when">{when}</span>
      <span class="b">{body.split("\n")[0]}</span>
    </>
  );
  if (flat) {
    return (
      <button type="button" class={unread ? "notice flat unread" : "notice flat"} onClick={onClick}>
        {words}
      </button>
    );
  }
  return (
    <div class="notice" role="status">
      {words}
      <span class="acts">
        {act !== null && (
          <button type="button" class={act === "Answer" ? "btn primary" : "btn"} onClick={onAct}>
            {act}
          </button>
        )}
        <button type="button" class="btn quiet" onClick={onRead}>
          Mark read
        </button>
      </span>
    </div>
  );
}
