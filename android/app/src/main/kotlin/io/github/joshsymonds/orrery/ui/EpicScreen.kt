package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
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

// The reason a command gives when the server did not answer it, in the words the web uses.
private const val UNREACHABLE = "the server did not answer"

// How long the screen rests on an ended epic before orreryd is told the owner has seen it: the web's dwell (DWELL_MS).
private const val SEEN_DWELL_MILLIS = 1_000L

// The phases an orreryd epic may be paused in, and the phases it stops on: web/src/status.ts's controlsOf sets.
private val PAUSABLE_PHASES = setOf("accepted", "built", "reviewing", "correcting")
private val TRACK_PHASES = setOf("accepted", "built", "reviewing", "correcting", "clean", "releasing")

private const val TAG = "EpicScreen"

/**
 * A section an epic's list opens at, by its name: "now", "happened" or "tasks", among the sections the epic reaches.
 * Equality is identity, so each open is distinct, and an open of the section the list is already at opens it again.
 */
class EpicOpening(val section: String)

/**
 * One epic as the record draws it: one scrolling list whose sections are drawn only when the epic reaches them. A
 * problem is one red line at the top. The summary carries the contract and steered doors; the track, Now (its sentence,
 * what asks, and its running steps), what just happened, the tasks (chart then table) and the requirements follow. The
 * title shows the state and the blocker pills, with no commands. A command is in the foot's More sheet, and Stop asks
 * first; the sheet also steers a live orreryd epic from another conversation, and the foot is the ended bar while its
 * steering has ended. A command that did not land shows one line under the title until a command lands. [onTalk] is
 * given the steering session's id when Talk or the steered door is tapped, and [onSteered] each steering the foot
 * settles on. [listState] is the list's state, so the caller keeps its scroll; [openAt] scrolls it to its section when
 * the list is composed. A tap on a task calls [onOpenPhase]. An ended orreryd epic the owner has not seen is marked
 * seen once the screen has rested on it for a second while resumed.
 */
@Composable
fun EpicScreen(
    client: OrreryClient,
    epicId: String,
    onBack: () -> Unit,
    onOpenStep: (StepRow) -> Unit,
    onOpenContract: () -> Unit,
    onTalk: (steering: String) -> Unit,
    listedSteerer: (steering: String) -> Pane?,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
    openAt: EpicOpening? = null,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
    onSteered: (steering: String) -> Unit = {},
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var load by remember(client, epicId) { mutableStateOf<EpicLoad>(EpicLoad.Loading) }
    var generation by remember(client, epicId) { mutableIntStateOf(0) }
    var notice by remember(client, epicId) { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    val loadArtifact: ArtifactLoader = remember(client) {
        { run: String, name: String, stderr: Boolean -> client.artifact(run, name, stderr) }
    }

    // send runs one command off the main thread. What did not land becomes the notice, and a command that lands clears
    // it. Then the epic is read again.
    fun send(verb: String, answer: String = "", call: () -> StepCallResult) {
        scope.launch {
            notice = withContext(Dispatchers.IO) { noticeOf(verb, answer, call) }
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

    val pause = { send("Pause") { client.pauseEpic(epicId) } }
    val resume = { send("Resume") { client.resumeEpic(epicId) } }
    val stop = { send("Stop") { client.stopEpic(epicId) } }
    val retry = { send("Retry") { client.retryEpic(epicId) } }
    val onAnswer: (String, String) -> String = { questionId, text ->
        send("Answer", answer = text) { client.answerEpic(epicId, questionId, text) }
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
    val statePill = detail?.let { statePillOf(it, nowAt) }
    val blockerPill = detail?.let { blockerPillOf(it, nowAt) }
    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(
            crumbs = listOfNotNull(Crumb("orrery", onBack), detail?.let { Crumb(it.epic.slug) }),
            onBack = onBack,
            state = statePill?.word.orEmpty(),
            stateTone = statePill?.tone,
            blocker = blockerPill?.word.orEmpty(),
            blockerTone = blockerPill?.tone,
        )
        if (notice.isNotEmpty()) {
            Text(
                notice,
                color = OrreryTokens.Role.bad,
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen).testTag("epic-notice"),
            )
        }
        when (current) {
            EpicLoad.Loading -> Text(
                "loading the epic…",
                modifier = Modifier.padding(OrreryTokens.Space.sixteen),
            )
            EpicLoad.Failed -> Column(modifier = Modifier.padding(OrreryTokens.Space.sixteen)) {
                Text("The server could not read this epic.", color = OrreryTokens.Role.bad)
                TextButton(onClick = { generation++ }) {
                    Text("Try again")
                }
            }
            EpicLoad.NotFound -> Text("epic not found", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            is EpicLoad.Loaded -> EpicBody(
                loaded = current,
                now = nowAt,
                listState = listState,
                openAt = openAt,
                loadArtifact = loadArtifact,
                onOpenStep = onOpenStep,
                onOpenContract = onOpenContract,
                onOpenPhase = onOpenPhase,
                onTalk = onTalk,
                listedSteerer = listedSteerer,
                actions = NowActions(answer = onAnswer, retry = retry, resume = resume, stop = stop),
                modifier = Modifier.weight(1f),
            )
        }
        detail?.let { loaded ->
            val controls = controlsOf(loaded)
            EpicFoot(
                client = client,
                detail = loaded,
                onOpenSteerer = { onTalk(loaded.epic.steering) },
                onSteered = onSteered,
                canPause = controls.pause,
                canResume = controls.resume,
                canStop = controls.stop,
                onPause = pause,
                onResume = resume,
                onStop = stop,
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

// noticeOf runs one command and says what did not land: "" when it landed, else "<verb> did not land: <reason>.", with the
// typed [answer] after it when the command is an answer, so the text is not lost with the form.
private fun noticeOf(verb: String, answer: String, call: () -> StepCallResult): String {
    val reason = try {
        when (val result = call()) {
            StepCallResult.Landed -> return ""
            is StepCallResult.Refused -> result.message
        }
    } catch (error: IOException) {
        UNREACHABLE
    }
    return if (answer.isEmpty()) "$verb did not land: $reason." else "$verb did not land: $reason. Your answer: $answer"
}

// controlsOf is which commands an epic allows, by the rule web/src/status.ts shares between its page and its card: nothing
// once the epic is not live; an orreryd epic pauses while it is not paused in a pausable phase, resumes whenever it is
// paused, and stops in a track phase; a record epic pauses, resumes and stops only while it has no outcome. An owner's
// park offers no pause, since nothing runs until the owner answers; the park's Stop stays.
private data class Controls(val pause: Boolean, val resume: Boolean, val stop: Boolean)

private fun controlsOf(detail: EpicDetail): Controls {
    val epic = detail.epic
    if (!epic.live) return Controls(pause = false, resume = false, stop = false)
    val pausable = !ownerParked(detail)
    if (epic.source == "orreryd") {
        return Controls(
            pause = pausable && !detail.paused && epic.outcome in PAUSABLE_PHASES,
            resume = detail.paused,
            stop = epic.outcome in TRACK_PHASES,
        )
    }
    val open = epic.outcome.isEmpty()
    return Controls(pause = pausable && open && !detail.paused, resume = open && detail.paused, stop = open)
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

/** The actions the Now section makes: an answer to its question, Retry, Resume now, and Stop once its confirm is given. */
private class NowActions(
    val answer: (questionId: String, text: String) -> String,
    val retry: () -> Unit,
    val resume: () -> Unit,
    val stop: () -> Unit,
)

/** The body of a loaded epic: one list, each section one item, drawn only when the epic reaches it. */
@Composable
private fun EpicBody(
    loaded: EpicLoad.Loaded,
    now: Long,
    listState: LazyListState,
    openAt: EpicOpening?,
    loadArtifact: ArtifactLoader,
    onOpenStep: (StepRow) -> Unit,
    onOpenContract: () -> Unit,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)?,
    onTalk: (steering: String) -> Unit,
    listedSteerer: (steering: String) -> Pane?,
    actions: NowActions,
    modifier: Modifier,
) {
    val detail = loaded.detail
    val reached = reachedOf(detail, loaded.history)
    val tasks = remember(detail) { recordTasksOf(detail) }
    val proven = remember(detail) {
        val wedges = wedgesOf(detail)
        detail.requirements.filter { provenOf(it.id, wedges, detail.requirements) }.map { it.id }.toSet()
    }
    val drawn = listOfNotNull(
        "problem".takeIf { detail.epic.problem.isNotEmpty() },
        "summary".takeIf { reached.summary },
        "track".takeIf { reached.track },
        "now",
        "happened".takeIf { reached.history },
        "tasks".takeIf { reached.tasks },
        "requirements".takeIf { reached.requirements },
    )
    LaunchedEffect(openAt) {
        openAt?.let { opening ->
            drawn.indexOf(opening.section).takeIf { it >= 0 }?.let { listState.scrollToItem(it) }
        }
    }
    LazyColumn(modifier = modifier.fillMaxWidth(), state = listState) {
        if (detail.epic.problem.isNotEmpty()) {
            section("problem") {
                Text(
                    detail.epic.problem,
                    color = OrreryTokens.Role.bad,
                    modifier = Modifier
                        .testTag("epic-problem")
                        .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                )
            }
        }
        if (reached.summary) {
            section("summary") {
                Summary(detail, onOpenContract = onOpenContract, onTalk = onTalk, listedSteerer = listedSteerer)
            }
        }
        if (reached.track) {
            section("track") {
                Column(modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen)) {
                    Track(trackOf(detail))
                }
            }
        }
        section("now") { NowSection(detail = detail, now = now, actions = actions) }
        if (reached.history) {
            section("happened") {
                JustHappened(entries = loaded.history, steps = detail.steps, onOpenStep = onOpenStep)
            }
        }
        if (reached.tasks) {
            section("tasks") {
                Row(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    SectionHeading("Tasks")
                    Text(
                        "${tasks.count { it.integrated }} of ${tasks.size} done",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                    )
                }
                EpicOrrery(tasks = tasks, proven = proven)
                TaskTable(tasks = tasks, now = now, onOpenPhase = onOpenPhase)
            }
        }
        if (reached.requirements) {
            section("requirements") {
                Row(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    SectionHeading("Requirements")
                    Text(
                        "${proven.size} of ${detail.requirements.size} proven",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                    )
                }
                RequirementTable(requirements = detail.requirements, loadArtifact = loadArtifact)
            }
        }
    }
}

// NowSection is Now: its sentence and what asks, then the running steps. Stop… on an owner's park opens a confirm here;
// Stop is sent only once that confirm is given.
@Composable
private fun NowSection(detail: EpicDetail, now: Long, actions: NowActions) {
    var stopAsking by remember { mutableStateOf(false) }
    NowAsk(
        detail = detail,
        onAnswer = actions.answer,
        onRetry = actions.retry,
        onStop = { stopAsking = true },
        onResume = actions.resume,
    )
    if (stopAsking) {
        val slug = detail.epic.slug
        Text("Stop $slug?", modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen).testTag("now-stop-sure"))
        TextButton(
            onClick = {
                stopAsking = false
                actions.stop()
            },
            modifier = Modifier.testTag("now-stop-confirm"),
        ) {
            Text("Stop $slug")
        }
        TextButton(onClick = { stopAsking = false }, modifier = Modifier.testTag("now-stop-keep")) {
            Text("Keep it running")
        }
    }
    StepRows(lines = stepLinesOf(detail), tasks = recordTasksOf(detail), now = now)
}

// Summary is the summary paragraph, then its two doors: the contract, and the conversation that steers the epic. A door
// whose target is absent is not drawn; the Steered from door is drawn once its steerer is read.
@Composable
private fun ColumnScope.Summary(
    detail: EpicDetail,
    onOpenContract: () -> Unit,
    onTalk: (steering: String) -> Unit,
    listedSteerer: (steering: String) -> Pane?,
) {
    Text(
        detail.summary.ifEmpty { detail.contract?.title.orEmpty() },
        style = OrreryTokens.Type.body,
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic-summary")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
    )
    if (detail.contract != null) {
        Door(label = "Contract", line = contractDoorOf(detail), tag = "epic-door-contract", onTap = onOpenContract)
    }
    val steering = detail.epic.steering
    val read = listedReadOf(steering, listedSteerer)
    if (steering.isNotEmpty() && read != null) {
        // A listed steerer is titled from its directory and its label, else its command; one no row lists is
        // the word session.
        val pane = read.pane
        val line = if (pane == null) {
            "session"
        } else {
            listOf(lastPathElement(pane.cwd), pane.label.ifEmpty { pane.command })
                .filter { it.isNotEmpty() }
                .joinToString(" · ")
        }
        Door(label = "Steered from", line = line, tag = "epic-door-steered", onTap = { onTalk(steering) })
    }
}

/** What a steering's read of the listed rows found: the pane or headless session that lists it, or null for none. */
private class ListedRead(val pane: Pane?)

/**
 * The listed rows' read of [steering], made once per steering off the main thread by [listedSteerer]. It is null until
 * that read answers, and a read that fails answers as no row lists the steering.
 */
@Composable
private fun listedReadOf(steering: String, listedSteerer: (steering: String) -> Pane?): ListedRead? {
    var read by remember(steering, listedSteerer) { mutableStateOf<ListedRead?>(null) }
    LaunchedEffect(steering, listedSteerer) {
        if (steering.isEmpty()) return@LaunchedEffect
        read = ListedRead(
            try {
                withContext(Dispatchers.IO) { listedSteerer(steering) }
            } catch (error: IOException) {
                null
            },
        )
    }
    return read
}

// Door is one tappable line of the summary: its label, then what it opens.
@Composable
private fun Door(label: String, line: String, tag: String, onTap: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .clickable(onClick = onTap)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
    ) {
        Text(label, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        Text(line, style = OrreryTokens.Type.small)
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

// section is one item of the list: the section's content in a column tagged "epic-section:[name]", keyed "section:[name]".
private fun LazyListScope.section(name: String, content: @Composable ColumnScope.() -> Unit) {
    item(key = "section:$name") {
        Column(modifier = Modifier.fillMaxWidth().testTag("epic-section:$name"), content = content)
    }
}
