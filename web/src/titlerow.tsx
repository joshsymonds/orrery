import { type ComponentChildren, Fragment, type JSX } from "preact";

import { Mark } from "./mark";

// A crumb's label is text, or the title a row's conversation head draws (TitleParts).
export interface Crumb {
  label: ComponentChildren;
  onGo?: () => void;
  place?: boolean;
  // host, on a place crumb, is the host the directory is on; the phone layout names it after the directory.
  host?: string;
}

// InboxMark is the inbox's hold on the mark every title row draws first: the count of what needs you, the hover that
// pops the inbox out, the leave that tucks it away unless the pointer moved into the inbox, and the click that pins it.
export interface InboxMark {
  needs: number;
  enter: () => void;
  leave: (to: EventTarget | null) => void;
  pin: () => void;
}

// crumbClass is the class of a crumb: "crumb", plus "here" for the page and "place" for the directory.
function crumbClass(here: string, place: boolean | undefined): string {
  return ["crumb", here, place ? "place" : ""].filter(Boolean).join(" ");
}

// noInbox is the mark of a title row no inbox answers for: no count, and no action.
const noInbox: InboxMark = { needs: 0, enter: () => {}, leave: () => {}, pin: () => {} };

// TitleRow is the title row every epic page shares: the back button, the breadcrumb, the state, the blocker, and the
// commands the children are. The breadcrumb opens on the Orrery mark, which carries the count and drives the inbox.
// Back and every crumb but the last call their handlers; a crumb with no handler is a plain label, and the last crumb
// is the page itself, a span marked as the current page. A place crumb draws in the place colour. Every text is a child
// text node, so markup in a label shows as text. The strip, when given, takes a line of its own under the row's
// controls. A non-empty note draws under the row as an alert.
export function TitleRow({
  crumbs,
  onBack,
  state,
  blocker,
  mark = noInbox,
  strip,
  note,
  children,
}: {
  crumbs: Crumb[];
  onBack: () => void;
  state?: string;
  blocker?: string;
  mark?: InboxMark;
  strip?: ComponentChildren;
  note?: string;
  children?: ComponentChildren;
}): JSX.Element {
  const last = crumbs.length - 1;
  return (
    <>
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
                <span class={crumbClass("here", crumb.place)} data-host={crumb.host} aria-current="page">
                  {crumb.label}
                </span>
              ) : crumb.onGo === undefined ? (
                <span class={crumbClass("", crumb.place)} data-host={crumb.host}>
                  {crumb.label}
                </span>
              ) : (
                <button type="button" class={crumbClass("", crumb.place)} data-host={crumb.host} onClick={() => crumb.onGo?.()}>
                  {crumb.label}
                </button>
              )}
            </Fragment>
          ))}
        </nav>
        {state ? <span class="state">{state}</span> : null}
        {blocker ? <span class="blocker">{blocker}</span> : null}
        <div class="acts">{children}</div>
        {strip}
      </header>
      {note ? (
        <p class="page-note" role="alert">
          {note}
        </p>
      ) : null}
    </>
  );
}
