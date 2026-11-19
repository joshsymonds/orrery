// The round-6 names check: every registered story bears a round-6 name, or unnamed.ts lists it. A story bears a round-6 name
// when its name is kebab(Component)/kebab(State) for a pair in round6.json's components, or page/<id> for a scenario id in
// its scenarios. unnamed.ts lists a story by its name, or every story of an export by the export's kebab name, with the
// round-6 component or scenario that draws it and the frontend epic that draws that.

import { kebab } from "./story";

// Round6 is the shape of round6.json that the check reads: each round-6 component with its states, and each scenario id.
export interface Round6 {
  readonly components: Readonly<Record<string, readonly string[]>>;
  readonly scenarios: Readonly<Record<string, unknown>>;
}

// UnnamedEntry gives the round-6 component or scenario that draws an unnamed story, and the frontend epic, F1 to F6, that
// draws it.
export interface UnnamedEntry {
  readonly replacement: string;
  readonly epic: string;
}

// Unnamed is the table of unnamed.ts: each entry keyed by a story's name, or by an export's kebab name.
export type Unnamed = Readonly<Record<string, UnnamedEntry>>;

const unnamedFile = "web/src/catalog/unnamed.ts";
const round6File = "web/src/catalog/round6.json";
const frontendEpic = /^F[1-6]$/;

// roundNames maps each round-6 name to the component and state, or scenario, it names.
function roundNames(round6: Round6): Map<string, string> {
  const names = new Map<string, string>();
  for (const [component, states] of Object.entries(round6.components)) {
    for (const state of states) {
      names.set(`${kebab(component)}/${kebab(state)}`, `${component}/${state}`);
    }
  }
  for (const id of Object.keys(round6.scenarios)) {
    names.set(`page/${id}`, `page/${id}`);
  }
  return names;
}

// storyNameProblems returns one line for each problem with the names: a registered story that bears no round-6 name and is
// listed nowhere; an entry of unnamed that covers no registered story, or a story that already bears a round-6 name; an
// export-level entry whose export is a round-6 component; an entry whose replacement is neither a component nor a scenario;
// and an entry whose epic is not F1 to F6. The lines are sorted.
export function storyNameProblems(registered: readonly string[], round6: Round6, unnamed: Unnamed): string[] {
  const names = roundNames(round6);
  const componentOf = new Map(Object.keys(round6.components).map((component) => [kebab(component), component]));
  const exportOf = (name: string): string => name.slice(0, name.indexOf("/"));
  const problems: string[] = [];

  for (const [key, entry] of Object.entries(unnamed)) {
    const exportLevel = !key.includes("/");
    const covered = exportLevel ? registered.filter((name) => exportOf(name) === key) : registered.filter((name) => name === key);
    if (covered.length === 0) {
      problems.push(`${unnamedFile} entry ${key} covers no registered story`);
    }
    if (exportLevel && componentOf.has(key)) {
      problems.push(
        `${unnamedFile} entry ${key} is an export-level entry, but ${key} is the kebab name of round-6 component ${componentOf.get(key)}: list its stories by name`,
      );
    }
    for (const name of covered) {
      const round = names.get(name);
      if (round !== undefined) {
        problems.push(`${unnamedFile} entry ${key} covers ${name}, which already bears the round-6 name ${round}`);
      }
    }
    if (!Object.hasOwn(round6.components, entry.replacement) && !Object.hasOwn(round6.scenarios, entry.replacement)) {
      problems.push(`${unnamedFile} entry ${key} replaces with ${entry.replacement}, which is neither a component nor a scenario in ${round6File}`);
    }
    if (!frontendEpic.test(entry.epic)) {
      problems.push(`${unnamedFile} entry ${key} names epic ${entry.epic}, which is not F1 to F6`);
    }
  }

  for (const name of registered) {
    if (names.has(name) || Object.hasOwn(unnamed, name) || Object.hasOwn(unnamed, exportOf(name))) {
      continue;
    }
    problems.push(
      `${name} bears no round-6 name in ${round6File}, and ${unnamedFile} does not list it: rerun \`just round6-names\` after editing round 6, or list the story in ${unnamedFile}`,
    );
  }
  return problems.sort();
}
