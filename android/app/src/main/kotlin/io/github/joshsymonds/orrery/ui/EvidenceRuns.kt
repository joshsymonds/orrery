package io.github.joshsymonds.orrery.ui

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EvidenceArtifactFile
import io.github.joshsymonds.orrery.api.EvidenceArtifactRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** Fetches an artifact of a run by its path, or the run's log when the name is empty. Null when the server has none. */
internal typealias ArtifactLoader = (run: String, name: String) -> EvidenceArtifactFile?

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
 * characters of its revision, and "running" while it has not ended. Its log loads when asked for. Each artifact it kept
 * is named: a kept image is drawn, and a missing or over-cap artifact says so.
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
            .padding(vertical = 4.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(run.phase, style = MaterialTheme.typography.labelLarge)
            if (run.source == "supplied") {
                Text("agent-supplied", style = MaterialTheme.typography.labelSmall)
            }
            if (run.state != "ended") {
                Text("running", style = MaterialTheme.typography.labelSmall)
            }
        }
        Text(run.verdict.ifEmpty { "no verdict" }, style = MaterialTheme.typography.bodyMedium)
        if (run.judgeReason.isNotEmpty()) {
            Text(run.judgeReason, style = MaterialTheme.typography.bodySmall)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(if (run.passed) "passed" else "not passed", style = MaterialTheme.typography.labelSmall)
            Text("exit ${run.exit}", style = MaterialTheme.typography.labelSmall)
            Text("revision ${run.revision.take(12)}", style = MaterialTheme.typography.labelSmall)
        }
        RunLog(position = position, runId = run.id, loadArtifact = loadArtifact)
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

/** The log button of one run, and the log once it has loaded. */
@Composable
private fun RunLog(position: String, runId: String, loadArtifact: ArtifactLoader) {
    var log by remember { mutableStateOf<Fetch<String>?>(null) }
    val scope = rememberCoroutineScope()
    TextButton(
        onClick = {
            log = Fetch.Loading
            scope.launch { log = fetchLog(runId, loadArtifact) }
        },
        modifier = Modifier.testTag("evidence-log:$position"),
    ) {
        Text("log")
    }
    when (val current = log) {
        null -> Unit
        Fetch.Loading -> Text("loading log", style = MaterialTheme.typography.labelSmall)
        Fetch.Missing -> Text("log missing", style = MaterialTheme.typography.labelSmall)
        is Fetch.Failed -> Text(current.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.labelSmall)
        is Fetch.Loaded -> Text(
            current.value,
            fontFamily = FontFamily.Monospace,
            style = MaterialTheme.typography.bodySmall,
        )
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
    Text(artifact.path, style = MaterialTheme.typography.labelSmall)
    when {
        artifact.state == "missing" -> Text("missing", style = MaterialTheme.typography.labelSmall)
        artifact.state == "over_cap" -> Text("over cap", style = MaterialTheme.typography.labelSmall)
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
        Fetch.Loading -> Text("loading image", style = MaterialTheme.typography.labelSmall)
        Fetch.Missing -> Text("missing", style = MaterialTheme.typography.labelSmall)
        is Fetch.Failed -> Text(current.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.labelSmall)
        is Fetch.Loaded -> Image(
            bitmap = current.value,
            contentDescription = path,
            modifier = Modifier.fillMaxWidth().testTag(tag),
        )
    }
}

private suspend fun fetchLog(runId: String, loadArtifact: ArtifactLoader): Fetch<String> =
    try {
        val file = withContext(Dispatchers.IO) { loadArtifact(runId, "") }
        if (file == null) Fetch.Missing else Fetch.Loaded(String(file.data, Charsets.UTF_8))
    } catch (error: IOException) {
        Fetch.Failed("could not load log")
    }

private suspend fun fetchImage(runId: String, path: String, loadArtifact: ArtifactLoader): Fetch<ImageBitmap> =
    try {
        withContext(Dispatchers.IO) {
            val file = loadArtifact(runId, path) ?: return@withContext Fetch.Missing
            val bitmap = BitmapFactory.decodeByteArray(file.data, 0, file.data.size)
                ?: return@withContext Fetch.Failed("could not show image")
            Fetch.Loaded(bitmap.asImageBitmap())
        }
    } catch (error: IOException) {
        Fetch.Failed("could not load image")
    }
