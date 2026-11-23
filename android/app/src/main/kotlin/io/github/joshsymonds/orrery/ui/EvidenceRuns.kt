package io.github.joshsymonds.orrery.ui

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EvidenceArtifactFile
import io.github.joshsymonds.orrery.api.EvidenceArtifactRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

private const val LOG_NOT_AVAILABLE = "The log is not available."

/** What a run reads while it waits for the gatherer, and while the gatherer gathers its evidence. */
internal const val WAITING_FOR_GATHERER = "waiting for the gatherer"
internal const val BEING_GATHERED = "being gathered"

/**
 * Fetches an artifact of a run by its path, or one of the run's logs when the name is empty: its stderr log when
 * [stderr] is set, its stdout log otherwise. Null when the server has none.
 */
internal typealias ArtifactLoader = (run: String, name: String, stderr: Boolean) -> EvidenceArtifactFile?

/** One fetch from the phone's evidence: what it fetched, or why there is nothing to show. */
private sealed interface Fetch<out T> {
    data object Loading : Fetch<Nothing>
    data class Loaded<out T>(val value: T) : Fetch<T>
    data object Missing : Fetch<Nothing>
    data class Failed(val message: String) : Fetch<Nothing>
}

/** A run's log as text: its stdout, and its stderr when the run has stderr bytes. */
private data class LogText(val stdout: String, val stderr: String?)

/**
 * The evidence runs of requirement [requirement], oldest first. Each run draws as one line: its sentence, its verdict
 * chip and its meta. Under it, a `log` toggle opens the run's log, stdout then stderr when it has bytes, and its
 * artifacts, which fold with the log. A kept image is drawn in that fold.
 */
@Composable
fun EvidenceRuns(
    requirement: Int,
    runs: List<EvidenceRunRow>,
    loadArtifact: ArtifactLoader,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        runs.forEachIndexed { index, run ->
            EvidenceRunView(position = "$requirement:$index", run = run, loadArtifact = loadArtifact)
        }
    }
}

@Composable
private fun EvidenceRunView(position: String, run: EvidenceRunRow, loadArtifact: ArtifactLoader) {
    val (verdict, tone) = verdictOf(run)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("evidence-run:$position")
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        ) {
            Text(sentenceOf(run), style = OrreryTokens.Type.body)
            Chip(verdict, tone)
            Text(metaOf(run), style = OrreryTokens.Type.small)
        }
        RunLogFold(position = position, run = run, loadArtifact = loadArtifact)
    }
}

/**
 * The toggle of a run's log: `log` opens the fold and `hide the log` closes it. Open, the fold holds the stdout log, then
 * the stderr log when the run has stderr bytes, then the run's artifacts.
 */
@Composable
private fun RunLogFold(position: String, run: EvidenceRunRow, loadArtifact: ArtifactLoader) {
    var open by remember { mutableStateOf(false) }
    var log by remember { mutableStateOf<Fetch<LogText>>(Fetch.Loading) }
    val hasStderr = run.stderrBytes > 0
    LaunchedEffect(open, run.id, hasStderr) {
        if (open) {
            log = Fetch.Loading
            log = fetchLog(run.id, hasStderr, loadArtifact)
        }
    }
    Column(modifier = Modifier.fillMaxWidth()) {
        TextButton(
            onClick = { open = !open },
            modifier = Modifier.testTag("evidence-log:$position"),
        ) {
            Text(if (open) "hide the log" else "log")
        }
        if (open) {
            when (val current = log) {
                Fetch.Loading -> Text("loading the log…", style = OrreryTokens.Type.small)
                is Fetch.Loaded -> {
                    Column(modifier = Modifier.fillMaxWidth().testTag("evidence-stream:$position:stdout")) {
                        Text(current.value.stdout, style = OrreryTokens.Type.small)
                    }
                    current.value.stderr?.let { stderr ->
                        Column(modifier = Modifier.fillMaxWidth().testTag("evidence-stream:$position:stderr")) {
                            Text(stderr, style = OrreryTokens.Type.small)
                        }
                    }
                }
                Fetch.Missing, is Fetch.Failed -> Text(LOG_NOT_AVAILABLE, style = OrreryTokens.Type.small)
            }
            run.artifacts.forEachIndexed { artifactIndex, artifact ->
                ArtifactView(
                    position = position,
                    artifactIndex = artifactIndex,
                    runId = run.id,
                    artifact = artifact,
                    loadArtifact = loadArtifact,
                )
            }
        }
    }
}

/**
 * The one sentence a run draws, from its state, its capture and its result: the phase is "Red" or "Green", and the result
 * is whether the run passed its phase.
 */
private fun sentenceOf(run: EvidenceRunRow): String {
    val phase = run.phase.replaceFirstChar { it.uppercase() }
    return when {
        run.state == "waiting" -> "$phase run: $WAITING_FOR_GATHERER…"
        run.state == "gathering" -> "$phase run: $BEING_GATHERED…"
        run.state == "capturing" -> "$phase run: capturing the output…"
        run.state == "judging" -> "$phase run: a judge is reading the output…"
        run.notCaptured.isNotEmpty() -> "$phase run: the output was not captured: ${run.notCaptured}."
        run.phase == "red" && run.passed -> "Red run: the test fails before any change, as it should."
        run.phase == "red" -> "Red run: the test passes before any change. It should fail."
        run.passed -> "Green run: the test passes on the change."
        else -> "Green run: the test still fails on the change."
    }
}

/**
 * The verdict chip of a run and its tone: its state while it waits for the gatherer, is gathered, captures or is
 * judged, else its verdict, else no verdict with the judge's reason when the judge gave one.
 */
private fun verdictOf(run: EvidenceRunRow): Pair<String, ChipTone> = when (run.state) {
    "waiting" -> WAITING_FOR_GATHERER to ChipTone.Live
    "gathering" -> BEING_GATHERED to ChipTone.Live
    "capturing" -> "capturing" to ChipTone.Live
    "judging" -> "judging" to ChipTone.Live
    else -> when (run.verdict) {
        "holds" -> "✓ holds" to ChipTone.Ok
        "fails" -> "✗ fails" to ChipTone.Bad
        else -> (if (run.judgeReason.isEmpty()) "no verdict" else "no verdict · ${run.judgeReason}") to ChipTone.Neutral
    }
}

/** The meta of a run: its source, and its exit once it has ended. */
private fun metaOf(run: EvidenceRunRow): String {
    val source = if (run.source == "supplied") "supplied by the agent" else "captured"
    return if (run.state == "ended") "$source · exit ${run.exit}" else source
}

/** An artifact's line under the log: its path, its state and its size. */
@Composable
private fun ArtifactView(
    position: String,
    artifactIndex: Int,
    runId: String,
    artifact: EvidenceArtifactRow,
    loadArtifact: ArtifactLoader,
) {
    Text("${artifact.path} · ${stateWordOf(artifact.state)} · ${artifact.size} bytes", style = OrreryTokens.Type.small)
    if (artifact.state == "kept" && artifact.mediaType.startsWith("image/")) {
        ArtifactImage(
            runId = runId,
            path = artifact.path,
            tag = "evidence-image:$position:$artifactIndex",
            loadArtifact = loadArtifact,
        )
    }
}

/** An artifact's state as its line names it: kept and missing as they are, an over-cap artifact as too large to keep. */
private fun stateWordOf(state: String): String = when (state) {
    "over_cap" -> "too large to keep"
    else -> state
}

@Composable
private fun ArtifactImage(runId: String, path: String, tag: String, loadArtifact: ArtifactLoader) {
    val image by produceState<Fetch<ImageBitmap>>(Fetch.Loading, runId, path, loadArtifact) {
        value = fetchImage(runId, path, loadArtifact)
    }
    when (val current = image) {
        Fetch.Loading -> Text("loading image", style = OrreryTokens.Type.small)
        Fetch.Missing -> Text("missing", style = OrreryTokens.Type.small)
        is Fetch.Failed -> Text(current.message, color = OrreryTokens.Role.bad, style = OrreryTokens.Type.small)
        is Fetch.Loaded -> Image(
            bitmap = current.value,
            contentDescription = path,
            modifier = Modifier.fillMaxWidth().testTag(tag),
        )
    }
}

private suspend fun fetchLog(runId: String, hasStderr: Boolean, loadArtifact: ArtifactLoader): Fetch<LogText> =
    try {
        withContext(Dispatchers.IO) {
            val stdout = loadArtifact(runId, "", false) ?: return@withContext Fetch.Missing
            val stderr = if (hasStderr) (loadArtifact(runId, "", true) ?: return@withContext Fetch.Missing) else null
            Fetch.Loaded(
                LogText(
                    stdout = String(stdout.data, Charsets.UTF_8),
                    stderr = stderr?.let { String(it.data, Charsets.UTF_8) },
                ),
            )
        }
    } catch (error: IOException) {
        Fetch.Failed(LOG_NOT_AVAILABLE)
    }

private suspend fun fetchImage(runId: String, path: String, loadArtifact: ArtifactLoader): Fetch<ImageBitmap> =
    try {
        withContext(Dispatchers.IO) {
            val file = loadArtifact(runId, path, false) ?: return@withContext Fetch.Missing
            val bitmap = BitmapFactory.decodeByteArray(file.data, 0, file.data.size)
                ?: return@withContext Fetch.Failed("could not show image")
            Fetch.Loaded(bitmap.asImageBitmap())
        }
    } catch (error: IOException) {
        Fetch.Failed("could not load image")
    }
