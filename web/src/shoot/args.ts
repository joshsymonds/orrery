// The arguments of just shoot. A name is component/state, naming one story, or a bare component, naming every state of
// that component. Each --width W is a width to shoot at, and the width is 1600 when none is given. parseShootArgs reads
// the arguments, and resolveNames resolves the names against the names the catalog holds.

// usage is the line a failure to read the arguments carries.
const usage = "usage: just shoot NAME... [--width W]...";

// defaultWidths is the width a run shoots at when no --width is given.
const defaultWidths = [1600];

// ShootArgs is what a run shoots: the names it was asked for, and the widths.
export interface ShootArgs {
  names: string[];
  widths: number[];
}

// ArgsError is the failure to read the arguments, with the message to print.
export interface ArgsError {
  error: string;
}

// parseShootArgs reads the arguments after the catalog directory. It fails when no name is given, when --width has no
// value or a value that is not a positive integer, and on any other option. Each failure's message carries the usage.
export function parseShootArgs(argv: readonly string[]): ShootArgs | ArgsError {
  const names: string[] = [];
  const widths: number[] = [];
  for (let at = 0; at < argv.length; at++) {
    const arg = argv[at];
    if (arg === "--width") {
      at++;
      const value = argv[at];
      const width = value === undefined || !/^\d+$/.test(value) ? Number.NaN : Number(value);
      if (!Number.isSafeInteger(width) || width <= 0) {
        return failure(`--width needs a positive integer, got ${value ?? "nothing"}`);
      }
      widths.push(width);
    } else if (arg.startsWith("-")) {
      return failure(`unknown option ${arg}`);
    } else {
      names.push(arg);
    }
  }
  if (names.length === 0) {
    return failure("name at least one story or component");
  }
  return { names, widths: [...new Set(widths.length > 0 ? widths : defaultWidths)] };
}

// failure is the failure a problem makes, its message ending with the usage.
function failure(problem: string): ArgsError {
  return { error: `${problem}\n${usage}` };
}

// resolveNames resolves each requested name against the names the catalog holds, in the order the catalog lists them. A
// component/state names that story, and a bare component names every story of that component. A name that matches
// nothing fails, naming it. A story named twice is shot once.
export function resolveNames(
  requested: readonly string[],
  available: readonly string[],
): { names: string[] } | { error: string } {
  const names: string[] = [];
  for (const name of requested) {
    const matches = available.filter((story) =>
      name.includes("/") ? story === name : story.startsWith(`${name}/`),
    );
    if (matches.length === 0) {
      return { error: `unknown name ${name}: it is neither a story nor a component in the catalog` };
    }
    for (const match of matches) {
      if (!names.includes(match)) {
        names.push(match);
      }
    }
  }
  return { names };
}
