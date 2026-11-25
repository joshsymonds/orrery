package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
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
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "example-epic"
private const val TASK = "T1"
private const val NOW = 1_700_000_000L
private val STAGES = listOf("red", "implement", "green", "review", "integrate")

// The five stage rows of a task's record, driven as the record's stateless body over synthetic epic details.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w600dp-h1200dp")
class TaskStagesTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun theFiveRowsComeInPlaceOrder() {
        showBody(detail(buildTasks = listOf(buildTask(place = "implement", stage = "implement"))))

        val tops = STAGES.map { composeRule.onNodeWithTag("stage-head:$it", useUnmergedTree = true).fetchSemanticsNode().positionInRoot.y }
        assertTrue("expected the rows top to bottom in place order, got $tops", tops.zipWithNext().all { (a, b) -> a < b })
    }

    @Test
    fun theCurrentRowIsOpenAndTheOthersAreFolded() {
        showBody(detail(buildTasks = listOf(buildTask(place = "implement", stage = "implement"))))

        composeRule.onNodeWithTag("stage-body:implement", useUnmergedTree = true).assertExists()
        listOf("red", "green", "review", "integrate").forEach { assertNoNode("stage-body:$it") }
        composeRule.onNodeWithTag("stage-tone:implement", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theNowRowSaysItsTonesWord() {
        showBody(detail(buildTasks = listOf(buildTask(place = "implement", stage = "implement"))))

        val tone = composeRule.onNodeWithTag("stage-tone:implement", useUnmergedTree = true).fetchSemanticsNode()
        assertEquals("run", tone.config[SemanticsProperties.StateDescription])
    }

    @Test
    fun theOrchestrateStageSaysAnOrchestratorDecides() {
        showBody(detail(buildTasks = listOf(buildTask(place = "implement", stage = "orchestrate"))))

        composeRule.onNodeWithTag("stage-sum:implement", useUnmergedTree = true)
            .assertTextEquals("an orchestrator is deciding on the implementer’s request")
    }

    @Test
    fun theVerifyStageSaysAVerifierChecksTheReview() {
        showBody(detail(buildTasks = listOf(buildTask(place = "review", stage = "verify"))))

        composeRule.onNodeWithTag("stage-sum:review", useUnmergedTree = true)
            .assertTextEquals("a verifier is checking its findings")
        composeRule.onNodeWithTag("stage-body:review", useUnmergedTree = true).assertExists()
    }

    @Test
    fun anAheadRowCannotBeOpened() {
        showBody(detail(buildTasks = listOf(buildTask(place = "red", stage = "red"))))

        composeRule.onNodeWithTag("stage-head:review", useUnmergedTree = true).assertHasNoClickAction()
        composeRule.onNodeWithTag("stage-head:review", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()

        assertNoNode("stage-body:review")
        assertNoNode("stage-time:review")
        composeRule.onNodeWithTag("stage-sum:review", useUnmergedTree = true).assertTextEquals("not reached")
    }

    @Test
    fun aPassedRowOpensInPlaceClosingTheOtherAndItsHeadFoldsIt() {
        showBody(detail(buildTasks = listOf(buildTask(place = "green", stage = "green"))))

        composeRule.onNodeWithTag("stage-head:red", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag("stage-body:red", useUnmergedTree = true).assertExists()
        assertNoNode("stage-body:green")

        composeRule.onNodeWithTag("stage-head:red", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()
        STAGES.forEach { composeRule.awaitNone(hasTestTag("stage-body:$it")) }
    }

    @Test
    fun theRedRowShowsItsRunOrSaysNoRedRunYet() {
        showBody(detail(buildTasks = listOf(buildTask(place = "red", stage = "red"))))

        composeRule.onNodeWithText("No red run yet.", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("stage-sum:red", useUnmergedTree = true).assertTextEquals("capturing the failing test")
    }

    @Test
    fun theRedRowDrawsTheRedRun() {
        showBody(detail(buildTasks = listOf(buildTask(place = "red", stage = "red", red = run("red-a", "red", passed = false)))))

        composeRule.onNodeWithTag("stage-body:red", useUnmergedTree = true).assertExists()
        composeRule.onAllNodesWithTag("evidence-run:-1:0", useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun theImplementBodyListsItsAttemptsAndItsLatestRoundsAsCheckBlocks() {
        val epic = detail(
            buildTasks = listOf(buildTask(place = "implement", stage = "implement")),
            steps = listOf(
                step("w1", end = "returned", ended = 130, checks = checkRounds()),
            ),
        )

        showBody(epic)

        composeRule.onNodeWithTag("stage-body:implement", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Attempts", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("spent", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Attempt 1 · check rounds", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("block:checks", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("fmt: synthetic fmt detail", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theSessionDoorOpensTheLatestWritingStep() {
        val opened = CopyOnWriteArrayList<StepRow>()
        val writer = step("w1", end = "returned", ended = 130)
        showBody(
            detail(buildTasks = listOf(buildTask(place = "implement", stage = "implement")), steps = listOf(writer)),
            onOpenSession = { opened.add(it) },
        )

        composeRule.onNode(hasTestTag("open-session") and hasAnyAncestor(hasTestTag("stage-body:implement")), useUnmergedTree = true)
            .performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(writer), opened.toList())
    }

    @Test
    fun theIntegrateBodySaysMergedOnceIntegrated() {
        showBody(detail(buildTasks = listOf(buildTask(place = "integrated", tone = "ok"))))

        composeRule.onNodeWithTag("stage-body:integrate", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Merged.", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theReviewBodySaysNoReviewHasRunYet() {
        showBody(detail(buildTasks = listOf(buildTask(place = "review", stage = "review"))))

        composeRule.onNodeWithText("No review has run yet.", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theIntegrateBodySaysItWaitsForItsMergeWhileNotIntegrated() {
        showBody(detail(buildTasks = listOf(buildTask(place = "integrate", tone = "acc"))))

        composeRule.onNodeWithTag("stage-body:integrate", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Every gate took it. It waits for its merge.", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theGreenBodyDrawsItsReproductionAndItsGreenRun() {
        showBody(detail(buildTasks = listOf(buildTask(place = "green", stage = "green", green = run("green-a", "green", passed = true)))))

        composeRule.onNodeWithTag("stage-body:green", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Reproduction", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("The red test, run on the change, now passes.", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Green run", useUnmergedTree = true).assertExists()
        composeRule.onAllNodesWithTag("evidence-run:-1:0", useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun theGreenBodySaysNoGreenRunYetWhenNoneRan() {
        showBody(detail(buildTasks = listOf(buildTask(place = "green", stage = "green"))))

        composeRule.onNodeWithText("The red test has not run on the change yet.", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("No green run yet.", useUnmergedTree = true).assertExists()
    }

    @Test
    fun theImplementBodyDrawsTheGateAndItsLogTails() {
        val gateRound = listOf(
            StepCheckRow(
                round = 1,
                name = "gate",
                ok = false,
                stdout = "",
                stderr = "",
                tailStdout = "synthetic gate stdout",
                tailStderr = "synthetic gate stderr",
            ),
        )
        val epic = detail(
            buildTasks = listOf(buildTask(place = "implement", stage = "implement")),
            steps = listOf(step("w1", end = "returned", ended = 130, checks = gateRound, gateName = "synthetic gate")),
        )

        showBody(epic)

        composeRule.onNodeWithText("Gate · synthetic gate", useUnmergedTree = true).assertExists()
        composeRule.onNode(hasText("synthetic gate stdout") and hasAnyAncestor(hasTestTag("gate-tail")), useUnmergedTree = true)
            .assertExists()
        composeRule.onNode(hasText("synthetic gate stderr") and hasAnyAncestor(hasTestTag("gate-tail")), useUnmergedTree = true)
            .assertExists()
    }

    @Test
    fun theImplementBodyJudgesTheLatestAttempt() {
        val epic = detail(
            buildTasks = listOf(buildTask(place = "implement", stage = "implement")),
            steps = listOf(
                step(
                    "w1",
                    end = "returned",
                    ended = 130,
                    verdicts = listOf(StepVerdictRow(round = 1, verdict = "unmet", reason = "synthetic reason")),
                ),
            ),
        )

        showBody(epic)

        composeRule.onNodeWithText("Judge", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithText("Judged: unmet. synthetic reason", useUnmergedTree = true).assertExists()
    }

    @Test
    fun aReviewWithNoFindingsDrawsItsRowWithItsVerdict() {
        val review = TaskReviewRow(attempt = 1, gate = "lint", step = "v1", verdict = "holds", findings = emptyList())

        showBody(detail(buildTasks = listOf(buildTask(place = "review", stage = "review", reviews = listOf(review)))))

        composeRule.onNodeWithTag("review-row:lint", useUnmergedTree = true).assertExists()
        composeRule.onNode(hasText("lint") and hasAnyAncestor(hasTestTag("review-row:lint")), useUnmergedTree = true).assertExists()
        composeRule.onNode(hasText("no findings") and hasAnyAncestor(hasTestTag("review-row:lint")), useUnmergedTree = true)
            .assertExists()
        composeRule.onNode(hasText("holds") and hasAnyAncestor(hasTestTag("review-row:lint")), useUnmergedTree = true).assertExists()
    }

    @Test
    fun aFindingDrawsItsClaimWhereAndWhatHappenedToIt() {
        val review = TaskReviewRow(
            attempt = 1,
            gate = "spec",
            step = "v1",
            verdict = "fails",
            findings = listOf(
                TaskFindingRow(id = "f1", claim = "synthetic claim one", location = "parser.kt:12", step = "", result = "", reason = ""),
                TaskFindingRow(
                    id = "f2",
                    claim = "synthetic claim two",
                    location = "parser.kt:40",
                    step = "v1",
                    result = "confirmed",
                    reason = "synthetic reason",
                ),
                TaskFindingRow(
                    id = "f3",
                    claim = "synthetic claim three",
                    location = "parser.kt:77",
                    step = "v1",
                    result = "dropped",
                    reason = "synthetic dropped reason",
                ),
            ),
        )

        showBody(detail(buildTasks = listOf(buildTask(place = "review", stage = "review", reviews = listOf(review)))))

        listOf("f1", "f2", "f3").forEach { composeRule.onNodeWithTag("finding:$it", useUnmergedTree = true).assertExists() }
        inFinding("f1", "synthetic claim one")
        inFinding("f1", "parser.kt:12")
        inFinding("f1", "verifying")
        inFinding("f1", "a verifier is checking it")
        inFinding("f2", "synthetic claim two")
        inFinding("f2", "parser.kt:40")
        inFinding("f2", "confirmed")
        inFinding("f2", "synthetic reason")
        inFinding("f3", "dropped")
        inFinding("f3", "synthetic dropped reason")
    }

    @Test
    fun theReviewSessionDoorOpensTheLatestReadingStep() {
        val opened = CopyOnWriteArrayList<StepRow>()
        val reader = step("v1", end = "returned", ended = 130, readOnly = true)
        val review = TaskReviewRow(attempt = 1, gate = "spec", step = "v1", verdict = "holds", findings = emptyList())
        showBody(
            detail(buildTasks = listOf(buildTask(place = "review", stage = "review", reviews = listOf(review))), steps = listOf(reader)),
            onOpenSession = { opened.add(it) },
        )

        composeRule.onNode(hasTestTag("open-session") and hasAnyAncestor(hasTestTag("stage-body:review")), useUnmergedTree = true)
            .performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(reader), opened.toList())
    }

    private fun inFinding(id: String, text: String) {
        composeRule.onNode(hasText(text) and hasAnyAncestor(hasTestTag("finding:$id")), useUnmergedTree = true).assertExists()
    }

    private fun assertNoNode(tag: String) {
        composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(0)
    }

    private fun showBody(detail: EpicDetail, onOpenSession: (StepRow) -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                PhaseOutputBody(
                    detail = detail,
                    task = TASK,
                    brief = null,
                    now = NOW,
                    onOpenEpic = {},
                    onOpenTask = { _, _ -> },
                    onOpenSession = onOpenSession,
                    hold = { StepCallResult.Landed },
                    stop = { StepCallResult.Landed },
                    onLanded = {},
                    loadArtifact = { _, _, _ -> null },
                )
            }
        }
    }

    private fun step(
        id: String,
        end: String = "",
        started: Long = 100,
        ended: Long = 0,
        checks: List<StepCheckRow> = emptyList(),
        readOnly: Boolean = false,
        gateName: String = "",
        verdicts: List<StepVerdictRow> = emptyList(),
    ): StepRow = StepRow(
        id = id,
        task = TASK,
        agent = "builder",
        end = end,
        status = "",
        held = false,
        started = started,
        ended = ended,
        doing = "",
        checks = checks,
        reason = "",
        readOnly = readOnly,
        gateName = gateName,
        verdicts = verdicts,
    )

    // Round one fails its fmt check with a detail; its gate passes.
    private fun checkRounds(): List<StepCheckRow> = listOf(
        StepCheckRow(round = 1, name = "fmt", ok = false, stdout = "", stderr = "", detail = "synthetic fmt detail"),
        StepCheckRow(round = 1, name = "gate", ok = true, stdout = "", stderr = ""),
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

    // The build task at the place and tone serve computed for it, on its build stage.
    private fun buildTask(
        place: String,
        tone: String = "run",
        stage: String = "",
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
        reviews: List<TaskReviewRow> = emptyList(),
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = TASK,
        title = "Parser $TASK",
        status = if (place == "integrated") "done" else "open",
        stage = stage,
        red = red,
        green = green,
        requirements = listOf("R1"),
        reviews = reviews,
        place = place,
        tone = tone,
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
        ),
        steps = steps,
        buildTasks = buildTasks,
    )
}
