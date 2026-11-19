// ORRERY_PAGE_BUILD is the build scripts/build-web.sh bundles the page with, the build.txt it writes beside it. A bundle
// made any other way, a test's or the catalog's, defines none.
declare const ORRERY_PAGE_BUILD: string | undefined;

// bundledBuild is the build this page was bundled with, or "" when it was bundled with none.
export const bundledBuild: string = typeof ORRERY_PAGE_BUILD === "string" ? ORRERY_PAGE_BUILD : "";

// PageBuildWatch is what the page tells the watch: each build serve reports, and each moment the page may be ready to
// reload, so a reload a stale page waits on happens at the first moment it can.
export interface PageBuildWatch {
  // served takes the build a Host.Status answer named.
  served(build: string): void;
  // check reloads a stale page when it is ready.
  check(): void;
}

// watchPageBuild watches for a page that an earlier serve sent: own is the build the page was bundled with, and a
// served build that is not empty and differs from it makes the page stale. A stale page reloads once, through reload,
// at the first served answer or check that finds it hidden and holding no unsent text, so a reload never takes a page
// the owner is looking at or a draft the owner has not sent. A page with no build of its own, or a serve that names
// none, is never stale. A page stays stale once it is: a later answer naming its build again does not undo it.
export function watchPageBuild(
  own: string,
  hidden: () => boolean,
  unsent: () => boolean,
  reload: () => void,
): PageBuildWatch {
  let stale = false;
  let reloaded = false;
  const check = (): void => {
    if (stale && !reloaded && hidden() && !unsent()) {
      reloaded = true;
      reload();
    }
  };
  return {
    served: (build) => {
      if (own !== "" && build !== "" && build !== own) {
        stale = true;
      }
      check();
    },
    check,
  };
}

// TextBoxes is what holdsUnsentText reads of the page: its text boxes, each with its value.
export interface TextBoxes {
  querySelectorAll(selectors: "textarea"): ArrayLike<{ value: string }>;
}

// holdsUnsentText reports whether any text box under root holds text that is not only white space: a composer's draft,
// a nudge, or an answer's own words, none of which a reload would keep.
export function holdsUnsentText(root: TextBoxes): boolean {
  return Array.from(root.querySelectorAll("textarea")).some((box) => box.value.trim() !== "");
}
