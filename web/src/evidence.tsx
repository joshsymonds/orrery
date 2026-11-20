import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";

import type { EvidenceArtifact, EvidenceRun } from "./gen/orrery/v1/epics_pb";

// LoadArtifact reads one artifact of an evidence run, or the run's log when the name is empty: its stderr log when
// stderr is set, else its stdout log.
export type LoadArtifact = (
  run: string,
  name: string,
  stderr: boolean,
) => Promise<{ data: Uint8Array; mediaType: string }>;

// EvidenceRuns is a requirement's evidence runs in order. Each run draws as one line: its sentence, its verdict chip and
// its meta, then a log toggle. Opened, the toggle folds the run's stdout, then its stderr when it has bytes, and its
// artifacts. initiallyOpen draws every run's log open. Every text is a child of its element, so no markup in a log, reason
// or path becomes an element.
export function EvidenceRuns(props: {
  runs: EvidenceRun[];
  loadArtifact: LoadArtifact;
  initiallyOpen?: boolean;
}): JSX.Element {
  const { runs, loadArtifact, initiallyOpen } = props;
  return (
    <ul class="runs">
      {runs.map((run) => (
        <EvidenceRunRow key={run.id} run={run} loadArtifact={loadArtifact} initiallyOpen={initiallyOpen === true} />
      ))}
    </ul>
  );
}

// EvidenceRunRow is one run: its line, and under it while its toggle is open, its logs and artifacts.
function EvidenceRunRow(props: { run: EvidenceRun; loadArtifact: LoadArtifact; initiallyOpen: boolean }): JSX.Element {
  const { run, loadArtifact, initiallyOpen } = props;
  const [open, setOpen] = useState(initiallyOpen);
  return (
    <li class="run">
      <div class="run-line">
        <span class="sentence">{sentenceOf(run)}</span>
        <VerdictChip run={run} />
        <span class="meta">{metaOf(run)}</span>
        <span class="end">
          <button type="button" class="link" aria-expanded={open} onClick={() => setOpen((isOpen) => !isOpen)}>
            {open ? "hide the log" : "log"}
          </button>
        </span>
      </div>
      {open && <RunLog run={run.id} stderr={false} loadArtifact={loadArtifact} />}
      {open && run.stderrBytes > 0n && <RunLog run={run.id} stderr loadArtifact={loadArtifact} />}
      {open &&
        run.artifacts.map((artifact) => (
          <ArtifactRow key={artifact.path} run={run.id} artifact={artifact} loadArtifact={loadArtifact} />
        ))}
    </li>
  );
}

// VerdictChip is a run's verdict: its state while it is going, else its verdict, or no verdict with the judge's reason.
function VerdictChip(props: { run: EvidenceRun }): JSX.Element {
  const { tone, glyph, word } = chipOf(props.run);
  return (
    <span class={tone === "" ? "chip" : `chip ${tone}`}>
      {glyph !== "" && <span aria-hidden="true">{glyph}</span>}
      {word}
    </span>
  );
}

// RunLog is a run's log while its toggle is open, labelled stdout or stderr: a loading line until the bytes arrive, then
// the text. A log that fails to load says so. A load that lands after the log closes sets nothing.
function RunLog(props: { run: string; stderr: boolean; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, stderr, loadArtifact } = props;
  const [text, setText] = useState<string | undefined>(undefined);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let current = true;
    loadArtifact(run, "", stderr).then(
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
  }, [run, stderr, loadArtifact]);
  const body = failed ? "The log is not available." : text === undefined ? "loading the log…" : text;
  return (
    <div class="log side">
      <span class={stderr ? "lbl is-halt" : "lbl"}>{stderr ? "stderr" : "stdout"}</span>
      {body}
    </div>
  );
}

// ArtifactRow is one artifact's line: its path, its state and its size, and for a kept image the image drawn under it.
function ArtifactRow(props: { run: string; artifact: EvidenceArtifact; loadArtifact: LoadArtifact }): JSX.Element {
  const { run, artifact, loadArtifact } = props;
  const drawn = artifact.state === "kept" && artifact.mediaType.startsWith("image/");
  return (
    <div class="artifact">
      <span>{`${artifact.path} · ${artifactStateOf(artifact.state)} · ${artifact.size} bytes`}</span>
      {drawn && <ArtifactImage run={run} path={artifact.path} loadArtifact={loadArtifact} />}
    </div>
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
    loadArtifact(run, path, false).then(
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

// sentenceOf is a run's one sentence: what its capture is doing while it goes, why its output is missing, or whether
// the test failed as it should, or passed on the change, once it has ended.
function sentenceOf(run: EvidenceRun): string {
  const phase = run.phase === "red" ? "Red" : "Green";
  if (run.state !== "ended") {
    return `${phase} run: ${run.state === "judging" ? "a judge is reading the output" : "capturing the output"}…`;
  }
  if (run.notCaptured !== "") {
    return `${phase} run: the output was not captured: ${run.notCaptured}.`;
  }
  if (run.phase === "red") {
    return run.passed
      ? "Red run: the test fails before any change, as it should."
      : "Red run: the test passes before any change. It should fail.";
  }
  return run.passed ? "Green run: the test passes on the change." : "Green run: the test still fails on the change.";
}

// chipOf is a run's verdict chip: its state while it goes, its verdict once it has ended, or no verdict with the judge's
// reason after it. The tone names the chip's class, and the glyph its mark.
function chipOf(run: EvidenceRun): { tone: string; glyph: string; word: string } {
  if (run.state !== "ended") {
    return { tone: "live", glyph: "", word: run.state === "judging" ? "judging" : "capturing" };
  }
  if (run.verdict === "holds") {
    return { tone: "ok", glyph: "✓", word: "holds" };
  }
  if (run.verdict === "fails") {
    return { tone: "bad", glyph: "✗", word: "fails" };
  }
  return {
    tone: "",
    glyph: "",
    word: run.judgeReason === "" ? "no verdict" : `no verdict · ${run.judgeReason}`,
  };
}

// metaOf is a run's meta: its source, and its exit once it has ended.
function metaOf(run: EvidenceRun): string {
  const source = run.source === "supplied" ? "supplied by the agent" : "captured";
  return run.state === "ended" ? `${source} · exit ${run.exit}` : source;
}

// artifactStateOf names an artifact's state: kept, missing, or too large to keep.
function artifactStateOf(state: string): string {
  return state === "over_cap" ? "too large to keep" : state;
}
