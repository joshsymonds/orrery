// The stories of ReleaseTable: the release of the orbit-labels epic while it runs, and the release of the offline-tables
// epic stopped at a step that failed, whose reason is drawn under its row.

import type { Stories } from "./catalog/story";
import { type ReleaseView, releaseOf } from "./ending";
import { gets } from "./fixtures/epics";
import * as release from "./release";

// viewOf is the release section of an epic the fixtures hold a detail for.
function viewOf(id: string): ReleaseView {
  const detail = gets.get(id);
  const view = detail === undefined ? null : releaseOf(detail);
  if (view === null) {
    throw new Error(`the fixtures hold no release for ${id}`);
  }
  return view;
}

export const ReleaseTable: Stories = {
  running: () => <release.ReleaseTable view={viewOf("orrery/orbit-labels")} />,
  failed: () => <release.ReleaseTable view={viewOf("tidepool/offline-tables")} />,
};
