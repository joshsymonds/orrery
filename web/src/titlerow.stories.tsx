// The stories of TitleRow: a breadcrumb with a state, a blocker, the inbox's count on the mark, the commands a page
// holds, and a last crumb that is a session's title.

import type { Stories } from "./catalog/story";
import * as titleparts from "./titleparts";
import * as titlerow from "./titlerow";
import type { InboxMark } from "./titlerow";

const ignore = () => {};

const withCount: InboxMark = { needs: 3, enter: ignore, leave: ignore, pin: ignore };

export const TitleRow: Stories = {
  "crumbs-and-state": () => (
    <titlerow.TitleRow
      crumbs={[{ label: "Attention", onGo: ignore }, { label: "rota-export" }]}
      onBack={ignore}
      state="building"
    />
  ),
  "blocker": () => (
    <titlerow.TitleRow
      crumbs={[{ label: "Attention", onGo: ignore }, { label: "rota-export", onGo: ignore }, { label: "T2 · implementing" }]}
      onBack={ignore}
      state="working"
      blocker="waiting 4 min on halyard"
    />
  ),
  "inbox-count": () => (
    <titlerow.TitleRow crumbs={[{ label: "rota-export" }]} onBack={ignore} mark={withCount} />
  ),
  "with-commands": () => (
    <titlerow.TitleRow crumbs={[{ label: "rota-export" }]} onBack={ignore} state="building">
      <button type="button" class="act">
        Talk
      </button>
      <button type="button" class="act">
        Pause
      </button>
    </titlerow.TitleRow>
  ),
  "session-title": () => (
    <titlerow.TitleRow
      crumbs={[
        { label: "Sessions", onGo: ignore },
        { label: <titleparts.TitleParts dir="/home/owner/code/lighthouse" line="adding retry backoff" /> },
      ]}
      onBack={ignore}
      state="working"
    />
  ),
};
