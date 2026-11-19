package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNode
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNodeCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The window is tall so every row is composed: a lazy list composes only the rows its window shows.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h3000dp")
class WorkflowNodesTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val EPIC_SLUG = "example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val CHANGED = 4_102_444_800L
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun node(
        path: String,
        kind: String,
        state: String = "ended",
        outcome: String = "",
        round: Long = 0L,
        loopRound: Long = 0L,
        attempt: Long = 0L,
        output: String = "",
        turns: List<Long> = emptyList(),
        tasks: List<Long> = emptyList(),
        runs: List<EvidenceRun> = emptyList(),
        check: EpicNodeCheck? = null,
        step: String = "",
    ): EpicNode {
        val builder = EpicNode.newBuilder()
            .setPath(path)
            .setNode("example-node")
            .setKind(kind)
            .setState(state)
            .setOutcome(outcome)
            .setRound(round)
            .setLoopRound(loopRound)
            .setAttempt(attempt)
            .setOutput(output)
            .setRevision("example-revision")
            .addAllTurns(turns)
            .addAllTasks(tasks)
            .addAllRuns(runs)
            .setStep(step)
        if (check != null) builder.setCheck(check)
        return builder.build()
    }

    private fun nodeCheck(
        state: String,
        exit: Int = 0,
        timedOut: Boolean = false,
        notRun: String = "",
        stdout: String = "",
        stderr: String = "",
    ): EpicNodeCheck =
        EpicNodeCheck.newBuilder()
            .setState(state)
            .setExit(exit)
            .setTimedOut(timedOut)
            .setNotRun(notRun)
            .setStdout(stdout)
            .setStderr(stderr)
            .build()

    private fun evidenceRun(id: String, verdict: String): EvidenceRun =
        EvidenceRun.newBuilder()
            .setId(id)
            .setPhase("red")
            .setSource("captured")
            .setState("ended")
            .setVerdict(verdict)
            .setPassed(true)
            .setRevision("example-revision")
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .build()

    private fun body(
        nodes: List<EpicNode> = emptyList(),
        requirements: List<EpicRequirement> = emptyList(),
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug(EPIC_SLUG)
            .setOutcome("in progress")
            .setChanged(CHANGED)
            .setLive(true)
        return EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllRequirements(requirements)
            .addAllNodes(nodes)
            .build()
    }

    private fun setScreen(server: FakeOrreryServer) {
        // The screen reads the epic's history with its detail, so an empty history is served beside it.
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
        composeRule.setContent {
            EpicScreen(
                client = OrreryClient(server.baseUrl),
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countNodes(matcher and hasAnyAncestor(hasTestTag(tag)))

    /**
     * Waits until the epic has loaded. The screen draws the epic's title once it has, and its failure or not-found
     * message instead when the load fails, so the wait ends on every outcome and never on a node's text.
     */
    private fun awaitLoaded() {
        composeRule.awaitUntil {
            listOf(EPIC_SLUG, "could not load epic", "epic not found").any { countNodes(hasText(it)) > 0 }
        }
    }

    @Test
    fun anEpicWithNoNodesShowsNoWorkflowSection() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body())
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasText("WORKFLOW")))
            assertEquals(0, countNodes(hasTestTag("node-row:0")))
        }
    }

    @Test
    fun theWorkflowListsEachNodeInOrderWithItsHeaderFields() {
        val nodes = listOf(
            node("example-plan", kind = "plan", outcome = "complete"),
            node("example-build", kind = "build", state = "running"),
            node("example-gate", kind = "gate", outcome = "failed"),
            node("example-command", kind = "command", outcome = "passed"),
            node("example-evidence", kind = "evidence", outcome = "passed"),
            node("example-agent", kind = "agent", state = "parked", outcome = "failed"),
            node("example-loop", kind = "loop", loopRound = 2L),
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = nodes))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countNodes(hasText("WORKFLOW")))
            val tops = nodes.mapIndexed { index, node ->
                val row = "node-row:$index"
                assertEquals(1, countNodes(hasTestTag(row)))
                assertEquals(1, countIn(row, hasText(node.path)))
                assertEquals(1, countIn(row, hasText(node.kind)))
                assertEquals(1, countIn(row, hasText(node.state)))
                if (node.outcome.isNotEmpty()) assertEquals(1, countIn(row, hasText(node.outcome)))
                assertEquals(1, countIn(row, hasText("round 0")))
                composeRule.onNodeWithTag(row, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top
            }
            assertEquals(0, countNodes(hasTestTag("node-row:${nodes.size}")))
            assertTrue(
                composeRule.onNodeWithText("WORKFLOW", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top <
                    tops.first(),
            )
            assertTrue(tops.zipWithNext().all { (above, below) -> above < below })
        }
    }

    @Test
    fun aPlanNodeShowsItsTurnsAndTasksOneRowEach() {
        val plan = node("example-plan", kind = "plan", turns = listOf(1L, 2L), tasks = listOf(3L, 4L))
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(plan)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("turn 1")))
            assertEquals(1, countIn("node-row:0", hasText("turn 2")))
            assertEquals(1, countIn("node-row:0", hasText("task 3")))
            assertEquals(1, countIn("node-row:0", hasText("task 4")))
        }
    }

    @Test
    fun anEvidenceNodeShowsItsRunsAsEvidenceRunsDoApartFromTheRequirementsRuns() {
        val requirement = EpicRequirement.newBuilder()
            .setId("R1")
            .setText("Example requirement.")
            .setEvidence("example evidence")
            .setKind("check")
            .addRuns(evidenceRun("example-requirement-run", "example requirement verdict"))
            .build()
        val evidence = node(
            "example-evidence",
            kind = "evidence",
            runs = listOf(evidenceRun("example-node-run", "example node verdict")),
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(evidence), requirements = listOf(requirement)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countNodes(hasTestTag("evidence-run:0:0")))
            assertEquals(1, countIn("node-row:0", hasTestTag("evidence-run:-1:0")))
            assertEquals(1, countIn("node-row:0", hasText("example node verdict")))
            assertEquals(0, countIn("node-row:0", hasText("example requirement verdict")))
            assertEquals(1, countNodes(hasText("example requirement verdict")))
        }
    }

    @Test
    fun aGateNodeShowsItsCheckStateExitTimeoutAndOutputTails() {
        val gate = node(
            "example-gate",
            kind = "gate",
            outcome = "failed",
            check = nodeCheck(
                "ended",
                exit = 2,
                timedOut = true,
                stdout = "example stdout tail",
                stderr = "example stderr tail",
            ),
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(gate)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("check ended")))
            assertEquals(1, countIn("node-row:0", hasText("exit 2")))
            assertEquals(1, countIn("node-row:0", hasText("timed out")))
            assertEquals(1, countIn("node-check-tail:0:stdout", hasText("example stdout tail")))
            assertEquals(0, countIn("node-check-tail:0:stdout", hasText("example stderr tail")))
            assertEquals(1, countIn("node-check-tail:0:stderr", hasText("example stderr tail")))
            assertEquals(0, countIn("node-check-tail:0:stderr", hasText("example stdout tail")))
        }
    }

    @Test
    fun aRunningCheckShowsItsStateAndNoExitYet() {
        val gate = node("example-gate", kind = "gate", state = "running", check = nodeCheck("running"))
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(gate)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("check running")))
            assertEquals(0, countIn("node-row:0", hasText("exit", substring = true)))
        }
    }

    @Test
    fun aCheckThatDidNotRunShowsItsReasonAndNoExit() {
        val command = node(
            "example-command",
            kind = "command",
            check = nodeCheck("ended", notRun = "example not-run reason"),
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(command)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("example not-run reason", substring = true)))
            assertEquals(0, countIn("node-row:0", hasText("exit", substring = true)))
        }
    }

    @Test
    fun roundIsShownAndLoopRoundAndAttemptOnlyWhenSet() {
        val nodes = listOf(
            node("example-agent", kind = "agent", round = 2L, loopRound = 3L, attempt = 1L),
            node("example-plain", kind = "agent"),
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = nodes))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("round 2")))
            assertEquals(1, countIn("node-row:0", hasText("loop round 3")))
            assertEquals(1, countIn("node-row:0", hasText("attempt 1")))
            assertEquals(1, countIn("node-row:1", hasText("round 0")))
            assertEquals(0, countIn("node-row:1", hasText("loop round", substring = true)))
            assertEquals(0, countIn("node-row:1", hasText("attempt", substring = true)))
        }
    }

    @Test
    fun anAgentNodeShowsItsStepIdAndOutput() {
        val agent = node(
            "example-agent",
            kind = "agent",
            outcome = "complete",
            step = "example-step-9",
            output = "example output line",
        )
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(nodes = listOf(agent)))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("node-row:0", hasText("step example-step-9")))
            assertEquals(1, countIn("node-row:0", hasText("example output line")))
        }
    }
}
