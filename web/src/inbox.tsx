import type { ComponentChildren, JSX, Ref, VNode } from "preact";
import { Fragment } from "preact";

import { Dir } from "./dir";
import { FilterChip } from "./filterchip";
import { Icon } from "./glyphs";
import { Mark } from "./mark";
import type { Convo, Project, View } from "./tree";
import { type Counts, type Narrow } from "./treefilter";

// InboxProps are the Inbox's inputs: the view and the load state, the projects already narrowed and searched, each
// project's waiting number over the whole tree by its key, the counts for the chips, and the presses and the state they
// change. search is null while the search field is closed.
export interface InboxProps {
  view: View;
  state: "loading" | "error" | "ready";
  projects: Project[];
  waitingOf: ReadonlyMap<string, number>;
  counts: Counts;
  narrow: Narrow;
  onNarrow: (by: Narrow) => void;
  search: string | null;
  onSearch: (text: string | null) => void;
  searchRef?: Ref<HTMLInputElement>;
  folded: ReadonlySet<string>;
  onFold: (key: string) => void;
  endedOpen: ReadonlySet<string>;
  onEnded: (key: string) => void;
  renderConvo: (convo: Convo) => VNode;
  archivedCount: number;
  onView: (view: View) => void;
  onRetry?: () => void;
  onNew: () => void;
  onKeys?: () => void;
  // onOpenProject is the press on a project's head, which opens the project's page.
  onOpenProject?: (key: string) => void;
  // pin is the foot's control that pins the inbox beside the page or unpins it. The foot draws it only when given.
  pin?: { pinned: boolean; onPin: () => void };
  notes?: ComponentChildren;
}

// Inbox draws the web's inbox column as round 6 draws it: the head with the mark, the name, search and New; under it the
// two chips, each only when its count is not zero, the search field while search is open, and the body. The body is
// loading, the error, "Nothing is running." when nothing is listed, "Nothing matches" when a search finds no project, or
// each project as a card. Archived draws its title and count in place of the chips. The foot leads to Archived, or back.
export function Inbox({
  view,
  state,
  projects,
  waitingOf,
  counts,
  narrow,
  onNarrow,
  search,
  onSearch,
  searchRef,
  folded,
  onFold,
  endedOpen,
  onEnded,
  renderConvo,
  archivedCount,
  onView,
  onRetry,
  onNew,
  onKeys,
  onOpenProject,
  pin,
  notes,
}: InboxProps): JSX.Element {
  // The chips count the drawn tree, so they draw only while it is drawn: not while loading or after a failed read, as on
  // Android's home.
  const showChips = view === "inbox" && state === "ready" && (counts.waiting > 0 || counts.fresh > 0);
  return (
    <nav class="inbox" aria-label="inbox">
      <header class="inbox-head">
        <Mark />
        <span class="name">orrery</span>
        <span class="end">
          <button
            type="button"
            class="btn quiet icon-only"
            aria-label="Search sessions and epics"
            onClick={() => onSearch("")}
          >
            <Icon name="search" />
          </button>
          <button type="button" class="btn" title="Start a session, or find one  N" onClick={() => onNew()}>
            <Icon name="plus" />
            New
          </button>
        </span>
      </header>
      {notes}
      {showChips && (
        <div class="inbox-attn">
          {counts.waiting > 0 && (
            <FilterChip
              count={counts.waiting}
              label={counts.waiting === 1 ? "waits on you" : "wait on you"}
              tone="attention"
              on={narrow === "waiting"}
              onPress={() => onNarrow(narrow === "waiting" ? "" : "waiting")}
            />
          )}
          {counts.fresh > 0 && (
            <FilterChip
              count={counts.fresh}
              label="new"
              tone="new"
              on={narrow === "new"}
              onPress={() => onNarrow(narrow === "new" ? "" : "new")}
            />
          )}
        </div>
      )}
      {view === "archived" && (
        <div class="inbox-attn">
          <div class="sec">
            <span class="name">Archived</span>
            <span class="n">{archivedCount}</span>
          </div>
        </div>
      )}
      {search !== null && (
        <div class="inbox-search">
          <label class="field focused">
            <Icon name="search" />
            <input
              ref={searchRef}
              value={search}
              placeholder="Search sessions and epics"
              aria-label="Search sessions and epics"
              onInput={(event) => onSearch(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  onSearch(null);
                }
              }}
            />
            <kbd>esc</kbd>
          </label>
        </div>
      )}
      <div class="inbox-rows" data-list={view}>
        <Body
          view={view}
          state={state}
          projects={projects}
          waitingOf={waitingOf}
          narrow={narrow}
          search={search}
          folded={folded}
          onFold={onFold}
          endedOpen={endedOpen}
          onEnded={onEnded}
          renderConvo={renderConvo}
          onRetry={onRetry}
          onNew={onNew}
          onOpenProject={onOpenProject}
        />
      </div>
      <footer class="inbox-foot">
        {view === "archived" ? (
          <button type="button" class="btn quiet" onClick={() => onView("inbox")}>
            <Icon name="back" />
            Back to the inbox
          </button>
        ) : (
          <button type="button" class="btn quiet" onClick={() => onView("archived")}>
            <Icon name="archive" />
            Archived
            <kbd>v</kbd>
          </button>
        )}
        {onKeys !== undefined && (
          <button type="button" class="btn quiet icon-only" aria-label="Keys" title="Keys  ?" onClick={() => onKeys()}>
            <Icon name="keys" />
          </button>
        )}
        {pin !== undefined && (
          <button
            type="button"
            class="btn quiet icon-only"
            aria-label={pin.pinned ? "Unpin the inbox" : "Pin the inbox beside the page"}
            title={pin.pinned ? "Unpin the inbox  B" : "Pin the inbox  B"}
            onClick={() => pin.onPin()}
          >
            <Icon name="sidebar" />
          </button>
        )}
      </footer>
    </nav>
  );
}

// BodyProps are what the inbox's body draws: the load state, the projects and their whole-tree waiting numbers, the
// narrowing and search that chose them, and the presses the cards and states make.
interface BodyProps {
  view: View;
  state: InboxProps["state"];
  projects: Project[];
  waitingOf: ReadonlyMap<string, number>;
  narrow: Narrow;
  search: string | null;
  folded: ReadonlySet<string>;
  onFold: (key: string) => void;
  endedOpen: ReadonlySet<string>;
  onEnded: (key: string) => void;
  renderConvo: (convo: Convo) => VNode;
  onRetry?: () => void;
  onNew: () => void;
  onOpenProject?: (key: string) => void;
}

// Body draws the inbox's rows: loading, the error with Try now, the nothing-running state with New, the nothing-matches
// state with the words searched, or each project as a card. With no projects, no search and no narrowing it is the
// nothing-running state. Narrowed with no projects and no search words, it draws nothing.
function Body({
  view,
  state,
  projects,
  waitingOf,
  narrow,
  search,
  folded,
  onFold,
  endedOpen,
  onEnded,
  renderConvo,
  onRetry,
  onNew,
  onOpenProject,
}: BodyProps): JSX.Element | null {
  if (state === "loading") {
    return (
      <div class="blank">
        <span>loading…</span>
      </div>
    );
  }
  if (state === "error") {
    return (
      <div class="blank">
        <p class="inote error">
          <Icon name="warn" />
          <span>The server did not answer.</span>
        </p>
        <span>Trying again every few seconds.</span>
        {onRetry !== undefined && (
          <button type="button" class="btn" onClick={() => onRetry()}>
            <Icon name="retry" />
            Try now
          </button>
        )}
      </div>
    );
  }
  if (projects.length === 0) {
    const words = search ?? "";
    if (words !== "") {
      return (
        <div class="blank">
          <span class="t-body">Nothing matches “{words}”.</span>
          <span>Search looks at titles, directories, hosts, states, and the names of epics and contracts.</span>
        </div>
      );
    }
    if (narrow === "" && view === "inbox") {
      return (
        <div class="blank">
          <span class="t-body">Nothing is running.</span>
          <span>Start a session in a directory and it shows here, grouped by that directory.</span>
          <button type="button" class="btn primary" onClick={() => onNew()}>
            <Icon name="plus" />
            New
          </button>
        </div>
      );
    }
    return null;
  }
  return (
    <>
      {projects.map((project) => (
        <Dir
          key={project.key}
          project={project}
          folded={folded.has(project.key)}
          waiting={waitingOf.get(project.key) ?? 0}
          endedOpen={endedOpen.has(project.key)}
          onFold={() => onFold(project.key)}
          onEnded={() => onEnded(project.key)}
          onOpen={onOpenProject}
          ended={project.ended.map((convo) => <Fragment key={convo.key}>{renderConvo(convo)}</Fragment>)}
        >
          {project.convos.map((convo) => (
            <Fragment key={convo.key}>{renderConvo(convo)}</Fragment>
          ))}
        </Dir>
      ))}
    </>
  );
}
