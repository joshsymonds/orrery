import type { JSX } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";

import type { createClients } from "./client";
import type { InAppNotice, NoticeKey, NoticesWatchResponse } from "./gen/orrery/v1/notice_pb";
import { Gauge, Icon, StateGlyph } from "./glyphs";
import type { NoticeAct, NoticeHead, NoticeLead } from "./noticewords";

// noticeKey names a notice's key as one string over all five fields in order, so a key that sets a different field is
// a different notice.
function noticeKey(key: NoticeKey | undefined): string {
  return JSON.stringify([key?.pane ?? "", key?.session ?? "", key?.epic ?? "", key?.limit ?? "", key?.alert ?? ""]);
}

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

// noticeRowLimit is how many rows the stack draws. The notices past it are counted in its "+n more" line.
const noticeRowLimit = 3;

interface NoticeBannerProps {
  notices: InAppNotice[];
  onOpen: (notice: InAppNotice) => void;
  onDismiss: (notice: InAppNotice) => void;
}

// NoticeBanner shows the notices as a stack of one-line rows: the first three that are still on it, then a "+n more"
// line for the rest. A row's title and body are one button, and a click calls onOpen with its notice. Its × calls
// onDismiss and drops the row at once. A row also leaves the stack noticeLifetimeMillis after it first shows, and
// nothing is told of that. Each row's clock starts when it is drawn, and a row that leaves the stack is forgotten, so a
// notice that is shown again under its key shows again. The banner makes no sound.
export function NoticeBanner({ notices, onOpen, onDismiss }: NoticeBannerProps): JSX.Element {
  // gone holds the keys of the notices this page has taken off the stack, by a × or by the lifetime.
  const [gone, setGone] = useState<ReadonlySet<string>>(() => new Set());
  // lives holds the timer of each drawn row, by key, from the render that first draws it until the row leaves.
  const lives = useRef(new Map<string, ReturnType<typeof setTimeout>>());

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

  const dismiss = (notice: InAppNotice) => {
    const key = noticeKey(notice.key);
    clearTimeout(lives.current.get(key));
    lives.current.delete(key);
    setGone((before) => new Set(before).add(key));
    onDismiss(notice);
  };

  const more = shown.length - rows.length;
  return (
    <div class="notices">
      {rows.map((notice) => (
        <div key={noticeKey(notice.key)} class="notice-row">
          <button type="button" class="notice" onClick={() => onOpen(notice)}>
            <span class="notice-title">{notice.title}</span>
            <span class="notice-separator" aria-hidden="true">
              {" · "}
            </span>
            <span class="notice-body">{notice.body}</span>
          </button>
          <button
            type="button"
            class="notice-dismiss"
            aria-label={`Dismiss ${notice.title}`}
            onClick={() => dismiss(notice)}
          >
            ×
          </button>
        </div>
      ))}
      {more > 0 && <p class="notices-more">{`+${more} more`}</p>}
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
