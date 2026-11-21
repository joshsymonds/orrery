import { type ComponentChildren, Fragment, type JSX } from "preact";

import type { WordTone } from "./recordwords";

// A crumb's label is text, or the title a row's conversation head draws (TitleParts).
export interface Crumb {
  label: ComponentChildren;
  onGo?: () => void;
  place?: boolean;
  // host, on a place crumb, is the host the directory is on; the phone layout names it after the directory.
  host?: string;
}

// crumbClass is the class of a crumb: "crumb", plus "here" for the page and "place" for the directory.
function crumbClass(here: string, place: boolean | undefined): string {
  return ["crumb", here, place ? "place" : ""].filter(Boolean).join(" ");
}

// pillClass is the class of a title row's pill: "state" or "blocker", plus "t-<tone>" when the pill has a tone.
function pillClass(base: string, tone: WordTone | undefined): string {
  return tone ? `${base} t-${tone}` : base;
}

// TitleRow is the title row every page shares, as round 6 draws it: one line holding the back button, the breadcrumb, the
// state, the blocker, and the commands the children are, pushed to its end. The line never wraps: the page's crumb is
// cut short to keep the commands in view, and carries its whole text as its title when it is text. A / stands between
// crumbs. Back and every crumb but the last call their handlers; a crumb with no handler is a plain label, and the last
// crumb is the page itself, a span marked as the current page. A place crumb draws in the place colour. Every text is a
// child text node, so markup in a label shows as text. The strip, when given, takes a line of its own under the first.
// A non-empty note draws under the row as an alert. The state and blocker pills take a word tone each, when given.
export function TitleRow({
  crumbs,
  onBack,
  state,
  stateTone,
  blocker,
  blockerTone,
  strip,
  note,
  children,
}: {
  crumbs: Crumb[];
  onBack: () => void;
  state?: string;
  stateTone?: WordTone;
  blocker?: string;
  blockerTone?: WordTone;
  strip?: ComponentChildren;
  note?: string;
  children?: ComponentChildren;
}): JSX.Element {
  const last = crumbs.length - 1;
  return (
    <>
      <header class="title-row">
        <div class="line1">
          <button type="button" class="back" aria-label="back" onClick={() => onBack()}>
            ‹
          </button>
          <nav class="crumbs" aria-label="where">
            {crumbs.map((crumb, index) => (
              <Fragment key={index}>
                {index > 0 && (
                  <>
                    {" "}
                    <span class="sep">/</span>{" "}
                  </>
                )}
                {index === last ? (
                  <span
                    class={crumbClass("here", crumb.place)}
                    data-host={crumb.host}
                    title={typeof crumb.label === "string" ? crumb.label : undefined}
                    aria-current="page"
                  >
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
          {state ? <span class={pillClass("state", stateTone)}>{state}</span> : null}
          {blocker ? <span class={pillClass("blocker", blockerTone)}>{blocker}</span> : null}
          <div class="acts">{children}</div>
        </div>
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
