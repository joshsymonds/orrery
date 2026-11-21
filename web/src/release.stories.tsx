// The stories of ReleaseTable: the release of the orbit-labels epic while it runs, and the release of the offline-tables
// epic stopped at a step that failed with no undo, so Release from step 2 is offered. The callbacks answer at once.

import type { Stories } from "./catalog/story";
import { type ReleaseView, releaseOf } from "./ending";
import { gets } from "./fixtures/epics";
import type { CallOutcome } from "./ownercall";
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

const landed = (): Promise<CallOutcome> => Promise.resolve({ kind: "landed" });
const reread = (): Promise<boolean> => Promise.resolve(true);

// stoppedView is the release of offline-tables with its step 2 undo set to none, which offers Release from step 2.
function stoppedView(): ReleaseView {
  const detail = gets.get("tidepool/offline-tables");
  if (detail === undefined) {
    throw new Error("the fixtures hold no detail for tidepool/offline-tables");
  }
  const view = releaseOf({
    ...detail,
    releaseSteps: detail.releaseSteps.map((step) => (step.position === 2n ? { ...step, undo: "none" } : step)),
  });
  if (view === null) {
    throw new Error("the fixtures hold no release for tidepool/offline-tables");
  }
  return view;
}

export const ReleaseTable: Stories = {
  running: () => (
    <release.ReleaseTable view={viewOf("orrery/orbit-labels")} narrow={false} onRelease={landed} reread={reread} />
  ),
  failed: () => <release.ReleaseTable view={stoppedView()} narrow={false} onRelease={landed} reread={reread} />,
};
