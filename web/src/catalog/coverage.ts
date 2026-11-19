// The story coverage check: every component export of a component file has a story of the same name in the file's own
// stories module. A component is an export that is a function whose name starts with an uppercase letter, so lowercase
// helpers and constants are not components. storyExemptions names the component files that export no component, each
// with its reason.

// Modules is the shape of the registry's modules: each file keyed by its path relative to web/src, holding its exports.
type Modules = Readonly<Record<string, Readonly<Record<string, unknown>>>>;

// storyExemptions lists the component files that export no component, by their paths, each with the reason it needs no
// stories module.
export const storyExemptions: Readonly<Record<string, string>> = {
  "main.tsx":
    "main.tsx exports only start, which mounts the page, so it exports no component; importing it runs start, so it has no module here.",
};

// isComponent reports whether an export is a component: a function whose name starts with an uppercase letter.
function isComponent(name: string, value: unknown): boolean {
  return typeof value === "function" && /^[A-Z]/.test(name);
}

// missingStories returns one line for each component export of componentModules that storyModules holds no export of the
// same name for, in the file's own stories module: "widget.tsx export Widget has no story in widget.stories.tsx". A
// stories module that does not exist holds no export. The lines are sorted.
export function missingStories(componentModules: Modules, storyModules: Modules): string[] {
  const misses: string[] = [];
  for (const [file, exports] of Object.entries(componentModules)) {
    const storiesFile = `${file.replace(/\.tsx$/, "")}.stories.tsx`;
    const stories: Readonly<Record<string, unknown>> = storyModules[storiesFile] ?? {};
    for (const [name, value] of Object.entries(exports)) {
      if (isComponent(name, value) && !Object.hasOwn(stories, name)) {
        misses.push(`${file} export ${name} has no story in ${storiesFile}`);
      }
    }
  }
  return misses.sort();
}
