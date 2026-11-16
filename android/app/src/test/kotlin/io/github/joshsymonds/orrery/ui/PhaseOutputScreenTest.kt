package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StepVerdictRow
import io.github.joshsymonds.orrery.api.TaskFindingRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "example-epic"
private const val TASK = "T1"
private const val OTHER_TASK = "T2"

// A task's phase output screen, driven as its stateless body over synthetic epic details.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w600dp-h1200dp")
class PhaseOutputScreenTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Before
    fun animatorDurationScaleIsOne() {
        setAnimatorDurationScale(1f)
    }

    @Test
    fun theCrumbsAndBackCallTheirCallers() {
        val backs = AtomicInteger()
        val homes = AtomicInteger()
        showBody(
            detail(buildTasks = listOf(buildTask(stage = "red"))),
            Phase.IMPLEMENTING,
            onBack = { backs.incrementAndGet() },
            onGoHome = { homes.incrementAndGet() },
        )

        composeRule.onNodeWithTag("crumb:0").assertTextEquals("orrery")
        composeRule.onNodeWithTag("crumb:1").assertTextEquals(EPIC)
        composeRule.onNodeWithTag("crumb:2").assertTextEquals("$TASK · implementing")
        composeRule.onNodeWithText("Parser $TASK").assertIsDisplayed()
        composeRule.onNodeWithTag("title-back").performClick()
        composeRule.onNodeWithTag("crumb:1").performClick()
        composeRule.onNodeWithTag("crumb:0").performClick()

        assertEquals(2, backs.get())
        assertEquals(1, homes.get())
    }

    @Test
    fun eachPhaseOpensAloneWhenItsRowIsGiven() {
        val integrated = detail(buildTasks = listOf(buildTask(status = "done")))
        val shown = showBody(integrated, Phase.IMPLEMENTING)

        Phase.entries.forEach { phase ->
            composeRule.runOnIdle { shown.value = phase }
            composeRule.waitForIdle()
            Phase.entries.forEach { other ->
                if (other == phase) {
                    composeRule.onNodeWithTag("phase-body:${other.word}").assertExists()
                } else {
                    assertNoNode("phase-body:${other.word}")
                }
            }
        }
    }

    @Test
    fun anotherRowsTapCallsOnOpenPhaseWithTheTaskAndThatPhase() {
        val opened = CopyOnWriteArrayList<Pair<String, Phase>>()
        showBody(
            detail(buildTasks = listOf(buildTask(status = "done"))),
            Phase.IMPLEMENTING,
            onOpenPhase = { task, phase -> opened.add(task to phase) },
        )

        composeRule.onNodeWithTag("phase-row:checking").performClick()
        composeRule.onNodeWithTag("phase-row:integrating").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(TASK to Phase.CHECKING, TASK to Phase.INTEGRATING), opened.toList())
    }

    @Test
    fun anAheadRowCannotOpen() {
        val opened = CopyOnWriteArrayList<Pair<String, Phase>>()
        showBody(
            detail(buildTasks = listOf(buildTask(stage = "red"))),
            Phase.IMPLEMENTING,
            onOpenPhase = { task, phase -> opened.add(task to phase) },
        )

        listOf("checking", "reviewing", "integrating").forEach { word ->
            composeRule.onNodeWithTag("phase-row:$word").assertHasNoClickAction()
        }
        composeRule.onNodeWithTag("phase-row:reviewing").performClick()
        composeRule.waitForIdle()

        assertTrue(opened.isEmpty())
        assertNoNode("phase-body:reviewing")
    }

    @Test
    fun aBodyShowsOnlyItsOwnTasksAndRoundsStrings() {
        val epic = detail(
            buildTasks = listOf(buildTask(TASK), buildTask(OTHER_TASK)),
            steps = listOf(
                step(
                    "own",
                    task = TASK,
                    end = "checks_failed",
                    ended = 130,
                    gateName = "gradle test",
                    checks = checkRounds("own"),
                    verdicts = listOf(StepVerdictRow(round = 2, verdict = "unmet", reason = "synthetic own reason")),
                ),
                step(
                    "other",
                    task = OTHER_TASK,
                    end = "checks_failed",
                    ended = 140,
                    gateName = "gradle test",
                    checks = checkRounds("other"),
                    verdicts = listOf(StepVerdictRow(round = 2, verdict = "unmet", reason = "synthetic other reason")),
                ),
            ),
        )

        showBody(epic, Phase.CHECKING, task = TASK)

        composeRule.onNodeWithText("round 1").assertExists()
        composeRule.onNodeWithText("round 2").assertExists()
        composeRule.onNodeWithText("ran checks, gate").assertExists()
        composeRule.onNodeWithText("ran checks, gate, judge").assertExists()
        composeRule.onNodeWithText("✗ fmt").assertExists()
        composeRule.onNodeWithText("✓ fmt").assertExists()
        composeRule.onNodeWithText("✗ gate gradle test").assertExists()
        composeRule.onNodeWithText("✓ gate gradle test").assertExists()
        composeRule.onNodeWithText("synthetic own fmt round one").assertExists()
        composeRule.onNodeWithText("synthetic own gate round one").assertExists()
        composeRule.onNodeWithText("synthetic own tail round one").assertExists()
        composeRule.onNodeWithText("synthetic own gate round two").assertExists()
        composeRule.onNodeWithText("judge unmet").assertExists()
        composeRule.onNodeWithText("synthetic own reason").assertExists()
        composeRule.onAllNodesWithText("synthetic other", substring = true).assertCountEquals(0)
    }

    @Test
    fun openSessionHandsOverTheStepOfItsRow() {
        val writer = step("w1", end = "returned", ended = 130, reason = "synthetic return")
        val reader = step("r1", readOnly = true, end = "done", started = 140, ended = 160, summary = "synthetic summary")
        val opened = CopyOnWriteArrayList<StepRow>()
        showBody(
            detail(buildTasks = listOf(buildTask(TASK)), steps = listOf(writer, reader)),
            Phase.REVIEWING,
            onOpenSession = { opened.add(it) },
        )

        composeRule.onNode(hasTestTag("open-session") and hasAnyAncestor(hasTestTag("phase-row:reviewing")))
            .performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(reader), opened.toList())
    }

    @Test
    fun holdCallsOnceAndALandedCallReloads() {
        val holds = CopyOnWriteArrayList<String>()
        val reloads = AtomicInteger()
        showBody(
            runningDetail(),
            Phase.IMPLEMENTING,
            hold = { id ->
                holds.add(id)
                StepCallResult.Landed
            },
            onLanded = { reloads.incrementAndGet() },
        )

        composeRule.onNodeWithTag("phase-hold").performClick()
        composeRule.awaitUntil("the hold call and its reload") { holds.size == 1 && reloads.get() == 1 }

        assertEquals(listOf("w1"), holds.toList())
    }

    @Test
    fun noHoldWhenTheRunningStepIsHeld() {
        showBody(runningDetail(held = true), Phase.IMPLEMENTING)

        assertNoNode("phase-hold")
        composeRule.onNodeWithTag("phase-stop").assertExists()
    }

    @Test
    fun noHoldOrStopWithNothingRunning() {
        showBody(
            detail(buildTasks = listOf(buildTask(TASK)), steps = listOf(step("w1", end = "returned", ended = 130))),
            Phase.IMPLEMENTING,
        )

        assertNoNode("phase-hold")
        assertNoNode("phase-stop")
    }

    @Test
    fun stopCallsNothingUntilConfirmed() {
        val stops = CopyOnWriteArrayList<String>()
        showBody(
            runningDetail(),
            Phase.IMPLEMENTING,
            stop = { id ->
                stops.add(id)
                StepCallResult.Landed
            },
        )

        composeRule.onNodeWithTag("phase-stop").performClick()
        composeRule.onNodeWithTag("phase-stop-sure").assertExists()
        composeRule.waitForIdle()
        assertTrue(stops.isEmpty())

        composeRule.onNodeWithTag("phase-stop-keep").performClick()
        composeRule.waitForIdle()
        assertTrue(stops.isEmpty())

        composeRule.onNodeWithTag("phase-stop").performClick()
        composeRule.onNodeWithTag("phase-stop-confirm").performClick()
        composeRule.awaitUntil("the stop call") { stops.size == 1 }

        assertEquals(listOf("w1"), stops.toList())
    }

    @Test
    fun aRefusalShowsItsLine() {
        val reloads = AtomicInteger()
        showBody(
            runningDetail(),
            Phase.IMPLEMENTING,
            hold = { StepCallResult.Refused("synthetic refusal") },
            onLanded = { reloads.incrementAndGet() },
        )

        composeRule.onNodeWithTag("phase-hold").performClick()
        composeRule.awaitUntil("the refusal line") {
            composeRule.onAllNodesWithText("hold refused: synthetic refusal").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithText("hold refused: synthetic refusal").assertExists()
        assertEquals(0, reloads.get())
    }

    @Test
    fun aCallThatMayHaveLandedShowsItsLine() {
        showBody(
            runningDetail(),
            Phase.IMPLEMENTING,
            stop = { throw IOException("synthetic transport failure") },
        )

        composeRule.onNodeWithTag("phase-stop").performClick()
        composeRule.onNodeWithTag("phase-stop-confirm").performClick()
        composeRule.awaitUntil("the unknown-call line") {
            composeRule.onAllNodesWithText("stop not known: it may have landed").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithText("stop not known: it may have landed").assertExists()
    }

    @Test
    fun aRowsBodyShowsWithTheClockPausedWhenAnimationsAreOff() {
        setAnimatorDurationScale(0f)
        composeRule.mainClock.autoAdvance = false
        val shown = showBody(checkingDetail(), Phase.IMPLEMENTING)

        composeRule.runOnIdle { shown.value = Phase.CHECKING }
        composeRule.waitForIdle()
        // One frame lays the new body out; the clock itself does not run on.
        composeRule.mainClock.advanceTimeByFrame()

        composeRule.onNodeWithTag("phase-body:checking").assertIsDisplayed()
        assertNoNode("phase-body:implementing")
    }

    @Test
    fun implementingShowsTheRedAndGreenRunsAndEachAttempt() {
        val epic = detail(
            buildTasks = listOf(
                buildTask(
                    TASK,
                    red = run("red-a", "red", passed = false),
                    green = run("green-a", "green", passed = true),
                ),
            ),
            steps = listOf(
                step("w1", end = "returned", ended = 130, reason = "synthetic return reason"),
                step("w2", end = "", started = 140, doing = "synthetic doing now"),
            ),
        )

        showBody(epic, Phase.IMPLEMENTING)

        composeRule.onNodeWithText("red").assertExists()
        composeRule.onNodeWithText("green").assertExists()
        composeRule.onNodeWithText("returned").assertExists()
        composeRule.onNodeWithText("synthetic return reason").assertExists()
        // The running step's doing also shows as its phase row's summary, so it is on screen more than once.
        assertTrue(composeRule.onAllNodesWithText("synthetic doing now").fetchSemanticsNodes().isNotEmpty())
    }

    @Test
    fun reviewingShowsEachReviewsGateVerdictFindingsAndTheReviewersSummary() {
        val finding = TaskFindingRow(
            id = "f1",
            claim = "synthetic claim",
            location = "synthetic.kt:7",
            step = "r1",
            result = "fixed",
            reason = "synthetic finding reason",
        )
        val epic = detail(
            buildTasks = listOf(
                buildTask(
                    TASK,
                    reviews = listOf(
                        TaskReviewRow(
                            attempt = 2L,
                            gate = "review",
                            step = "r1",
                            verdict = "holds",
                            findings = listOf(finding),
                        ),
                    ),
                ),
            ),
            steps = listOf(
                step("w1", end = "returned", ended = 130),
                step("r1", readOnly = true, end = "done", started = 140, ended = 160, summary = "synthetic reviewer summary"),
            ),
        )

        showBody(epic, Phase.REVIEWING)

        composeRule.onNodeWithText("attempt 2").assertExists()
        composeRule.onNodeWithText("gate review").assertExists()
        composeRule.onNodeWithText("verdict holds").assertExists()
        composeRule.onNodeWithText("synthetic claim").assertExists()
        composeRule.onNodeWithText("synthetic.kt:7").assertExists()
        composeRule.onNodeWithText("fixed").assertExists()
        composeRule.onNodeWithText("synthetic finding reason").assertExists()
        // The reviewing row's summary is the same reviewer's summary, so it is on screen more than once.
        assertTrue(composeRule.onAllNodesWithText("synthetic reviewer summary").fetchSemanticsNodes().isNotEmpty())
    }

    @Test
    fun integratingWaitsWhileTheTaskIsGated() {
        showBody(detail(buildTasks = listOf(buildTask(status = "gated"))), Phase.INTEGRATING)

        composeRule.onNodeWithText("waits to integrate").assertExists()
    }

    @Test
    fun integratingShowsIntegratedWhenTheTaskIs() {
        showBody(detail(buildTasks = listOf(buildTask(status = "done"))), Phase.INTEGRATING)

        composeRule.onNodeWithText("integrated").assertExists()
    }

    private fun assertNoNode(tag: String) {
        composeRule.onAllNodesWithTag(tag).assertCountEquals(0)
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            scale,
        )
    }

    // Sets the body up over [detail] with its phase state in the returned state, so a test can open another row.
    private fun showBody(
        detail: EpicDetail,
        phase: Phase,
        task: String = TASK,
        onBack: () -> Unit = {},
        onGoHome: () -> Unit = {},
        onOpenPhase: (String, Phase) -> Unit = { _, _ -> },
        onOpenSession: (StepRow) -> Unit = {},
        hold: (String) -> StepCallResult = { StepCallResult.Landed },
        stop: (String) -> StepCallResult = { StepCallResult.Landed },
        onLanded: () -> Unit = {},
    ): MutableState<Phase> {
        val shown = mutableStateOf(phase)
        composeRule.setContent {
            MaterialTheme {
                PhaseOutputBody(
                    detail = detail,
                    epic = EPIC,
                    task = task,
                    phase = shown.value,
                    onBack = onBack,
                    onGoHome = onGoHome,
                    onOpenPhase = onOpenPhase,
                    onOpenSession = onOpenSession,
                    hold = hold,
                    stop = stop,
                    onLanded = onLanded,
                    loadArtifact = { _, _, _ -> null },
                )
            }
        }
        return shown
    }

    private fun step(
        id: String,
        task: String = TASK,
        end: String = "",
        readOnly: Boolean = false,
        held: Boolean = false,
        started: Long = 100,
        ended: Long = 0,
        doing: String = "",
        reason: String = "",
        summary: String = "",
        checks: List<StepCheckRow> = emptyList(),
        gateName: String = "",
        verdicts: List<StepVerdictRow> = emptyList(),
    ): StepRow = StepRow(
        id = id,
        task = task,
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = started,
        ended = ended,
        doing = doing,
        checks = checks,
        reason = reason,
        readOnly = readOnly,
        summary = summary,
        gateName = gateName,
        verdicts = verdicts,
    )

    private fun check(
        round: Int,
        name: String,
        ok: Boolean,
        detail: String = "",
        tailStdout: String = "",
    ): StepCheckRow = StepCheckRow(
        round = round,
        name = name,
        ok = ok,
        stdout = "",
        stderr = "",
        detail = detail,
        tailStdout = tailStdout,
        tailStderr = "",
    )

    // Two rounds of a step's checks: round 1 fails its fmt check and its gate, round 2 passes both.
    private fun checkRounds(label: String): List<StepCheckRow> = listOf(
        check(1, "fmt", false, "synthetic $label fmt round one"),
        check(1, "gate", false, "synthetic $label gate round one", tailStdout = "synthetic $label tail round one"),
        check(2, "fmt", true),
        check(2, "gate", true, "synthetic $label gate round two"),
    )

    private fun run(id: String, phase: String, passed: Boolean): EvidenceRunRow = EvidenceRunRow(
        id = id,
        phase = phase,
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "",
        judgeReason = "",
        passed = passed,
        revision = "",
        created = 0,
        ended = 0,
        logBytes = 0,
        stderrBytes = 0,
        artifacts = emptyList(),
    )

    private fun buildTask(
        id: String = TASK,
        status: String = "dispatched",
        stage: String = "",
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
        reviews: List<TaskReviewRow> = emptyList(),
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = id,
        title = "Parser $id",
        status = status,
        stage = stage,
        red = red,
        green = green,
        requirements = listOf("R1"),
        reviews = reviews,
    )

    private fun detail(
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
        ),
        tasks = emptyList(),
        slices = emptyList(),
        steps = steps,
        buildTasks = buildTasks,
    )

    // A task with one running writing step, which may be held.
    private fun runningDetail(held: Boolean = false): EpicDetail = detail(
        buildTasks = listOf(buildTask(TASK)),
        steps = listOf(step("w1", end = "", started = 100, doing = "synthetic doing", held = held)),
    )

    // A task whose latest step ended in its checks, so it is in checking with a round to show.
    private fun checkingDetail(): EpicDetail = detail(
        buildTasks = listOf(buildTask(TASK)),
        steps = listOf(
            step("w1", end = "checks_failed", ended = 130, gateName = "gradle test", checks = checkRounds("own")),
        ),
    )
}
