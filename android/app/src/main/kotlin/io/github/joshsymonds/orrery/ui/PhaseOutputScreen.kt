package io.github.joshsymonds.orrery.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.spec
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

// The position the task's own red and green runs draw under. No requirement's index is below 0.
private const val TASK_RUNS = -1

/**
 * A task's phase output: its title row, its title, and its four phases, the phase [phase] open. Tapping another row
 * calls [onOpenPhase] with the task and that phase. Hold and Stop sit in the title row while a step runs; Stop asks
 * first. [onBack] is the back control and the epic crumb, [onGoHome] the orrery crumb, and [onOpenSession] takes the
 * step a row's session names.
 */
@Composable
fun PhaseOutputScreen(
    client: OrreryClient,
    epicId: String,
    epic: String,
    task: String,
    phase: Phase,
    onBack: () -> Unit,
    onGoHome: () -> Unit,
    onOpenPhase: (String, Phase) -> Unit,
    onOpenSession: (StepRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    // Null while the epic loads; then the epic's detail (null when there is no such epic), or the failure.
    var load by remember { mutableStateOf<Result<EpicDetail?>?>(null) }
    var generation by remember { mutableIntStateOf(0) }
    // Kept per client, so a recomposition does not start the loads again.
    val loadArtifact: ArtifactLoader = remember(client) {
        { run: String, name: String, stderr: Boolean -> client.artifact(run, name, stderr) }
    }
    val hold: (String) -> StepCallResult = remember(client) { { id: String -> client.holdStep(id) } }
    val stop: (String) -> StepCallResult = remember(client) { { id: String -> client.stopStep(id) } }

    LaunchedEffect(client, epicId, generation) {
        load = null
        load = try {
            Result.success(withContext(Dispatchers.IO) { client.epic(epicId) })
        } catch (error: IOException) {
            Result.failure(error)
        }
    }

    val current = load
    val detail = current?.getOrNull()
    if (detail != null) {
        PhaseOutputBody(
            detail = detail,
            epic = epic,
            task = task,
            phase = phase,
            onBack = onBack,
            onGoHome = onGoHome,
            onOpenPhase = onOpenPhase,
            onOpenSession = onOpenSession,
            hold = hold,
            stop = stop,
            onLanded = { generation++ },
            loadArtifact = loadArtifact,
            modifier = modifier,
        )
    } else {
        Column(modifier = modifier.fillMaxSize()) {
            TitleRow(crumbs = crumbsOf(epic, task, phase, onGoHome, onBack), onBack = onBack)
            when {
                current == null -> Text("Loading...", modifier = Modifier.padding(16.dp))
                current.isFailure -> Column(modifier = Modifier.padding(16.dp)) {
                    Text("could not load epic", color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = { generation++ }) {
                        Text("Retry")
                    }
                }
                else -> Text("epic not found", modifier = Modifier.padding(16.dp))
            }
        }
    }
}

/**
 * The phase output screen over [detail], with no loading: the tests drive it over a synthetic epic. [hold] and [stop]
 * make a call on a step's id off the main thread; a landed call runs [onLanded], a refusal shows its reason, and a call
 * that fails in transport shows that it may have landed.
 */
@Composable
internal fun PhaseOutputBody(
    detail: EpicDetail,
    epic: String,
    task: String,
    phase: Phase,
    onBack: () -> Unit,
    onGoHome: () -> Unit,
    onOpenPhase: (String, Phase) -> Unit,
    onOpenSession: (StepRow) -> Unit,
    hold: (String) -> StepCallResult,
    stop: (String) -> StepCallResult,
    onLanded: () -> Unit,
    loadArtifact: ArtifactLoader,
    modifier: Modifier = Modifier,
) {
    val output = phaseOutputOf(detail, task)
    val animated = animationsOn()
    val scope = rememberCoroutineScope()
    var outcome by remember { mutableStateOf<String?>(null) }
    fun call(name: String, rpc: () -> StepCallResult) {
        scope.launch {
            outcome = try {
                when (val result = withContext(Dispatchers.IO) { rpc() }) {
                    StepCallResult.Landed -> {
                        onLanded()
                        null
                    }
                    is StepCallResult.Refused -> "$name refused: ${result.message}"
                }
            } catch (error: IOException) {
                "$name not known: it may have landed"
            }
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(
            crumbs = crumbsOf(epic, task, phase, onGoHome, onBack),
            onBack = onBack,
            actions = {
                output?.running?.let { running ->
                    if (!running.held) {
                        TextButton(
                            onClick = { call("hold") { hold(running.id) } },
                            modifier = Modifier.testTag("phase-hold"),
                        ) {
                            Text("Hold")
                        }
                    }
                    AskFirst(
                        label = "Stop…",
                        question = "Stop this step?",
                        confirm = "Confirm stop",
                        keep = "Keep it",
                        tag = "phase-stop",
                        onConfirm = { call("stop") { stop(running.id) } },
                    )
                }
            },
        )
        outcome?.let { Text(it, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        if (output == null) {
            Text("task not found", modifier = Modifier.padding(16.dp))
        } else {
            Column(modifier = Modifier.fillMaxWidth().weight(1f).verticalScroll(rememberScrollState())) {
                Text(output.title, style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(16.dp))
                output.rows.forEach { row ->
                    PhaseRowView(
                        row = row,
                        session = detail.steps.find { it.id == row.session },
                        opened = row.phase == phase && row.state != SliceState.AHEAD,
                        animated = animated,
                        onOpen = { onOpenPhase(task, row.phase) },
                        onOpenSession = onOpenSession,
                    ) {
                        PhaseBody(output = output, phase = row.phase, loadArtifact = loadArtifact)
                    }
                }
            }
        }
    }
}

// The crumbs of the screen: orrery, the epic, then the task and its phase, which is the page itself.
private fun crumbsOf(epic: String, task: String, phase: Phase, onGoHome: () -> Unit, onBack: () -> Unit): List<Crumb> =
    listOf(Crumb("orrery", onGo = onGoHome), Crumb(epic, onGo = onBack), Crumb("$task · ${phase.word}"))

/**
 * One phase row: its word, its summary and, when it names a session, the session's button. A row that is not ahead
 * opens on tap, unless it is open already; an open row shows [body], opening at Move.LAYOUT and snapping when
 * [animated] is false.
 */
@Composable
private fun PhaseRowView(
    row: PhaseRow,
    session: StepRow?,
    opened: Boolean,
    animated: Boolean,
    onOpen: () -> Unit,
    onOpenSession: (StepRow) -> Unit,
    body: @Composable () -> Unit,
) {
    val canOpen = row.state != SliceState.AHEAD && !opened
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .testTag("phase-row:${row.phase.word}")
                .fillMaxWidth()
                .then(if (canOpen) Modifier.clickable(onClick = onOpen) else Modifier)
                .padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(row.phase.word, style = MaterialTheme.typography.titleSmall)
            Text(row.summary, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
            if (session != null) {
                TextButton(onClick = { onOpenSession(session) }, modifier = Modifier.testTag("open-session")) {
                    Text("Open the session ›")
                }
            }
        }
        val content: @Composable () -> Unit = {
            Column(modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, bottom = 8.dp)) {
                body()
            }
        }
        // With animations off the body is there as soon as the row is open: a snap still waits for a frame to finish.
        if (animated) {
            AnimatedVisibility(
                visible = opened,
                enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
                exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
            ) {
                content()
            }
        } else if (opened) {
            content()
        }
    }
}

// The content of one phase, tagged with its word: the runs and attempts of implementing, each round of checking, each
// review and reviewer of reviewing, and whether the task is integrated.
@Composable
private fun PhaseBody(output: PhaseOutput, phase: Phase, loadArtifact: ArtifactLoader) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag("phase-body:${phase.word}"),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        when (phase) {
            Phase.IMPLEMENTING -> {
                EvidenceRuns(requirement = TASK_RUNS, runs = listOfNotNull(output.red, output.green), loadArtifact = loadArtifact)
                output.attempts.forEach { attempt -> AttemptView(attempt) }
            }
            Phase.CHECKING -> output.rounds.forEach { round -> RoundView(round) }
            Phase.REVIEWING -> {
                output.reviews.forEach { review -> ReviewView(review) }
                output.reviewers.forEach { reviewer ->
                    if (reviewer.summary.isNotEmpty()) Text(reviewer.summary, style = MaterialTheme.typography.bodySmall)
                }
            }
            Phase.INTEGRATING -> Text(if (output.integrated) "integrated" else "waits to integrate")
        }
    }
}

// An attempt: what it is doing while it runs, else how it ended, with its reason or else its summary.
@Composable
private fun AttemptView(attempt: Attempt) {
    if (attempt.end.isEmpty()) {
        if (attempt.doing.isNotEmpty()) Text(attempt.doing, style = MaterialTheme.typography.bodySmall)
        return
    }
    Text(attempt.end, style = MaterialTheme.typography.labelSmall)
    val note = attempt.reason.ifEmpty { attempt.summary }
    if (note.isNotEmpty()) Text(note, style = MaterialTheme.typography.bodySmall)
}

// A check round: the stages that ran, each check with its mark and a failed check's detail, the gate with its result,
// detail and tails, and the judge's verdict with its reason.
@Composable
private fun RoundView(round: CheckRound) {
    Text("round ${round.round}", style = MaterialTheme.typography.labelLarge)
    Text("ran ${round.ran.joinToString(", ")}", style = MaterialTheme.typography.labelSmall)
    round.checks.forEach { check ->
        Text("${mark(check.ok)} ${check.name}", style = MaterialTheme.typography.bodySmall)
        if (!check.ok && check.detail.isNotEmpty()) Text(check.detail, style = MaterialTheme.typography.bodySmall)
    }
    round.gate?.let { gate ->
        Text("${mark(gate.ok)} gate ${gate.name}", style = MaterialTheme.typography.bodySmall)
        if (gate.detail.isNotEmpty()) Text(gate.detail, style = MaterialTheme.typography.bodySmall)
        if (gate.stdout.isNotEmpty()) {
            Text(gate.stdout, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
        if (gate.stderr.isNotEmpty()) {
            Text(gate.stderr, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
    }
    round.judge?.let { judge ->
        Text("judge ${judge.verdict}", style = MaterialTheme.typography.bodySmall)
        if (judge.reason.isNotEmpty()) Text(judge.reason, style = MaterialTheme.typography.bodySmall)
    }
}

// A review: its attempt, gate and verdict, then each finding's claim, location, result and reason.
@Composable
private fun ReviewView(review: TaskReviewRow) {
    Text("attempt ${review.attempt}", style = MaterialTheme.typography.labelLarge)
    Text("gate ${review.gate}", style = MaterialTheme.typography.labelSmall)
    Text("verdict ${review.verdict}", style = MaterialTheme.typography.bodySmall)
    review.findings.forEach { finding ->
        Text(finding.claim, style = MaterialTheme.typography.bodySmall)
        Text(finding.location, style = MaterialTheme.typography.labelSmall)
        Text(finding.result, style = MaterialTheme.typography.labelSmall)
        if (finding.reason.isNotEmpty()) Text(finding.reason, style = MaterialTheme.typography.bodySmall)
    }
}

private fun mark(ok: Boolean): String = if (ok) "✓" else "✗"
