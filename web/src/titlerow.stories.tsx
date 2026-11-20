// The stories of TitleRow: a breadcrumb with a state, a blocker, the inbox's count on the mark, the commands a page
// holds.

import type { Stories } from "./catalog/story";
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
  "inbox-hidden": () => (
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
  conversation: () => (
    <titlerow.TitleRow
      crumbs={[{ label: "tidepool", place: true }, { label: "Tide table cache warm-up" }]}
      onBack={ignore}
    >
      <button type="button" class="icon find" aria-label="Find in this conversation" title="Find  /">
        ⌕
      </button>
      <button type="button" class="icon more" aria-label="More" title="Archive, hide meters, keys" aria-expanded={false}>
        ⋯
      </button>
    </titlerow.TitleRow>
  ),
  "with-error": () => (
    <titlerow.TitleRow
      crumbs={[{ label: "tidepool", place: true }, { label: "Tide table cache warm-up" }]}
      onBack={ignore}
      note="Could not archive this session: the server did not answer. Nothing changed."
    >
      <button type="button" class="icon find" aria-label="Find in this conversation" title="Find  /">
        ⌕
      </button>
      <button type="button" class="icon more" aria-label="More" title="Archive, hide meters, keys" aria-expanded={false}>
        ⋯
      </button>
    </titlerow.TitleRow>
  ),
};
