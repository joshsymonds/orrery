import { type ComponentChildren, Fragment, type JSX } from "preact";

export interface Crumb {
  label: string;
  onGo?: () => void;
}

// TitleRow is the title row every epic page shares: the back button, the breadcrumb, the state, the blocker, and the
// commands the children are. Back and every crumb but the last call their handlers; the last crumb is the page itself,
// a span marked as the current page. Every text is a child text node, so markup in a label shows as text.
export function TitleRow({
  crumbs,
  onBack,
  state,
  blocker,
  children,
}: {
  crumbs: Crumb[];
  onBack: () => void;
  state?: string;
  blocker?: string;
  children?: ComponentChildren;
}): JSX.Element {
  const last = crumbs.length - 1;
  return (
    <header class="title-row">
      <button type="button" class="back" aria-label="back" onClick={() => onBack()}>
        ‹
      </button>
      <nav class="crumbs" aria-label="where">
        {crumbs.map((crumb, index) => (
          <Fragment key={index}>
            {index > 0 && (
              <>
                {" "}
                <span class="sep">›</span>{" "}
              </>
            )}
            {index === last ? (
              <span class="crumb here" aria-current="page">
                {crumb.label}
              </span>
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
