package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private sealed interface EpicLoad {
    data object Loading : EpicLoad
    data object Failed : EpicLoad
    data object NotFound : EpicLoad
    data class Loaded(val detail: EpicDetail, val history: List<EpicHistoryRow>) : EpicLoad
}

// The outcome an epic is released with, as written.
private const val RELEASED = "released"

// The notice a command leaves when the epic could not be reached.
private const val UNREACHABLE = "could not reach the epic"

// How long the screen rests on an ended epic before orreryd is told the owner has seen it: the web's dwell (DWELL_MS).
private const val SEEN_DWELL_MILLIS = 1_000L

private const val TAG = "EpicScreen"

/**
 * A section an epic's list opens at, by its name: "now" or "report". Equality is identity, so each open is distinct,
 * and an open of the section the list is already at opens it again.
 */
class EpicOpening(val section: String)

/**
 * One epic as one scrolling list, in the epic page's order: its summary when it has a contract, what is happening now
 * (the problem and the blocker), the red proof, the implementation (the orrery, the now and next rows, what just
 * happened, the workflow's nodes), the green proof, [handover] when it is given, the release and the report. Each
 * section is one item, tagged "epic-section:[name]". A tap on a step calls [onOpenStep] with it, a tap on the summary
 * calls [onOpenContract], and a tap on the orrery, a proof card or a row calls [onOpenPhase]. [actions] render first in
 * the title row. [listState] is the list's state, so the caller keeps its scroll; [openAt] scrolls it to its section
 * when the list is composed. Back and the first crumb call [onBack].
 * A command runs off the main thread, then the epic is read again; a command that did not land shows as a notice. An
 * ended orreryd epic the owner has not seen is marked seen once the screen has rested on it for a second while resumed.
 */
@Composable
fun EpicScreen(
    client: OrreryClient,
    epicId: String,
    onBack: () -> Unit,
    onOpenStep: (StepRow) -> Unit,
    modifier: Modifier = Modifier,
    onOpenContract: () -> Unit = {},
    listState: LazyListState = rememberLazyListState(),
    openAt: EpicOpening? = null,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
    actions: @Composable RowScope.() -> Unit = {},
    handover: (@Composable (EpicDetail) -> Unit)? = null,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var load by remember(client, epicId) { mutableStateOf<EpicLoad>(EpicLoad.Loading) }
    var generation by remember(client, epicId) { mutableIntStateOf(0) }
    var notice by remember(client, epicId) { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    val loadArtifact: ArtifactLoader = remember(client) {
        { run: String, name: String, stderr: Boolean -> client.artifact(run, name, stderr) }
    }

    // send runs one command off the main thread. What did not land shows as the notice, with the typed [answer] beside
    // it when the command is an answer. Then the epic is read again.
    fun send(answer: String = "", call: () -> StepCallResult) {
        scope.launch {
            notice = withContext(Dispatchers.IO) { noticeOf(call, answer) }
            generation++
        }
    }

    LaunchedEffect(client, epicId, generation) {
        // A reload after a command keeps the epic on screen; only a first load or a retry shows the loading line.
        if (load !is EpicLoad.Loaded) load = EpicLoad.Loading
        load = try {
            withContext(Dispatchers.IO) { loadEpic(client, epicId) }
        } catch (error: IOException) {
            EpicLoad.Failed
        }
    }

    val pause = { send { client.pauseEpic(epicId) } }
    val resume = { send { client.resumeEpic(epicId) } }
    val stop = { send { client.stopEpic(epicId) } }
    val retry = { send { client.retryEpic(epicId) } }
    val onAnswer: (String, String) -> String = { questionId, text ->
        send(answer = text) { client.answerEpic(epicId, questionId, text) }
        ""
    }

    val current = load
    val detail = (current as? EpicLoad.Loaded)?.detail
    // The ending this screen marks seen, 0 for none. A reload that still shows the same unseen ending keeps the same key,
    // so it does not start the dwell again.
    val unseenEnding = detail?.epic?.unseenEndingAt() ?: 0L
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(client, epicId, unseenEnding, lifecycle) {
        var dwell: Job? = null
        // Set when the dwell completes, so a resume after it does not send the mark again.
        var marked = false
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> {
                    if (unseenEnding != 0L && !marked) {
                        dwell = scope.launch {
                            delay(SEEN_DWELL_MILLIS)
                            marked = true
                            markSeen(client, epicId)
                        }
                    }
                }
                Lifecycle.Event.ON_PAUSE -> dwell?.cancel()
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            dwell?.cancel()
        }
    }
    val nowAt = now()
    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(
            crumbs = listOfNotNull(Crumb("orrery", onBack), detail?.let { Crumb(it.epic.slug) }),
            onBack = onBack,
            state = detail?.let { stateLineOf(epicStateOf(it), it.epic.phaseAt, nowAt) }.orEmpty(),
            blocker = detail?.let { blockerLineOf(blockerOf(it), nowAt) }.orEmpty(),
            actions = {
                actions()
                if (detail != null && detail.epic.live && detail.epic.outcome.isEmpty()) {
                    if (detail.paused) {
                        TextButton(onClick = resume, modifier = Modifier.testTag("epic-resume")) {
                            Text("Resume")
                        }
                    } else {
                        TextButton(onClick = pause, modifier = Modifier.testTag("epic-pause")) {
                            Text("Pause")
                        }
                    }
                    AskFirst(
                        label = "Stop…",
                        question = "Stop ${detail.epic.slug}?",
                        confirm = "Stop ${detail.epic.slug}",
                        keep = "Keep it running",
                        tag = "epic-stop",
                        onConfirm = stop,
                    )
                }
            },
        )
        when (current) {
            EpicLoad.Loading -> Text("Loading...", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            EpicLoad.Failed -> Column(modifier = Modifier.padding(OrreryTokens.Space.sixteen)) {
                Text("could not load epic", color = OrreryTokens.Role.bad)
                TextButton(onClick = { generation++ }) {
                    Text("Retry")
                }
            }
            EpicLoad.NotFound -> Text("epic not found", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            is EpicLoad.Loaded -> EpicBody(
                loaded = current,
                now = nowAt,
                notice = notice,
                listState = listState,
                openAt = openAt,
                loadArtifact = loadArtifact,
                onOpenStep = onOpenStep,
                onOpenContract = onOpenContract,
                onOpenPhase = onOpenPhase,
                onAnswer = onAnswer,
                onRetry = retry,
                onStop = stop,
                handover = handover,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

// loadEpic reads the epic, then every page of its history, oldest first. Null when no epic has the id.
private fun loadEpic(client: OrreryClient, epicId: String): EpicLoad {
    val detail = client.epic(epicId) ?: return EpicLoad.NotFound
    val history = mutableListOf<EpicHistoryRow>()
    var after = ""
    do {
        val page = client.history(epicId, after)
        history += page.entries
        after = page.next
    } while (after.isNotEmpty())
    return EpicLoad.Loaded(detail, history)
}

// noticeOf runs one command and says what did not land: "" when it landed, else the server's reason or that the epic
// could not be reached. An answer that did not land keeps its text in the notice, so it is not lost with the form.
private fun noticeOf(call: () -> StepCallResult, answer: String): String {
    val reason = try {
        when (val result = call()) {
            StepCallResult.Landed -> return ""
            is StepCallResult.Refused -> result.message
        }
    } catch (error: IOException) {
        UNREACHABLE
    }
    return if (answer.isEmpty()) "not sent: $reason" else "not sent: $reason. Your answer: $answer"
}

// unseenEndingAt is the phase an orreryd epic ended at, when the owner has not seen it since: the ending the dwell marks.
// 0 for a live epic, a record epic, or one seen since it ended.
private fun EpicRow.unseenEndingAt(): Long = if (source == "orreryd" && !live && seen < phaseAt) phaseAt else 0L

// markSeen tells orreryd the owner saw the epic. A mark that does not land is logged: no control on the screen asked for
// it, so nothing shows it.
private suspend fun markSeen(client: OrreryClient, epicId: String) {
    try {
        when (val result = withContext(Dispatchers.IO) { client.seeEpic(epicId) }) {
            StepCallResult.Landed -> Unit
            is StepCallResult.Refused -> Log.w(TAG, "could not mark $epicId seen: ${result.message}")
        }
    } catch (error: IOException) {
        Log.w(TAG, "could not mark $epicId seen", error)
    }
}

// stateLineOf is the epic's state for the title row: its state word, and its time once its phase has begun, never below
// zero when the clock is behind the server's stamp. An empty state stays empty.
internal fun stateLineOf(state: String, phaseAt: Long, now: Long): String =
    if (state.isNotEmpty() && phaseAt > 0L) "$state · ${durationText((now - phaseAt).coerceAtLeast(0L))}" else state

// blockerLineOf is the blocker's line in the title row: its text, and how long it has waited. A park is left out, since
// the now section shows its text and it has no time.
private fun blockerLineOf(blocker: EpicBlocker?, now: Long): String = when {
    blocker == null || blocker is EpicBlocker.Parked -> ""
    else -> blockerTextOf(blocker, now)
}

/** The body of a loaded epic: one list, each section one item. */
@Composable
private fun EpicBody(
    loaded: EpicLoad.Loaded,
    now: Long,
    notice: String,
    listState: LazyListState,
    openAt: EpicOpening?,
    loadArtifact: ArtifactLoader,
    onOpenStep: (StepRow) -> Unit,
    onOpenContract: () -> Unit,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)?,
    onAnswer: (questionId: String, text: String) -> String,
    onRetry: () -> Unit,
    onStop: () -> Unit,
    handover: (@Composable (EpicDetail) -> Unit)?,
    modifier: Modifier,
) {
    val detail = loaded.detail
    val wedges = remember(detail) { wedgesOf(detail) }
    val arcs = remember(detail) { requirementArcs(wedges, detail.requirements) }
    val contract = detail.contract
    LaunchedEffect(openAt) {
        openAt?.let { listState.scrollToItem(sectionIndex(it.section, contract != null, handover != null)) }
    }
    LazyColumn(modifier = modifier.fillMaxWidth(), state = listState) {
        if (contract != null) {
            section("summary") {
                Text(
                    detail.summary.ifEmpty { contract.title },
                    style = OrreryTokens.Type.body,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("epic-summary")
                        .clickable(onClick = onOpenContract)
                        .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                )
            }
        }
        section("now") {
            if (detail.epic.problem.isNotEmpty()) {
                Text(
                    detail.epic.problem,
                    color = OrreryTokens.Role.bad,
                    modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                )
            }
            NowBlock(
                sentence = nowSentence(wedges),
                blocker = blockerOf(detail),
                slug = detail.epic.slug,
                onAnswer = onAnswer,
                onRetry = onRetry,
                onStop = onStop,
            )
            if (notice.isNotEmpty()) {
                Text(
                    notice,
                    color = OrreryTokens.Role.bad,
                    modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen).testTag("epic-notice"),
                )
            }
        }
        section("red") {
            RedProof(
                requirements = detail.requirements,
                loadArtifact = loadArtifact,
                wedges = wedges,
                onOpenPhase = onOpenPhase,
            )
        }
        section("implementation") {
            Orrery(
                wedges = wedges,
                arcs = arcs,
                modifier = Modifier.fillMaxWidth().aspectRatio(1f).padding(horizontal = OrreryTokens.Space.sixteen),
                onOpenPhase = onOpenPhase,
            )
            NowRows(wedges = wedges, now = now, onOpenPhase = onOpenPhase)
            NextRows(wedges = wedges, onOpenPhase = onOpenPhase)
            JustHappened(entries = loaded.history, steps = detail.steps, onOpenStep = onOpenStep)
            if (detail.nodes.isNotEmpty()) {
                SectionHeading("Workflow")
                detail.nodes.forEachIndexed { index, node -> WorkflowNodeRow(index, node, loadArtifact) }
            }
        }
        section("green") {
            GreenProof(
                requirements = detail.requirements,
                loadArtifact = loadArtifact,
                wedges = wedges,
                onOpenPhase = onOpenPhase,
            )
        }
        if (handover != null) {
            section("handover") { handover(detail) }
        }
        section("release") {
            ReleaseProof(contract = contract, releaseProof = detail.releaseProof, released = detail.epic.outcome == RELEASED)
        }
        section("report") {
            Report(report = detail.report)
        }
    }
}

// sectionIndex is the position of the named section among the list's items, in the order EpicBody lays them out: the
// summary comes first when there is a contract, and the handover sits between the green proof and the release.
private fun sectionIndex(name: String, hasSummary: Boolean, hasHandover: Boolean): Int = listOfNotNull(
    "summary".takeIf { hasSummary },
    "now",
    "red",
    "implementation",
    "green",
    "handover".takeIf { hasHandover },
    "release",
    "report",
).indexOf(name)

// section is one item of the list: the section's content in a column tagged "epic-section:[name]", keyed "section:[name]".
private fun LazyListScope.section(name: String, content: @Composable ColumnScope.() -> Unit) {
    item(key = "section:$name") {
        Column(modifier = Modifier.fillMaxWidth().testTag("epic-section:$name"), content = content)
    }
}

@Composable
private fun SectionHeading(title: String) {
    Text(
        title.inLabelCase(),
        style = OrreryTokens.Type.label,
        modifier = Modifier.padding(
            start = OrreryTokens.Space.sixteen,
            top = OrreryTokens.Space.twelve,
            end = OrreryTokens.Space.sixteen,
            bottom = OrreryTokens.Space.four,
        ),
    )
}
