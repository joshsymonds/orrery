// The names of round 6 as the catalogue and the scenario registry state them. A catalogue cell is named
// Component/State. A component that the catalogue registers again, as the touch section does for Button and Ask, is one
// component with the union of its states. A scenario is its id with the frames that draw it, desk then phone.
// round6Names builds the names, and serializeNames writes them as the committed round6.json.

// Frame is one of the two clients a scenario can be drawn for.
export type Frame = "desk" | "phone";

// Scenario is one scenario of the registry: its id, and whether it has a drawing for the desk and one for the phone.
export interface Scenario {
  id: string;
  desk: boolean;
  phone: boolean;
}

// Round6Names is what round6.json holds: each component with its sorted distinct states, and each scenario id with the
// frames that draw it.
export interface Round6Names {
  components: Record<string, string[]>;
  scenarios: Record<string, Frame[]>;
}

// byKey builds an object from a map, with its keys in code-unit order.
function byKey<T>(entries: Map<string, T>): Record<string, T> {
  return Object.fromEntries([...entries].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
}

// round6Names builds the names of round 6 from the catalogue's cell names and the scenarios. A cell name that is not
// Component/State, with a non-empty part on each side of the first slash, fails, naming it.
export function round6Names(cells: readonly string[], scenarios: readonly Scenario[]): Round6Names {
  const states = new Map<string, Set<string>>();
  for (const cell of cells) {
    const at = cell.indexOf("/");
    if (at <= 0 || at === cell.length - 1) {
      throw new Error(`cell name ${JSON.stringify(cell)} is not Component/State`);
    }
    const component = cell.slice(0, at);
    const have = states.get(component) ?? new Set<string>();
    have.add(cell.slice(at + 1));
    states.set(component, have);
  }

  const components = new Map<string, string[]>();
  for (const [component, have] of states) {
    components.set(component, [...have].sort());
  }

  const frames = new Map<string, Frame[]>();
  for (const scenario of scenarios) {
    const drawn: Frame[] = [];
    if (scenario.desk) drawn.push("desk");
    if (scenario.phone) drawn.push("phone");
    frames.set(scenario.id, drawn);
  }

  return { components: byKey(components), scenarios: byKey(frames) };
}

// serializeNames writes the names as JSON with a 2-space indent and a trailing newline.
export function serializeNames(names: Round6Names): string {
  return `${JSON.stringify(names, null, 2)}\n`;
}
