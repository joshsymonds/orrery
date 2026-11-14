import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";

import type { EvidenceArtifact, EvidenceRun } from "./gen/orrery/v1/epics_pb";

// LoadArtifact reads one artifact of an evidence run, or the run's log when the name is empty.
export type LoadArtifact = (run: string, name: string) => Promise<{ data: Uint8Array; mediaType: string }>;

// revisionLength is how many characters of a run's commit id show.
const revisionLength = 12;

// EvidenceRuns is a requirement's evidence runs in order. Each run shows its phase, source, state, verdict, result,
// exit and revision, its log behind a toggle, and its artifacts; a kept image is drawn inline. Every text is a child of
// its element, so no markup in a log, reason or path becomes an element.
export function EvidenceRuns(props: { runs: EvidenceRun[]; loadArtifact: LoadArtifact }): JSX.Element {
  const { runs, loadArtifact } = props;
  return (
    <ul class="runs">
      {runs.map((run) => (
        <EvidenceRunRow key={run.id} run={run} loadArtifact={loadArtifact} />
      ))}
    </ul>
  );
}

// EvidenceRunRow is one run: its head line, its log toggle with the log while it is open, and its artifacts. The exit
// shows once the run has ended, since a run that is still going has none.
function EvidenceRunRow(props: { run: EvidenceRun; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, loadArtifact } = props;
  const [open, setOpen] = useState(false);
  const ended = run.state === "ended";
  return (
    <li class="run">
      <div class="run-head">
        <span class="phase">{run.phase}</span>
        <span class="source">{sourceOf(run.source)}</span>
        <span class="state">{ended ? "ended" : "running"}</span>
        <span class="verdict">{verdictOf(run)}</span>
        <span class={run.passed ? "pass ok" : "pass"}>{run.passed ? "passed" : "not passed"}</span>
        {ended && <span class="exit">{`exit ${run.exit}`}</span>}
        <span class="revision">{run.revision.slice(0, revisionLength)}</span>
      </div>
      <button
        type="button"
        class="log-toggle"
        aria-expanded={open}
        onClick={() => setOpen((isOpen) => !isOpen)}
      >
        log
      </button>
      {open && <RunLog run={run.id} loadArtifact={loadArtifact} />}
      {run.artifacts.length > 0 && (
        <ul class="artifacts">
          {run.artifacts.map((artifact) => (
            <ArtifactRow key={artifact.path} run={run.id} artifact={artifact} loadArtifact={loadArtifact} />
          ))}
        </ul>
      )}
    </li>
  );
}

// RunLog is a run's log while its toggle is open: a loading line until the bytes arrive, then the text preformatted. A
// log that fails to load says so. A load that lands after the log closes sets nothing.
function RunLog(props: { run: string; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, loadArtifact } = props;
  const [text, setText] = useState<string | undefined>(undefined);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let current = true;
    loadArtifact(run, "").then(
      (artifact) => {
        if (current) setText(new TextDecoder().decode(artifact.data));
      },
      () => {
        if (current) setFailed(true);
      },
    );
    return () => {
      current = false;
    };
  }, [run, loadArtifact]);
  if (failed) return <p class="unavailable">log unavailable</p>;
  if (text === undefined) return <p class="pending">loading</p>;
  return <pre class="log">{text}</pre>;
}

// ArtifactRow is one artifact: its path, its state and size, and for a kept image the image drawn under them.
function ArtifactRow(props: { run: string; artifact: EvidenceArtifact; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, artifact, loadArtifact } = props;
  const drawn = artifact.state === "kept" && artifact.mediaType.startsWith("image/");
  return (
    <li class="artifact">
      <span class="path">{artifact.path}</span>
      <span class="artifact-state">{artifactStateOf(artifact.state)}</span>
      <span class="size">{`${artifact.size} bytes`}</span>
      {drawn && <ArtifactImage run={run} path={artifact.path} loadArtifact={loadArtifact} />}
    </li>
  );
}

// ArtifactImage loads a kept image by its path and draws it from an object URL. The URL is revoked when the image
// unmounts or changes; a load that lands after the unmount makes no URL at all.
function ArtifactImage(props: { run: string; path: string; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, path, loadArtifact } = props;
  const [src, setSrc] = useState<string | undefined>(undefined);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let current = true;
    let url: string | undefined;
    loadArtifact(run, path).then(
      (artifact) => {
        if (!current) return;
        // A Blob takes an ArrayBuffer-backed array, so the bytes are copied into one.
        const bytes = new Uint8Array(artifact.data);
        url = URL.createObjectURL(new Blob([bytes], { type: artifact.mediaType }));
        setSrc(url);
      },
      () => {
        if (current) setFailed(true);
      },
    );
    return () => {
      current = false;
      if (url !== undefined) URL.revokeObjectURL(url);
    };
  }, [run, path, loadArtifact]);
  if (failed) return <p class="unavailable">image unavailable</p>;
  if (src === undefined) return <p class="pending">loading</p>;
  return <img class="artifact-image" src={src} alt={path} />;
}

// sourceOf names a run's source: a supplied run reads agent-supplied, and a captured run reads as captured.
function sourceOf(source: string): string {
  return source === "supplied" ? "agent-supplied" : source;
}

// verdictOf is a run's verdict, or no verdict with the judge's reason when the judge gave none.
function verdictOf(run: EvidenceRun): string {
  if (run.verdict !== "") {
    return run.verdict;
  }
  return run.judgeReason === "" ? "no verdict" : `no verdict · ${run.judgeReason}`;
}

// artifactStateOf names an artifact's state: kept, missing, or over cap.
function artifactStateOf(state: string): string {
  return state === "over_cap" ? "over cap" : state;
}
