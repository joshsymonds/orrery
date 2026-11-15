import type { JSX } from "preact";

import type { createClients } from "./client";
import type { InAppNotice, NoticeKey, NoticesWatchResponse } from "./gen/orrery/v1/notice_pb";

// noticeKey names a notice's key as one string over all four fields in order, so a key that sets a different field is
// a different notice.
function noticeKey(key: NoticeKey | undefined): string {
  return JSON.stringify([key?.pane ?? "", key?.session ?? "", key?.epic ?? "", key?.limit ?? ""]);
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

interface NoticeBannerProps {
  notices: InAppNotice[];
  onOpen: (notice: InAppNotice) => void;
}

// NoticeBanner shows each notice as one button with its title and body. A click calls onOpen with that notice. The
// banner makes no sound.
export function NoticeBanner({ notices, onOpen }: NoticeBannerProps): JSX.Element {
  return (
    <div class="notices">
      {notices.map((notice) => (
        <button key={noticeKey(notice.key)} type="button" class="notice" onClick={() => onOpen(notice)}>
          <span class="notice-title">{notice.title}</span>
          <span class="notice-body">{notice.body}</span>
        </button>
      ))}
    </div>
  );
}
