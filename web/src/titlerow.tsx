import { type ComponentChildren, Fragment, type JSX } from "preact";

import { Mark } from "./mark";

export interface Crumb {
  label: string;
  onGo?: () => void;
}

// InboxMark is the inbox's hold on the mark every title row draws first: the count of what needs you, the hover that
// pops the inbox out, the leave that tucks it away unless the pointer moved into the inbox, and the click that pins it.
export interface InboxMark {
  needs: number;
  enter: () => void;
  leave: (to: EventTarget | null) => void;
  pin: () => void;
}

// noInbox is the mark of a title row no inbox answers for: no count, and no action.
const noInbox: InboxMark = { needs: 0, enter: () => {}, leave: () => {}, pin: () => {} };

// TitleRow is the title row every epic page shares: the back button, the breadcrumb, the state, the blocker, and the
// commands the children are. The breadcrumb opens on the Orrery mark, which carries the count and drives the inbox.
// Back and every crumb but the last call their handlers; a crumb with no handler is a plain label, and the last crumb
// is the page itself, a span marked as the current page. Every text is a child text node, so markup in a label shows as
// text.
export function TitleRow({
  crumbs,
  onBack,
  state,
  blocker,
  mark = noInbox,
  children,
}: {
  crumbs: Crumb[];
  onBack: () => void;
  state?: string;
  blocker?: string;
  mark?: InboxMark;
  children?: ComponentChildren;
}): JSX.Element {
  const last = crumbs.length - 1;
  return (
    <header class="title-row">
      <button type="button" class="back" aria-label="back" onClick={() => onBack()}>
        ‹
      </button>
      <nav class="crumbs" aria-label="where">
        <button
          type="button"
          class="mark-crumb"
          aria-label="inbox"
          onClick={() => mark.pin()}
          onMouseEnter={() => mark.enter()}
          onMouseLeave={(event) => mark.leave(event.relatedTarget)}
        >
          <Mark />
          {mark.needs > 0 && <span class="needs">{mark.needs}</span>}
        </button>
        {crumbs.map((crumb, index) => (
          <Fragment key={index}>
            {" "}
            <span class="sep">›</span>{" "}
            {index === last ? (
              <span class="crumb here" aria-current="page">
                {crumb.label}
              </span>
            ) : crumb.onGo === undefined ? (
              <span class="crumb">{crumb.label}</span>
            ) : (
              <button type="button" class="crumb" onClick={() => crumb.onGo?.()}>
                {crumb.label}
              </button>
            )}
          </Fragment>
        ))}
      </nav>
      {state ? <span class="state">{state}</span> : null}
      {blocker ? <span class="blocker">{blocker}</span> : null}
      <div class="acts">{children}</div>
    </header>
  );
}
