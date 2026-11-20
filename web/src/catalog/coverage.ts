// The story coverage check: every component export of a component file has a story in the file's own stories module, or in
// the stories module storyRenames names for it. The story has the component's name, or the name storyRenames gives it. A
// component is an export that is a function whose name starts with an uppercase letter, so lowercase helpers and constants
// are not components. storyExemptions names the component files that export no component, each with its reason.

// Modules is the shape of the registry's modules: each file keyed by its path relative to web/src, holding its exports.
type Modules = Readonly<Record<string, Readonly<Record<string, unknown>>>>;

// Rename is where a component export's stories are: a story export in the file's own stories module, by its name, or a
// stories module and its story export, by both names.
type Rename = string | { module: string; story: string };

// Renames is the shape of storyRenames: each component file, then each of its component exports, mapped to the Rename that
// holds the export's stories.
type Renames = Readonly<Record<string, Readonly<Record<string, Rename>>>>;

// storyExemptions lists the component files that export no component, by their paths, each with the reason it needs no
// stories module.
export const storyExemptions: Readonly<Record<string, string>> = {
  "main.tsx":
    "main.tsx exports only start, which mounts the page, so it exports no component; importing it runs start, so it has no module here.",
};

// storyRenames names each component export whose stories are not in its file's own stories module under its own name, by
// its component file and export: a round-6 name for the story export in the file's stories module, or the stories module
// and story export of another file that draws the export, as { module, story }. A page draws Inbox, NewPanel, KeyList,
// SteerMenu and EndedBar, so their entries name app.stories.tsx's Page.
export const storyRenames: Renames = {
  "ask.tsx": { AskLine: "Ask" },
  "backgroundrow.tsx": { BackgroundRow: "TaskLine" },
  "blocker.tsx": { AskFirst: "ConfirmButton" },
  "card.tsx": { EpicWidget: "WorkCard" },
  "endedbar.tsx": { EndedBar: { module: "app.stories.tsx", story: "Page" } },
  "eventring.tsx": { EventRing: "EpicEventBlock", StepCard: "StepEndBlock" },
  "evidence.tsx": { EvidenceRuns: "EvidenceRun" },
  "glyphs.tsx": { Icon: "Icons" },
  "head.tsx": { Head: "Page" },
  "happenings.tsx": { JustHappened: "HappeningList" },
  "inbox.tsx": { Inbox: { module: "app.stories.tsx", story: "Page" } },
  "helppanel.tsx": { KeyList: { module: "app.stories.tsx", story: "Page" } },
  "notices.tsx": { NoticeBanner: "Notice" },
  "picker.tsx": { NewPanel: { module: "app.stories.tsx", story: "Page" } },
  "sidepanel.tsx": { SidePanel: "ListPanel" },
  "steermenu.tsx": { SteerMenu: { module: "app.stories.tsx", story: "Page" } },
  "strip.tsx": { PaneStrip: "MeterStrip" },
};

// isComponent reports whether an export is a component: a function whose name starts with an uppercase letter.
function isComponent(name: string, value: unknown): boolean {
  return typeof value === "function" && /^[A-Z]/.test(name);
}

// storiesFileOf is the stories module of a component file: the file's path with .stories.tsx in place of .tsx.
function storiesFileOf(file: string): string {
  return `${file.replace(/\.tsx$/, "")}.stories.tsx`;
}

// targetOf is the stories module and story export a Rename names for a component export of file: a plain story export is
// in the file's own stories module, and an object names its module.
function targetOf(file: string, rename: Rename): { module: string; story: string } {
  return typeof rename === "string" ? { module: storiesFileOf(file), story: rename } : rename;
}

// missingStories returns one line for each component export of componentModules that storyModules holds no story export
// for: "widget.tsx export Widget has no story in widget.stories.tsx". The story export and its module are the ones renames
// names for the export, or the export's own name in the file's own stories module when renames names none. A stories
// module that does not exist holds no export. The lines are sorted.
export function missingStories(
  componentModules: Modules,
  storyModules: Modules,
  renames: Renames = storyRenames,
): string[] {
  const misses: string[] = [];
  for (const [file, exports] of Object.entries(componentModules)) {
    const renamed: Readonly<Record<string, Rename>> = renames[file] ?? {};
    for (const [name, value] of Object.entries(exports)) {
      if (!isComponent(name, value)) {
        continue;
      }
      const target = Object.hasOwn(renamed, name)
        ? targetOf(file, renamed[name])
        : { module: storiesFileOf(file), story: name };
      const stories: Readonly<Record<string, unknown>> = storyModules[target.module] ?? {};
      if (!Object.hasOwn(stories, target.story)) {
        misses.push(`${file} export ${name} has no story in ${target.module}`);
      }
    }
  }
  return misses.sort();
}

// staleRenames returns one line for each entry of renames that names a component file componentModules does not hold, a
// component export that file does not export, or a story export its stories module does not hold: "widget.tsx export
// Widget renames to Dock: widget.stories.tsx exports no Dock". The lines are sorted.
export function staleRenames(renames: Renames, componentModules: Modules, storyModules: Modules): string[] {
  const stale: string[] = [];
  for (const [file, entries] of Object.entries(renames)) {
    const exports = componentModules[file];
    for (const [name, rename] of Object.entries(entries)) {
      const target = targetOf(file, rename);
      if (exports === undefined) {
        stale.push(`${file} export ${name} renames to ${target.story}: ${file} is not a component file`);
      } else if (!isComponent(name, exports[name])) {
        stale.push(`${file} export ${name} renames to ${target.story}: ${file} exports no component ${name}`);
      } else if (!Object.hasOwn(storyModules[target.module] ?? {}, target.story)) {
        stale.push(`${file} export ${name} renames to ${target.story}: ${target.module} exports no ${target.story}`);
      }
    }
  }
  return stale.sort();
}
