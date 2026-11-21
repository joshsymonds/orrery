import type { JSX, RefObject } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { createClients } from "./client";
import type { InAppNotice, NoticesWatchResponse } from "./gen/orrery/v1/notice_pb";
import { Gauge, Icon, StateGlyph } from "./glyphs";
import type { Row } from "./rows";
import { type NoticeAct, type NoticeHead, noticeKey, type NoticeLead } from "./noticewords";

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
  act: (notice: InAppNotice) => NoticeAct;
  // bringBackRow is the listed row a notice's Bring it back takes, or undefined when the notice has no such control.
  bringBackRow: (notice: InAppNotice) => Row | undefined;
  onOpen: (notice: InAppNotice) => void;
  // onBringBack takes the row a Bring it back takes, and resolves "" when the server takes the call or the server's
  // reason when it refuses it.
  onBringBack: (row: Row) => Promise<string>;
  onRead: (notice: InAppNotice) => void;
  onMore: () => void;
}

// NoticeStack shows the notices as a stack of two-line rows under the page's title row: the first three that are still on
// it, then an "N more in Notices" link for the rest, which calls onMore. A row's Answer or Open calls onOpen with its
// notice, and a row's Bring it back calls onBringBack with the row it takes, leaving the notice on the stack. A refused
// Bring it back shows the reason it resolves on that row. Its Mark read calls onRead and drops the row at once. A row
// also leaves the stack noticeLifetimeMillis after it first shows, and nothing is told of that. Each row's clock starts
// when it is drawn, and a row that leaves the stack is forgotten, so a notice that is shown again under its key shows
// again. The stack makes no sound.
export function NoticeStack({
  notices,
  head,
  age,
  act,
  bringBackRow,
  onOpen,
  onBringBack,
  onRead,
  onMore,
}: NoticeStackProps): JSX.Element {
  // gone holds the keys of the notices this page has taken off the stack, by a Mark read or by the lifetime.
  const [gone, setGone] = useState<ReadonlySet<string>>(() => new Set());
  // refusals holds the reason of each row's latest refused Bring it back, by key. A row whose Bring it back is taken
  // has none.
  const [refusals, setRefusals] = useState<ReadonlyMap<string, string>>(() => new Map());
  // lives holds the timer of each drawn row, by key, from the render that first draws it until the row leaves.
  const lives = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  // calls numbers each Bring it back in the order it is made, and latest holds the number of each row's latest one, so
  // a reply to an earlier call than a row's latest, or to a row that has left, is not kept.
  const calls = useRef(0);
  const latest = useRef(new Map<string, number>());
  const stack = useRef<HTMLDivElement>(null);

  const keys = new Set(notices.map((notice) => noticeKey(notice.key)));
  const shown = notices.filter((notice) => !gone.has(noticeKey(notice.key)));
  const shownKeys = new Set(shown.map((notice) => noticeKey(notice.key)));
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
    for (const key of latest.current.keys()) {
      if (!shownKeys.has(key)) {
        latest.current.delete(key);
      }
    }
    if ([...refusals.keys()].some((key) => !shownKeys.has(key))) {
      setRefusals((before) => new Map([...before].filter(([key]) => shownKeys.has(key))));
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

  // bringBackOf makes the row's Bring it back. The reply keeps the server's reason on the row when it refuses, and
  // clears the row's reason when the server takes the call.
  const bringBackOf = (notice: InAppNotice, row: Row) => {
    const key = noticeKey(notice.key);
    calls.current += 1;
    const call = calls.current;
    latest.current.set(key, call);
    void onBringBack(row).then((reason) => {
      if (latest.current.get(key) !== call) {
        return;
      }
      setRefusals((before) => {
        const next = new Map(before);
        if (reason === "") {
          next.delete(key);
        } else {
          next.set(key, reason);
        }
        return next;
      });
    });
  };

  const more = shown.length - rows.length;
  return (
    <div class="notices" ref={stack}>
      {rows.map((notice) => {
        const key = noticeKey(notice.key);
        const bringBack = bringBackRow(notice);
        return (
          <Notice
            key={key}
            head={head(notice)}
            when={age(notice)}
            body={notice.body}
            act={act(notice)}
            onAct={() => onOpen(notice)}
            onBringBack={bringBack === undefined ? undefined : () => bringBackOf(notice, bringBack)}
            refusal={refusals.get(key)}
            onRead={() => readNotice(notice)}
          />
        );
      })}
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
  onBringBack?: () => void;
  // refusal is the reason a Bring it back was refused, drawn as a line under the body, or undefined when none was.
  refusal?: string;
  onRead?: () => void;
  flat?: boolean;
  unread?: boolean;
  onClick?: () => void;
}

// Notice draws one notice in two lines, as round 6 draws it. The first line is its lead, its directory in the place
// colour and its words, then its age. The second is the body's first line, since a body may hold several lines. A stack
// row is a status block with its primary control, Answer or Open when it has one, a quiet Bring it back when it has
// onBringBack, and a quiet Mark read; a refusal of that Bring it back is drawn as a line under the body. A flat row is
// one button, marked unread while it is unread, and holds no control.
export function Notice({
  head,
  when,
  body,
  act = null,
  onAct,
  onBringBack,
  refusal,
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
      {refusal !== undefined && <span class="refusal">{refusal}</span>}
      <span class="acts">
        {act !== null && (
          <button type="button" class={act === "Answer" ? "btn primary" : "btn"} onClick={onAct}>
            {act}
          </button>
        )}
        {onBringBack !== undefined && (
          <button type="button" class="btn quiet" onClick={onBringBack}>
            Bring it back
          </button>
        )}
        <button type="button" class="btn quiet" onClick={onRead}>
          Mark read
        </button>
      </span>
    </div>
  );
}
