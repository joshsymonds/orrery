// Fold is how the phone header draws: open, folded to its reading line, or hidden for good.
export type Fold = "open" | "reading" | "hidden";

// foldOf is the header's fold. Only the narrow layout folds. The owner's hide holds across conversations; a scroll
// off the newest line folds the header to its reading line until the owner taps it.
export function foldOf(narrow: boolean, hidden: boolean, atNewest: boolean, tapped: boolean): Fold {
  if (!narrow) {
    return "open";
  }
  if (hidden) {
    return "hidden";
  }
  if (!atNewest && !tapped) {
    return "reading";
  }
  return "open";
}

// HeadStore is the per-device setting that hides the phone header.
export interface HeadStore {
  hidden(): boolean;
  setHidden(hidden: boolean): void;
}

const headKey = "orrery.head.hidden";

// memoryHeadStore keeps the setting in memory, for a page with no storage to keep it in.
export function memoryHeadStore(initial = false): HeadStore {
  let hidden = initial;
  return {
    hidden: () => hidden,
    setHidden: (value) => {
      hidden = value;
    },
  };
}

// localHeadStore keeps the setting in storage. A null or throwing storage reads false and drops writes, so the header
// shows as it would on a first visit.
export function localHeadStore(storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> | null): HeadStore {
  return {
    hidden() {
      try {
        return storage?.getItem(headKey) === "1";
      } catch {
        return false;
      }
    },
    setHidden(hidden) {
      try {
        if (hidden) {
          storage?.setItem(headKey, "1");
        } else {
          storage?.removeItem(headKey);
        }
      } catch {
        // Storage is unavailable, so the write is dropped and the setting is not kept.
      }
    },
  };
}
