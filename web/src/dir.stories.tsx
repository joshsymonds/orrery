// The stories of Dir: a project's card in the four states round 6 draws it, on the fixture projects. The conversations under
// each head are their titles, and the projects are what buildTree makes of the fixture rows.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import { Dir as DirView } from "./dir";
import { shippedSlices } from "./fixtures/slices";
import { rowsFrom } from "./rows";
import { buildTree, type Convo, type Project } from "./tree";

// projects are the fixture projects, as the inbox tree places the fixture rows on the serving host.
const projects = buildTree({
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
  view: "inbox",
});

// projectNamed is the fixture project with name, which the fixtures must hold.
function projectNamed(name: string): Project {
  const project = projects.find((candidate) => candidate.name === name);
  if (project === undefined) {
    throw new Error(`the fixtures hold no project ${name}`);
  }
  return project;
}

// titles draws each conversation of convos as its row's title.
function titles(convos: readonly Convo[]): VNode[] {
  return convos.map((convo) => <p key={convo.key}>{convo.row?.label ?? ""}</p>);
}

const noop = () => {};

export const Dir: Stories = {
  folded: () => {
    const project = projectNamed("lighthouse");
    return (
      <DirView project={project} folded={true} waiting={0} endedOpen={false} onFold={noop} onEnded={noop} ended={titles(project.ended)}>
        {titles(project.convos)}
      </DirView>
    );
  },
  "folded-waiting": () => {
    const project = projectNamed("tidepool");
    return (
      <DirView project={project} folded={true} waiting={3} endedOpen={false} onFold={noop} onEnded={noop} ended={titles(project.ended)}>
        {titles(project.convos)}
      </DirView>
    );
  },
  open: () => {
    const project = projectNamed("lighthouse");
    return (
      <DirView project={project} folded={false} waiting={0} endedOpen={false} onFold={noop} onEnded={noop} ended={titles(project.ended)}>
        {titles(project.convos)}
      </DirView>
    );
  },
  "other-host": () => {
    const project = projectNamed("kelp");
    return (
      <DirView project={project} folded={true} waiting={0} endedOpen={false} onFold={noop} onEnded={noop} ended={titles(project.ended)}>
        {titles(project.convos)}
      </DirView>
    );
  },
};
