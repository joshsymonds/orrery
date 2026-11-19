// The catalog's pages. catalogStories reads the registry's modules into the catalog's stories, sorted by name, and throws
// on a module it cannot catalogue. CatalogIndex lists every story and frames the one the hash names at each frame width,
// and StoryView draws one story alone.

import type { VNode } from "preact";

import { type Story, storyName } from "./story";

// stateKey is the shape of a story's state: kebab case, of lowercase letters and digits.
const stateKey = /^[a-z0-9]+(-[a-z0-9]+)*$/;

// frameWidths are the widths CatalogIndex frames a story at: a wide desktop, a narrow desktop and a phone.
export const frameWidths = [1600, 1000, 412];

// Modules is the registry's shape: each *.stories.tsx keyed by its path relative to web/src, holding its exports.
export type Modules = Readonly<Record<string, Readonly<Record<string, unknown>>>>;

// CatalogEntry is one story of the catalog, by its name.
export interface CatalogEntry {
  name: string;
  story: Story;
}

// isStories reports whether value is a Stories export: a record whose every value is a function.
function isStories(value: unknown): value is Record<string, Story> {
  return (
    typeof value === "object" &&
    value !== null &&
    Object.values(value).every((story) => typeof story === "function")
  );
}

// catalogStories lists every story of modules, sorted by name. It throws, naming the file and the export, when an
// export is not a record of functions, when a state key is not kebab case, and when two stories share a name.
export function catalogStories(modules: Modules): CatalogEntry[] {
  const owners = new Map<string, string>();
  const entries: CatalogEntry[] = [];
  for (const [file, exports] of Object.entries(modules)) {
    for (const [exportName, value] of Object.entries(exports)) {
      if (!isStories(value)) {
        throw new Error(`catalog: ${file} export ${exportName} is not a record of functions`);
      }
      for (const [state, story] of Object.entries(value)) {
        if (!stateKey.test(state)) {
          throw new Error(`catalog: ${file} export ${exportName} has the state "${state}", which is not kebab case`);
        }
        const name = storyName(exportName, state);
        const owner = owners.get(name);
        if (owner !== undefined) {
          throw new Error(`catalog: ${file} export ${exportName} shares the name ${name} with ${owner}`);
        }
        owners.set(name, `${file} export ${exportName}`);
        entries.push({ name, story });
      }
    }
  }
  return entries.sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0));
}

// CatalogIndex lists every story as a link to its name. Under the list it frames the story the hash names, which is
// the text after the #, at each of frameWidths: an iframe whose src is the story's own address, so each frame draws the
// story alone.
export function CatalogIndex({ entries, hash }: { entries: readonly CatalogEntry[]; hash: string }): VNode {
  const chosen = entries.find((entry) => entry.name === hash.slice(1));
  return (
    <main class="catalog-index">
      <ul class="catalog-list">
        {entries.map((entry) => (
          <li key={entry.name}>
            <a data-story={entry.name} href={`#${entry.name}`}>
              {entry.name}
            </a>
          </li>
        ))}
      </ul>
      {chosen !== undefined && (
        <div class="catalog-frames">
          {frameWidths.map((width) => (
            <iframe
              key={width}
              class="catalog-frame"
              title={`${chosen.name} at ${width} wide`}
              width={width}
              data-width={width}
              src={`?story=${chosen.name}`}
            />
          ))}
        </div>
      )}
    </main>
  );
}

// StoryView draws the story named name alone, inside #story. A name the catalog does not hold draws a failure element
// that names it.
export function StoryView({ entries, name }: { entries: readonly CatalogEntry[]; name: string }): VNode {
  const entry = entries.find((candidate) => candidate.name === name);
  if (entry === undefined) {
    return (
      <div id="story">
        <p data-story-failure="">{`no story named ${name} in the catalog`}</p>
      </div>
    );
  }
  return <div id="story">{entry.story()}</div>;
}
