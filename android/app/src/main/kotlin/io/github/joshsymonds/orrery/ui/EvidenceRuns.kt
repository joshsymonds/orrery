package io.github.joshsymonds.orrery.ui

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EvidenceArtifactFile
import io.github.joshsymonds.orrery.api.EvidenceArtifactRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

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

/**
 * The evidence runs of requirement [requirement], oldest first. A run shows its phase, "agent-supplied" when an agent
 * supplied it, its verdict or "no verdict" with the judge's reason, whether it passed, its exit, the first 12
 * characters of its revision, and "running" while it has not ended. Its stdout log loads when asked for, and its stderr
 * log too when it has one. Each artifact it kept is named: a kept image is drawn, and a missing or over-cap artifact
 * says so.
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
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("evidence-run:$position")
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight), verticalAlignment = Alignment.CenterVertically) {
            Text(run.phase.inLabelCase(), style = OrreryTokens.Type.label)
            if (run.source == "supplied") {
                Text("agent-supplied", style = OrreryTokens.Type.small)
            }
            if (run.state != "ended") {
                Text("running", style = OrreryTokens.Type.small)
            }
        }
        Text(run.verdict.ifEmpty { "no verdict" }, style = OrreryTokens.Type.body)
        if (run.judgeReason.isNotEmpty()) {
            Text(run.judgeReason, style = OrreryTokens.Type.body)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(if (run.passed) "passed" else "not passed", style = OrreryTokens.Type.small)
            Text("exit ${run.exit}", style = OrreryTokens.Type.small)
            Text("revision ${run.revision.take(12)}", style = OrreryTokens.Type.small)
        }
        RunLog(position = position, runId = run.id, stderr = false, loadArtifact = loadArtifact)
        if (run.stderrBytes > 0) {
            RunLog(position = position, runId = run.id, stderr = true, loadArtifact = loadArtifact)
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

/** The toggle of one log of one run, stdout or stderr, and the log once it has loaded under the toggle. */
@Composable
private fun RunLog(position: String, runId: String, stderr: Boolean, loadArtifact: ArtifactLoader) {
    val stream = if (stderr) "stderr" else "stdout"
    var log by remember { mutableStateOf<Fetch<String>?>(null) }
    val scope = rememberCoroutineScope()
    Column(modifier = Modifier.fillMaxWidth().testTag("evidence-stream:$position:$stream")) {
        TextButton(
            onClick = {
                log = Fetch.Loading
                scope.launch { log = fetchLog(runId, stderr, loadArtifact) }
            },
            modifier = Modifier.testTag("evidence-log:$position:$stream"),
        ) {
            Text(stream)
        }
        when (val current = log) {
            null -> Unit
            Fetch.Loading -> Text("loading log", style = OrreryTokens.Type.small)
            Fetch.Missing -> Text("log missing", style = OrreryTokens.Type.small)
            is Fetch.Failed -> Text(current.message, color = OrreryTokens.Role.bad, style = OrreryTokens.Type.small)
            is Fetch.Loaded -> Text(
                current.value,
                style = OrreryTokens.Type.small,
            )
        }
    }
}

@Composable
private fun ArtifactView(
    position: String,
    artifactIndex: Int,
    runId: String,
    artifact: EvidenceArtifactRow,
    loadArtifact: ArtifactLoader,
) {
    Text(artifact.path, style = OrreryTokens.Type.small)
    when {
        artifact.state == "missing" -> Text("missing", style = OrreryTokens.Type.small)
        artifact.state == "over_cap" -> Text("over cap", style = OrreryTokens.Type.small)
        artifact.state == "kept" && artifact.mediaType.startsWith("image/") -> ArtifactImage(
            runId = runId,
            path = artifact.path,
            tag = "evidence-image:$position:$artifactIndex",
            loadArtifact = loadArtifact,
        )
    }
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

private suspend fun fetchLog(runId: String, stderr: Boolean, loadArtifact: ArtifactLoader): Fetch<String> =
    try {
        val file = withContext(Dispatchers.IO) { loadArtifact(runId, "", stderr) }
        if (file == null) Fetch.Missing else Fetch.Loaded(String(file.data, Charsets.UTF_8))
    } catch (error: IOException) {
        Fetch.Failed("could not load log")
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
