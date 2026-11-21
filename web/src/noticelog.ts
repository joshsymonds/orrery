import { useCallback, useEffect, useRef, useState } from "preact/hooks";

import type { Clients } from "./actions";
import type { ListedNotice } from "./gen/orrery/v1/notice_pb";
import type { SeenClock, SeenWindow } from "./seen";

// noticeRelistMillis is how often the notice log is read again while the page is visible.
export const noticeRelistMillis = 60_000;

// NoticeLog is the notice history a page keeps: its rows, newest first, whether the latest List failed, how many rows are
// unread, and relist, which reads the history again.
export interface NoticeLog {
  rows: readonly ListedNotice[];
  failed: boolean;
  unread: number;
  relist: () => void;
}

interface Listed {
  rows: readonly ListedNotice[];
  failed: boolean;
}

// noRelist is the relist of a log that is not mounted: it lists nothing.
const noRelist = (): void => {};

// useNoticeLog reads Notices.List at mount, each time the page becomes visible, the window takes focus, watched changes
// identity, and relist is called, and every noticeRelistMillis while the page is visible. A List that fails keeps the
// last rows and sets failed, which a later answer clears. An answer is applied only if it answers the latest List sent,
// so an older answer that lands last changes nothing. Unmounting removes every listener and the timer.
export function useNoticeLog(clients: Clients, win: SeenWindow, clock: SeenClock, watched: unknown): NoticeLog {
  const [listed, setListed] = useState<Listed>({ rows: [], failed: false });
  // list is the List of the mounted subscription below, which relist calls, and noRelist while none is mounted.
  const list = useRef<() => void>(noRelist);
  const relist = useCallback(() => list.current(), []);

  useEffect(() => {
    let latest = 0;
    const visible = () => win.document.visibilityState === "visible";
    const read = () => {
      latest++;
      const request = latest;
      clients.notices.list({}).then(
        (response) => {
          if (request === latest) {
            setListed({ rows: response.notices, failed: false });
          }
        },
        () => {
          if (request === latest) {
            setListed((before) => ({ rows: before.rows, failed: true }));
          }
        },
      );
    };
    let timer: unknown;
    const tick = () => {
      timer = clock.setTimeout(tick, noticeRelistMillis);
      if (visible()) {
        read();
      }
    };
    const onVisibility = () => {
      if (visible()) {
        read();
      }
    };
    timer = clock.setTimeout(tick, noticeRelistMillis);
    win.document.addEventListener("visibilitychange", onVisibility);
    win.addEventListener("focus", read);
    list.current = read;
    return () => {
      list.current = noRelist;
      clock.clearTimeout(timer);
      win.document.removeEventListener("visibilitychange", onVisibility);
      win.removeEventListener("focus", read);
    };
  }, [clients, win, clock]);

  // The subscription above is set up before this effect runs, so a change of watched, mount included, lists at once.
  useEffect(() => {
    list.current();
  }, [watched]);

  const unread = listed.rows.filter((row) => !row.read).length;
  return { rows: listed.rows, failed: listed.failed, unread, relist };
}
