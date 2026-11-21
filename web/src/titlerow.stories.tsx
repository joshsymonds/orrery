// The stories of TitleRow: a breadcrumb with a state, a blocker, the commands a page holds, and a conversation's title
// row, drawn in its head: its project's name as the inbox draws it, then its title one step down the type scale, with no
// Orrery icon or count (the inbox shows the icon, in its head or on its rail), a title too long for its line, and an error
// note.

import type { Stories } from "./catalog/story";
import * as titlerow from "./titlerow";

const ignore = () => {};

// ConvoCommands are the commands a conversation's title row holds: find, and More.
function ConvoCommands() {
  return (
    <>
      <button type="button" class="icon find" aria-label="Find in this conversation" title="Find  /">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5" /></svg>
      </button>
      <button type="button" class="icon more" aria-label="More" title="Archive, hide meters, keys" aria-expanded={false}>
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z" /></svg>
      </button>
    </>
  );
}

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
    <div class="convo-head">
      <titlerow.TitleRow
        crumbs={[
          { label: "commonframe", place: true },
          { label: "Merging main into a story-out client branch, resolving four conflicts in the dock and the reader" },
        ]}
        onBack={ignore}
      >
        <ConvoCommands />
      </titlerow.TitleRow>
    </div>
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
    <div class="convo-head">
      <titlerow.TitleRow
        crumbs={[{ label: "tidepool", place: true }, { label: "Tide table cache warm-up" }]}
        onBack={ignore}
      >
        <ConvoCommands />
      </titlerow.TitleRow>
    </div>
  ),
  "with-error": () => (
    <div class="convo-head">
      <titlerow.TitleRow
        crumbs={[{ label: "tidepool", place: true }, { label: "Tide table cache warm-up" }]}
        onBack={ignore}
        note="Could not archive this session: the server did not answer. Nothing changed."
      >
        <ConvoCommands />
      </titlerow.TitleRow>
    </div>
  ),
};
