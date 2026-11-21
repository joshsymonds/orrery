package io.github.joshsymonds.orrery.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.spec
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

// The position the task's own red and green runs draw under. No requirement's index is below 0.
private const val TASK_RUNS = -1

// The kind and sender of the prompt that is the planner's brief in a step's conversation.
private const val BRIEF_KIND = "prompt"
private const val BRIEF_SENDER = "brief"

/**
 * A task's record: its title row, its band, its requirement lines and the planner's brief, its five stages with the one
 * open, and its foot bar. [onOpenEpic] is the epic crumb, the back control and the band's pin; [onOpenTask] is a chip's
 * tap, with the phase the task is in; [onOpenSession] takes the step a stage's session door names. The brief is read
 * through the step's session stream while the page is open; a reset that holds no brief is paged back through the
 * session's older items until one does. [now] is the clock the stages and the foot read.
 */
@Composable
fun PhaseOutputScreen(
    client: OrreryClient,
    epicId: String,
    epic: String,
    task: String,
    onOpenEpic: () -> Unit,
    onOpenTask: (String, Phase) -> Unit,
    onOpenSession: (StepRow) -> Unit,
    modifier: Modifier = Modifier,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    // Null while the epic loads; then the epic's detail (null when there is no such epic), or the failure.
    var load by remember(client, epicId) { mutableStateOf<Result<EpicDetail?>?>(null) }
    var generation by remember(client, epicId) { mutableIntStateOf(0) }
    // Kept per client, so a recomposition does not start the loads again.
    val loadArtifact: ArtifactLoader = remember(client) {
        { run: String, name: String, stderr: Boolean -> client.artifact(run, name, stderr) }
    }
    val hold: (String) -> StepCallResult = remember(client) { { id: String -> client.holdStep(id) } }
    val stop: (String) -> StepCallResult = remember(client) { { id: String -> client.stopStep(id) } }

    LaunchedEffect(client, epicId, generation) {
        load = try {
            Result.success(withContext(Dispatchers.IO) { client.epic(epicId) })
        } catch (error: IOException) {
            Result.failure(error)
        }
    }

    val detail = load?.getOrNull()
    val briefStep = detail?.let { briefStepOf(it, task) }
    var brief by remember(client, task) { mutableStateOf<String?>(null) }
    LaunchedEffect(client, task, briefStep?.id) {
        val session = briefStep?.id ?: return@LaunchedEffect
        try {
            client.watchSession(session, after = 0L) { delta ->
                delta.items.briefText()?.let { brief = it }
                if (delta.reset && brief == null && delta.oldest > 0L) {
                    try {
                        briefBefore(client, session, delta.oldest)?.let { brief = it }
                    } catch (error: IOException) {
                        // A failed older page leaves no brief, and the watch goes on to its later deltas.
                    }
                }
            }
        } catch (error: IOException) {
            // No brief is read: the record draws none.
        }
    }

    if (detail != null) {
        PhaseOutputBody(
            detail = detail,
            task = task,
            brief = brief,
            now = now(),
            onOpenEpic = onOpenEpic,
            onOpenTask = onOpenTask,
            onOpenSession = onOpenSession,
            hold = hold,
            stop = stop,
            onLanded = { generation++ },
            loadArtifact = loadArtifact,
            modifier = modifier,
        )
    } else {
        val current = load
        Column(modifier = modifier.fillMaxSize()) {
            TitleRow(crumbs = listOf(Crumb(epic, onGo = onOpenEpic), Crumb(task)), onBack = onOpenEpic)
            when {
                current == null -> Text("Loading...", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
                current.isFailure -> Column(modifier = Modifier.padding(OrreryTokens.Space.sixteen)) {
                    Text("could not load epic", color = OrreryTokens.Role.bad)
                    TextButton(onClick = { generation++ }) {
                        Text("Retry")
                    }
                }
                else -> Text("epic not found", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            }
        }
    }
}

/** The stage a record has open: [place], or none when it is null. Pressing an open stage's head folds it. */
private class OpenedStage(val place: RingPlace?)

/**
 * A task's record over [detail], with no loading: the tests drive it over a synthetic epic. [hold] and [stop] make a call
 * on the task's running step; a landed call runs [onLanded], a refusal or a call with no answer shows its line under the
 * title row.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun PhaseOutputBody(
    detail: EpicDetail,
    task: String,
    brief: String?,
    now: Long,
    onOpenEpic: () -> Unit,
    onOpenTask: (String, Phase) -> Unit,
    onOpenSession: (StepRow) -> Unit,
    hold: (String) -> StepCallResult,
    stop: (String) -> StepCallResult,
    onLanded: () -> Unit,
    loadArtifact: ArtifactLoader,
    modifier: Modifier = Modifier,
) {
    val record = recordTasksOf(detail).find { it.task == task }
    val bodies = stageBodiesOf(detail, task)
    if (record == null || bodies == null) {
        Column(modifier = modifier.fillMaxSize()) {
            TitleRow(crumbs = listOf(Crumb(detail.epic.slug, onGo = onOpenEpic), Crumb(task)), onBack = onOpenEpic)
            Text("task not found", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
        }
        return
    }
    val steps = detail.steps.filter { it.task == task }
    val running = steps.lastOrNull()?.takeIf { it.end == "" }
    val stages = stagesOf(detail, task, now)
    val defaultOpen = stages.firstOrNull { it.open }?.place
    var opened by remember(task) { mutableStateOf<OpenedStage?>(null) }
    val chosen = opened
    val openPlace = if (chosen != null) chosen.place else defaultOpen
    val animated = animationsOn()
    val scope = rememberCoroutineScope()
    var outcome by remember(task) { mutableStateOf<String?>(null) }
    var asking by remember(task) { mutableStateOf(false) }
    fun call(name: String, rpc: () -> StepCallResult) {
        scope.launch {
            outcome = try {
                when (val result = withContext(Dispatchers.IO) { rpc() }) {
                    StepCallResult.Landed -> {
                        onLanded()
                        null
                    }
                    is StepCallResult.Refused -> "$name was refused: ${sentenceOf(result.message)}"
                }
            } catch (error: IOException) {
                "$name may have landed: no answer came."
            }
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(
            crumbs = listOf(Crumb(detail.epic.slug, onGo = onOpenEpic), Crumb(record.title)),
            onBack = onOpenEpic,
        )
        outcome?.let {
            Text(
                it,
                modifier = Modifier.testTag("task-outcome").padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
            )
        }
        Column(modifier = Modifier.fillMaxWidth().weight(1f).verticalScroll(rememberScrollState())) {
            ContextBand(detail = detail, currentTask = task, now = now, onOpenEpic = onOpenEpic, onOpenTask = onOpenTask)
            record.requirements.forEach { id ->
                RequirementLine(id = id, text = detail.requirements.find { it.id == id }?.text)
            }
            brief?.let { BriefBlock(it) }
            stages.forEach { stage ->
                StageRowView(
                    stage = stage,
                    open = stage.place == openPlace,
                    animated = animated,
                    onPress = { opened = OpenedStage(if (openPlace == stage.place) null else stage.place) },
                ) {
                    StageBody(
                        place = stage.place,
                        bodies = bodies,
                        detail = detail,
                        task = task,
                        steps = steps,
                        loadArtifact = loadArtifact,
                        onOpenSession = onOpenSession,
                    )
                }
            }
        }
        FootBar(
            record = record,
            now = now,
            running = running,
            onInterrupt = { id -> call("Interrupt") { hold(id) } },
            onMore = { asking = true },
        )
    }
    if (asking && running != null) {
        ModalBottomSheet(onDismissRequest = { asking = false }) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text("Stop ${record.title}?", style = OrreryTokens.Type.small, modifier = Modifier.testTag("task-stop-title"))
                Text(
                    "The task ends short and the planner decides what to do next.",
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                )
                TextButton(
                    onClick = {
                        asking = false
                        call("Stop") { stop(running.id) }
                    },
                    modifier = Modifier.fillMaxWidth().testTag("task-stop-confirm"),
                ) {
                    Text("Stop ${record.title}")
                }
                TextButton(
                    onClick = { asking = false },
                    modifier = Modifier.fillMaxWidth().testTag("task-stop-keep"),
                ) {
                    Text("Keep it running")
                }
            }
        }
    }
}

// The step whose conversation holds the planner's brief: the task's first implementer step, else its first step that is
// not read-only, in started order.
private fun briefStepOf(detail: EpicDetail, task: String): StepRow? {
    val steps = detail.steps.filter { it.task == task }.sortedBy { it.started }
    return steps.firstOrNull { it.role == "implementer" } ?: steps.firstOrNull { !it.readOnly }
}

// The planner's brief among items, or null when none of them is one.
private fun List<ConversationItem>.briefText(): String? =
    firstOrNull { it.kind == BRIEF_KIND && it.sender == BRIEF_SENDER }?.text

// The planner's brief among the pages of session's items older than before, one page at a time, newest first, until a
// page holds it or no older item is left. Null when no page holds it.
private suspend fun briefBefore(client: OrreryClient, session: String, before: Long): String? {
    var cursor = before
    while (cursor > 0L) {
        val page = withContext(Dispatchers.IO) { client.sessionOlder(session, cursor) }
        page.items.briefText()?.let { return it }
        if (!page.more) return null
        cursor = page.oldest
    }
    return null
}

// A refusal's reason as a sentence: its final full stop added unless it ends in one.
private fun sentenceOf(reason: String): String = if (reason.endsWith(".")) reason else "$reason."

// A requirement's line: its id as a chip, then its text when the epic lists the requirement.
@Composable
private fun RequirementLine(id: String, text: String?) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("task-requirement:$id")
            .semantics(mergeDescendants = true) {}
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Chip(id, ChipTone.Neutral)
        if (!text.isNullOrEmpty()) Text(text, style = OrreryTokens.Type.body, modifier = Modifier.weight(1f))
    }
}

// The planner's brief under its label.
@Composable
private fun BriefBlock(brief: String) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("task-brief")
            .semantics(mergeDescendants = true) {}
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text("The planner’s brief", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        Text(brief, style = OrreryTokens.Type.body)
    }
}

/**
 * One stage row: its head, the stage's name, summary and time, which opens the body when a passed or now row is pressed.
 * An ahead row has no click. The now row's tone is its state description. [body] shows under the head while open.
 */
@Composable
private fun StageRowView(
    stage: StageRow,
    open: Boolean,
    animated: Boolean,
    onPress: () -> Unit,
    body: @Composable () -> Unit,
) {
    val name = stage.place.word
    val tone = stage.tone
    Column(modifier = Modifier.fillMaxWidth().testTag("stage:$name")) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .testTag("stage-head:$name")
                .then(if (stage.state != SliceState.AHEAD) Modifier.clickable(role = Role.Button, onClick = onPress) else Modifier)
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val nameModifier = if (tone != null) {
                Modifier.testTag("stage-tone:$name").semantics { stateDescription = tone.name.lowercase() }
            } else {
                Modifier
            }
            Text(
                name,
                modifier = nameModifier,
                style = OrreryTokens.Type.body,
                color = tone?.let(::wedgeColourOf) ?: OrreryTokens.Ink.one,
            )
            Text(stage.summary, modifier = Modifier.weight(1f).testTag("stage-sum:$name"), style = OrreryTokens.Type.small)
            if (stage.time.isNotEmpty()) {
                Text(stage.time, modifier = Modifier.testTag("stage-time:$name"), style = OrreryTokens.Type.small)
            }
        }
        val content: @Composable () -> Unit = {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("stage-body:$name")
                    .padding(
                        start = OrreryTokens.Space.sixteen,
                        end = OrreryTokens.Space.sixteen,
                        bottom = OrreryTokens.Space.eight,
                    ),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                body()
            }
        }
        // With animations off the body is there as soon as the row is open: a snap still waits for a frame to finish.
        if (animated) {
            AnimatedVisibility(
                visible = open,
                enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
                exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
            ) {
                content()
            }
        } else if (open) {
            content()
        }
    }
}

// The body of a stage, drawn from the task's stage bodies.
@Composable
private fun StageBody(
    place: RingPlace,
    bodies: StageBodies,
    detail: EpicDetail,
    task: String,
    steps: List<StepRow>,
    loadArtifact: ArtifactLoader,
    onOpenSession: (StepRow) -> Unit,
) {
    when (place) {
        RingPlace.RED -> RunStage(bodies.red, loadArtifact)
        RingPlace.IMPLEMENT -> ImplementStage(bodies.implement, steps, loadArtifact, onOpenSession)
        RingPlace.GREEN -> GreenStage(bodies.green, loadArtifact)
        RingPlace.REVIEW -> ReviewStage(
            body = bodies.review,
            reviews = detail.buildTasks.find { it.id == task }?.reviews.orEmpty(),
            steps = steps,
            onOpenSession = onOpenSession,
        )
        RingPlace.INTEGRATE -> ToneText(bodies.integrate.line)
    }
}

// The red stage: its run, or the quiet line that none has run.
@Composable
private fun RunStage(run: RunSection, loadArtifact: ArtifactLoader) {
    val evidence = run.run
    if (evidence == null) {
        ToneText(run.none)
    } else {
        EvidenceRuns(requirement = TASK_RUNS, runs = listOf(evidence), loadArtifact = loadArtifact)
    }
}

// The implement stage: each attempt with its chip, then the latest attempt's check rounds, its gate's tails, its judge, and
// the session door of the latest step.
@Composable
private fun ImplementStage(
    body: ImplementBody,
    steps: List<StepRow>,
    loadArtifact: ArtifactLoader,
    onOpenSession: (StepRow) -> Unit,
) {
    SubLabel("Attempts")
    body.attempts.forEach { attempt ->
        Row(
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("${attempt.number}", style = OrreryTokens.Type.small)
            Text(attempt.words, style = OrreryTokens.Type.body, modifier = Modifier.weight(1f))
            Chip(attempt.chip.text, chipToneOf(attempt.chip.tone))
        }
    }
    val latest = body.latest
    if (latest != null) {
        val checks = steps.find { it.id == body.session }?.checks.orEmpty()
        SubLabel(latest.label)
        latest.rounds.forEach { round ->
            CheckBlock(round.round, round.text.joinToString("\n"), checks = roundChecks(checks, round.round))
        }
        latest.gate?.let { gate ->
            SubLabel(gate.label)
            if (gate.lines.isNotEmpty()) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("gate-tail")
                        .background(OrreryTokens.Surface.raised)
                        .padding(OrreryTokens.Space.eight),
                ) {
                    gate.lines.forEach { Text(it, style = OrreryTokens.Type.small) }
                }
            }
        }
        SubLabel("Judge")
        ToneText(latest.judge)
    }
    body.session?.let { id -> steps.find { it.id == id }?.let { SessionDoor(it, onOpenSession) } }
}

// The green stage: its reproduction on the change, then its run, or the quiet line that none has run.
@Composable
private fun GreenStage(body: GreenBody, loadArtifact: ArtifactLoader) {
    SubLabel("Reproduction")
    ToneText(body.reproduction)
    SubLabel("Green run")
    RunStage(body.run, loadArtifact)
}

// The review stage: a row per review gate and per finding, then the session door of the latest read-only step.
@Composable
private fun ReviewStage(
    body: ReviewBody,
    reviews: List<TaskReviewRow>,
    steps: List<StepRow>,
    onOpenSession: (StepRow) -> Unit,
) {
    if (body.rows.isEmpty()) ToneText(body.none)
    // Each row is a review with no findings, or one finding, in the order the rows are drawn.
    val tags = reviews.flatMap { review ->
        if (review.findings.isEmpty()) listOf("review-row:${review.gate}") else review.findings.map { "finding:${it.id}" }
    }
    body.rows.zip(tags).forEach { (row, tag) -> ReviewRowView(row, tag) }
    body.session?.let { id -> steps.find { it.id == id }?.let { SessionDoor(it, onOpenSession) } }
}

// One review row: its gate cell, what it is, where, its result chip and the reason after it.
@Composable
private fun ReviewRowView(row: ReviewRow, tag: String) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .semantics(mergeDescendants = true) {},
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        if (row.gate.isNotEmpty()) SubLabel(row.gate)
        Text(row.what, style = OrreryTokens.Type.body)
        if (row.where.isNotEmpty()) Text(row.where, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        ) {
            Chip(row.result.text, chipToneOf(row.result.tone))
            if (row.reason.isNotEmpty()) Text(row.reason, style = OrreryTokens.Type.small)
        }
    }
}

// The door that opens a step's session.
@Composable
private fun SessionDoor(step: StepRow, onOpenSession: (StepRow) -> Unit) {
    TextButton(onClick = { onOpenSession(step) }, modifier = Modifier.testTag("open-session")) {
        Text("Open the session ›")
    }
}

// A stage body's label, in the secondary ink.
@Composable
private fun SubLabel(text: String) {
    Text(text, style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three, modifier = Modifier.testTag("stage-sub"))
}

// A line of words in its tone.
@Composable
private fun ToneText(line: ToneLine) {
    Text(line.text, style = OrreryTokens.Type.body, color = bodyColourOf(line.tone))
}

// The foot bar: the task's state pill, then, while a step runs, Interrupt when it is not held and More, which asks
// before the step is stopped.
@Composable
private fun FootBar(
    record: RecordTask,
    now: Long,
    running: StepRow?,
    onInterrupt: (String) -> Unit,
    onMore: () -> Unit,
) {
    val pill = if (running != null && record.place != null) {
        "${record.place.word} · ${elapsedClock(now - record.since)}"
    } else {
        record.words
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            pill,
            modifier = Modifier.weight(1f).testTag("task-state"),
            style = OrreryTokens.Type.small,
            color = wedgeColourOf(record.tone),
        )
        if (running != null) {
            if (!running.held) {
                TextButton(onClick = { onInterrupt(running.id) }, modifier = Modifier.testTag("task-interrupt")) {
                    Text("Interrupt")
                }
            }
            TextButton(
                onClick = onMore,
                modifier = Modifier.testTag("task-more").semantics { contentDescription = "Stop this step…" },
            ) {
                Text("More")
            }
        }
    }
}

// The colour of a task's tone: run live, mine peach, limited and plan grey, bad red, acc teal, ok green.
private fun wedgeColourOf(tone: WedgeTone): Color = when (tone) {
    WedgeTone.PLAN, WedgeTone.LIMITED -> OrreryTokens.Ink.three
    WedgeTone.RUN -> OrreryTokens.Tone.run
    WedgeTone.MINE -> OrreryTokens.Tone.mine
    WedgeTone.BAD -> OrreryTokens.Role.bad
    WedgeTone.ACC -> OrreryTokens.Tone.acc
    WedgeTone.OK -> OrreryTokens.Role.ok
}

private fun bodyColourOf(tone: BodyTone): Color = when (tone) {
    BodyTone.OK -> OrreryTokens.Role.ok
    BodyTone.BAD -> OrreryTokens.Role.bad
    BodyTone.ATTENTION -> OrreryTokens.Role.attention
    BodyTone.NEUTRAL -> OrreryTokens.Ink.two
    BodyTone.LIVE -> OrreryTokens.Role.live
    BodyTone.QUIET -> OrreryTokens.Ink.three
    BodyTone.ACC -> OrreryTokens.Tone.acc
}

// A body tone's chip: an accepted line is ok, since no chip draws accepted.
private fun chipToneOf(tone: BodyTone): ChipTone = when (tone) {
    BodyTone.OK, BodyTone.ACC -> ChipTone.Ok
    BodyTone.BAD -> ChipTone.Bad
    BodyTone.ATTENTION -> ChipTone.Attention
    BodyTone.NEUTRAL, BodyTone.QUIET -> ChipTone.Neutral
    BodyTone.LIVE -> ChipTone.Live
}
