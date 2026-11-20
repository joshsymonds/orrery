import type { VNode } from "preact";

// inboxColumn draws content in one column of the inbox at round 6's cell width, which is the inbox's own width token. The
// column takes the inbox's class, so the inbox's styles apply to the rows, directories and epic rows drawn in it, as they
// do in the app's inbox column. It is a story-only frame: the stories of the rows and the directory draw in it.
export function inboxColumn(content: VNode): VNode {
  return (
    <div class="inbox" style={{ width: "var(--o-layout-inbox-width)" }}>
      {content}
    </div>
  );
}
