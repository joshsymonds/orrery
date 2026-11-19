// The catalog's page. The story parameter of the address names the one story to draw alone; without it the page draws the
// index. The index is drawn again on each hash change, since the hash names the story it frames.

import { render } from "preact";

import { CatalogIndex, catalogStories, StoryView } from "./catalog";
import { storyModules } from "./registry.gen";

const root = document.getElementById("app");
if (root === null) {
  throw new Error("catalog: index.html has no #app element");
}

const entries = catalogStories(storyModules);

const draw = (): void => {
  const story = new URLSearchParams(location.search).get("story");
  render(
    story === null ? <CatalogIndex entries={entries} hash={location.hash} /> : <StoryView entries={entries} name={story} />,
    root,
  );
};

draw();
window.addEventListener("hashchange", draw);
