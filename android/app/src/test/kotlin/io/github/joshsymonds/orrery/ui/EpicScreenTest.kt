package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicScreenTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val TIMEOUT_MS = 5_000L
        const val CHANGED = 4_102_444_800L
    }

    @get:Rule
    val composeRule = createComposeRule()

    private val openedStep = AtomicReference<StepRow?>(null)
    private var backed = 0

    private fun task(effort: Int, slug: String, subject: String, status: String): EpicTask =
        EpicTask.newBuilder()
            .setEffort(effort)
            .setSlug(slug)
            .setSubject(subject)
            .setStatus(status)
            .build()

    private fun step(
        id: String,
        task: String,
        agent: String,
        end: String,
        held: Boolean = false,
        checks: List<Pair<String, Boolean>> = emptyList(),
    ): EpicStep {
        val builder = EpicStep.newBuilder()
            .setId(id)
            .setTask(task)
            .setAgent(agent)
            .setEnd(end)
            .setStatus(if (end.isEmpty()) "working" else "ended")
            .setHeld(held)
            .setStarted(CHANGED - 600)
            .setEnded(if (end.isEmpty()) 0L else CHANGED - 60)
            .setDoing("")
            .setReason("")
        checks.forEachIndexed { index, (name, ok) ->
            builder.addChecks(EpicStepCheck.newBuilder().setRound(index + 1).setName(name).setOk(ok))
        }
        return builder.build()
    }

    private fun body(
        tasks: List<EpicTask> = emptyList(),
        steps: List<EpicStep> = emptyList(),
        problem: String = "",
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome("in progress")
            .setChanged(CHANGED)
            .setLive(true)
            .addAllRunningSteps(steps.filter { it.end.isEmpty() })
            .setEndedSteps(steps.count { it.end.isNotEmpty() })
            .setProblem(problem)
        return EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllTasks(tasks)
            .addAllSteps(steps)
            .build()
    }

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            EpicScreen(
                client = OrreryClient(server.baseUrl),
                epicId = EPIC_ID,
                onBack = { backed++ },
                onOpenStep = { openedStep.set(it) },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun hasTagPrefix(prefix: String): SemanticsMatcher =
        SemanticsMatcher("test tag starts with $prefix") {
            it.config.contains(SemanticsProperties.TestTag) &&
                it.config[SemanticsProperties.TestTag].startsWith(prefix)
        }

    private fun awaitText(text: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasText(text)) > 0 }
    }

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    @Test
    fun oneRingPerTaskDrawsItsOwnStateAndAnUnknownStatusStaysRaw() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    tasks = listOf(
                        task(1, "example-a", "Subject A", "done"),
                        task(2, "example-b", "Subject B", "dispatched"),
                        task(3, "example-c", "Subject C", "gated"),
                        task(4, "example-d", "Subject D", "pending"),
                        task(5, "example-e", "Subject E", "gap"),
                        task(6, "example-f", "Subject F", "split"),
                        task(7, "example-g", "Subject G", "Reviewing: 2 of 3"),
                    ),
                ),
            )
            setScreen(server)
            awaitText("Subject G")

            for (state in listOf("done", "dispatched", "gated", "pending", "gap", "split", "neutral")) {
                assertEquals("ring $state", 1, countNodes(hasTestTag("task-ring:$state")))
            }
            assertEquals(7, countNodes(hasTagPrefix("task-ring:")))
            assertEquals(1, countNodes(hasText("Reviewing: 2 of 3")))
        }
    }

    @Test
    fun ringsFollowTheStatusWordsWithoutCaseAndUnknownTextStaysRaw() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    tasks = listOf(
                        task(1, "example-a", "Subject A", "integrated"),
                        task(2, "example-b", "Subject B", "DONE"),
                        task(3, "example-c", "Subject C", "Integrated"),
                        task(4, "example-d", "Subject D", "Gated"),
                        task(5, "example-e", "Subject E", "done at 3f9c2e1"),
                        task(6, "example-f", "Subject F", "integrated (e2cb5e4)"),
                    ),
                ),
            )
            setScreen(server)
            awaitText("Subject F")

            assertEquals(3, countNodes(hasTestTag("task-ring:done")))
            assertEquals(1, countNodes(hasTestTag("task-ring:gated")))
            assertEquals(2, countNodes(hasTestTag("task-ring:neutral")))
            assertEquals(1, countNodes(hasText("done at 3f9c2e1")))
            assertEquals(1, countNodes(hasText("integrated (e2cb5e4)")))
        }
    }

    @Test
    fun taskRowShowsSlugSubjectAndRawStatus() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(tasks = listOf(task(1, "example-task", "Example subject", "DONE?  as written"))),
            )
            setScreen(server)
            awaitText("Example subject")

            assertEquals(1, countNodes(hasText("example-task")))
            assertEquals(1, countNodes(hasText("DONE?  as written")))
        }
    }

    @Test
    fun stepRowShowsAgentTaskEndAndOnePipPerCheck() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    steps = listOf(
                        step(
                            "example-step-1",
                            "example-task-1",
                            "example-agent",
                            "",
                            checks = listOf("example-check" to true, "other-check" to false),
                        ),
                        step("example-step-2", "", "other-agent", "", held = true),
                    ),
                ),
            )
            setScreen(server)
            awaitText("example-agent")

            assertEquals(1, countNodes(hasText("example-task-1")))
            assertEquals(1, countNodes(hasText("running")))
            assertEquals(1, countNodes(hasText("held")))
            assertEquals(1, countNodes(hasTestTag("pip:ok") and hasAnyAncestor(hasTestTag("step-row:example-step-1"))))
            assertEquals(1, countNodes(hasTestTag("pip:failed") and hasAnyAncestor(hasTestTag("step-row:example-step-1"))))
            assertEquals(0, countNodes(hasTestTag("pip:ok") and hasAnyAncestor(hasTestTag("step-row:example-step-2"))))
            assertEquals(0, countNodes(hasTestTag("pip:failed") and hasAnyAncestor(hasTestTag("step-row:example-step-2"))))
        }
    }

    @Test
    fun endedStepsFoldIntoOneLineThatOpens() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    steps = listOf(
                        step("example-step-1", "", "example-agent", ""),
                        step("example-step-2", "", "other-agent", "checks_failed", checks = listOf("example-check" to false)),
                        step("example-step-3", "", "third-agent", "done"),
                    ),
                ),
            )
            setScreen(server)
            awaitText("2 ended steps")

            assertEquals(1, countNodes(hasTestTag("step-row:example-step-1")))
            assertEquals(0, countNodes(hasTestTag("step-row:example-step-2")))
            assertEquals(0, countNodes(hasText("checks_failed")))

            clickTag("ended-steps")

            assertEquals(1, countNodes(hasTestTag("step-row:example-step-2")))
            assertEquals(1, countNodes(hasTestTag("step-row:example-step-3")))
            assertEquals(1, countNodes(hasText("checks_failed")))
            assertEquals(1, countNodes(hasTestTag("pip:failed") and hasAnyAncestor(hasTestTag("step-row:example-step-2"))))
        }
    }

    @Test
    fun tappingAStepOpensIt() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(steps = listOf(step("example-step-1", "example-task-1", "example-agent", ""))),
            )
            setScreen(server)
            awaitText("example-agent")

            clickTag("step-row:example-step-1")

            val opened = openedStep.get()
            assertEquals("example-step-1", opened?.id)
            assertEquals("example-task-1", opened?.task)
            assertEquals("example-agent", opened?.agent)
        }
    }

    @Test
    fun unknownEpicShowsEpicNotFound() {
        FakeOrreryServer().use { server ->
            setScreen(server)

            awaitText("epic not found")
            assertEquals(0, countNodes(hasText("Retry")))
        }
    }

    @Test
    fun failedLoadShowsRetryAndRetryLoadsTheEpic() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_GET, Code.UNAVAILABLE, "synthetic outage")
            setScreen(server)
            awaitText("could not load epic")

            server.respond(
                EPICS_GET,
                body(tasks = listOf(task(1, "example-task", "Loaded after retry", "done"))),
            )
            composeRule.onNodeWithText("Retry").performClick()

            awaitText("Loaded after retry")
        }
    }

    @Test
    fun problemShowsAsSent() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(problem = "efforts/1/state.json: does not parse"),
            )
            setScreen(server)

            awaitText("efforts/1/state.json: does not parse")
        }
    }

    @Test
    fun headerShowsSlugRepositoryAndOutcome() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body())
            setScreen(server)

            awaitText("example-epic")
            assertEquals(1, countNodes(hasText("example-epic")))
            assertEquals(1, countNodes(hasText("example-repo · in progress")))
        }
    }

    @Test
    fun backButtonCallsOnBack() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body())
            setScreen(server)
            // Wait for the load to land, so no request is still in flight when the server closes at the end.
            awaitText("example-epic")

            clickTag("epic-back")

            assertEquals(1, backed)
        }
    }
}
