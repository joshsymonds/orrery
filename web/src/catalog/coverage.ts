// The story coverage check: every component export of a component file has a story in the file's own stories module. The
// story has the component's name, or the name storyRenames gives it. A component is an export that is a function whose
// name starts with an uppercase letter, so lowercase helpers and constants are not components. storyExemptions names the
// component files that export no component, each with its reason.

// Modules is the shape of the registry's modules: each file keyed by its path relative to web/src, holding its exports.
type Modules = Readonly<Record<string, Readonly<Record<string, unknown>>>>;

// Renames is the shape of storyRenames: each component file, then each of its component exports, mapped to the story
// export that holds the export's stories.
type Renames = Readonly<Record<string, Readonly<Record<string, string>>>>;

// storyExemptions lists the component files that export no component, by their paths, each with the reason it needs no
// stories module.
export const storyExemptions: Readonly<Record<string, string>> = {
  "main.tsx":
    "main.tsx exports only start, which mounts the page, so it exports no component; importing it runs start, so it has no module here.",
};

// storyRenames names each component export whose stories export takes a round-6 name, by its component file and export,
// with the story export that holds its stories. The export is covered by that story export in the file's stories module,
// so the stories need not share the component's name.
export const storyRenames: Renames = {
  "backgroundrow.tsx": { BackgroundRow: "TaskLine" },
  "blocker.tsx": { QuestionCard: "Ask", AskFirst: "ConfirmButton" },
  "card.tsx": { EpicWidget: "WorkCard" },
  "composer.tsx": { Composer: "Dock" },
  "epicline.tsx": { EpicLine: "EpicRow" },
  "eventring.tsx": { EventRing: "EpicEventBlock", StepCard: "StepEndBlock" },
  "evidence.tsx": { EvidenceRuns: "EvidenceRun" },
  "happenings.tsx": { JustHappened: "HappeningList" },
  "notices.tsx": { NoticeBanner: "Notice" },
  "sidepanel.tsx": { SidePanel: "ListPanel" },
  "stepbox.tsx": { StepBox: "Dock" },
  "strip.tsx": { PaneStrip: "MeterStrip" },
  "talkdock.tsx": { TalkDock: "Dock" },
};

// isComponent reports whether an export is a component: a function whose name starts with an uppercase letter.
function isComponent(name: string, value: unknown): boolean {
  return typeof value === "function" && /^[A-Z]/.test(name);
}

// storiesFileOf is the stories module of a component file: the file's path with .stories.tsx in place of .tsx.
function storiesFileOf(file: string): string {
  return `${file.replace(/\.tsx$/, "")}.stories.tsx`;
}

// missingStories returns one line for each component export of componentModules that storyModules holds no story export
// for, in the file's own stories module: "widget.tsx export Widget has no story in widget.stories.tsx". The story export is
// the one renames names for the export, or the export's own name when renames names none. A stories module that does not
// exist holds no export. The lines are sorted.
export function missingStories(
  componentModules: Modules,
  storyModules: Modules,
  renames: Renames = storyRenames,
): string[] {
  const misses: string[] = [];
  for (const [file, exports] of Object.entries(componentModules)) {
    const storiesFile = storiesFileOf(file);
    const stories: Readonly<Record<string, unknown>> = storyModules[storiesFile] ?? {};
    const renamed: Readonly<Record<string, string>> = renames[file] ?? {};
    for (const [name, value] of Object.entries(exports)) {
      const storyExport = Object.hasOwn(renamed, name) ? renamed[name] : name;
      if (isComponent(name, value) && !Object.hasOwn(stories, storyExport)) {
        misses.push(`${file} export ${name} has no story in ${storiesFile}`);
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
    const storiesFile = storiesFileOf(file);
    const exports = componentModules[file];
    for (const [name, storyExport] of Object.entries(entries)) {
      if (exports === undefined) {
        stale.push(`${file} export ${name} renames to ${storyExport}: ${file} is not a component file`);
      } else if (!isComponent(name, exports[name])) {
        stale.push(`${file} export ${name} renames to ${storyExport}: ${file} exports no component ${name}`);
      } else if (!Object.hasOwn(storyModules[storiesFile] ?? {}, storyExport)) {
        stale.push(`${file} export ${name} renames to ${storyExport}: ${storiesFile} exports no ${storyExport}`);
      }
    }
  }
  return stale.sort();
}
