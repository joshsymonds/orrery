package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.click
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isNotSelected
import androidx.compose.ui.test.isSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicSlice
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicReference
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.min
import kotlin.math.sin

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

    private fun slice(task: String): EpicSlice =
        EpicSlice.newBuilder().setTask(task).setHue("dispatched").setRedTest(true).setAt(1).build()

    private fun numberedTasks(count: Int): List<EpicTask> =
        (1..count).map { task(it, "example-task-$it", "Subject $it", "done") }

    private fun body(
        tasks: List<EpicTask> = emptyList(),
        steps: List<EpicStep> = emptyList(),
        slices: List<EpicSlice> = emptyList(),
        problem: String = "",
        outcome: String = "in progress",
        requirements: List<EpicRequirement> = emptyList(),
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome(outcome)
            .setChanged(CHANGED)
            .setLive(true)
            .addAllRunningSteps(steps.filter { it.end.isEmpty() })
            .setEndedSteps(steps.count { it.end.isNotEmpty() })
            .setProblem(problem)
        return EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllTasks(tasks)
            .addAllSteps(steps)
            .addAllSlices(slices)
            .addAllRequirements(requirements)
            .build()
    }

    private fun requirement(id: String, text: String, kind: String): EpicRequirement =
        EpicRequirement.newBuilder()
            .setId(id)
            .setText(text)
            .setEvidence("example evidence")
            .setKind(kind)
            .build()

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

    private fun awaitText(text: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasText(text)) > 0 }
    }

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    private fun scrollToRow(index: Int) {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag("task-row:$index"))
    }

    private fun chartStateDescription(): String? =
        composeRule.onNodeWithTag("epic-chart", useUnmergedTree = true)
            .fetchSemanticsNode()
            .config
            .getOrNull(SemanticsProperties.StateDescription)

    // The point [radius] units from the chart's centre at [degrees] clockwise from 12 o'clock, in the chart's own pixels:
    // the coordinates a touch on the chart is sent in. The chart is a 412-unit box that fills the smaller side.
    private fun chartPoint(radius: Float, degrees: Float): Offset {
        val size = composeRule.onNodeWithTag("epic-chart", useUnmergedTree = true).fetchSemanticsNode().size
        val unit = min(size.width, size.height) / 412f
        val angle = degrees * PI.toFloat() / 180f
        return Offset(size.width / 2f + radius * unit * sin(angle), size.height / 2f - radius * unit * cos(angle))
    }

    // The angle at the middle of slice [index] of [count]: a 8-degree start gap, then 344 degrees shared out equally.
    private fun sliceMiddle(index: Int, count: Int): Float = 8f + (index + 0.5f) * 344f / count

    private fun tapChart(position: Offset) {
        composeRule.onNodeWithTag("epic-chart", useUnmergedTree = true).performTouchInput { click(position) }
    }

    // The row's top edge lies at or below the chart's bottom edge, in root pixels.
    private fun assertRowBelowChart(row: String) {
        val chart = composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().boundsInRoot
        val top = composeRule.onNodeWithText(row).fetchSemanticsNode().boundsInRoot.top
        assertTrue(top >= chart.bottom)
    }

    @Test
    fun taskRowShowsSlugSubjectAndRawStatus() {
        val rows = listOf(
            Triple("example-a", "Subject A", "done"),
            Triple("example-b", "Subject B", "Integrated"),
            Triple("example-c", "Subject C", "Gated"),
            Triple("example-d", "Subject D", "done at 3f9c2e1"),
            Triple("example-e", "Subject E", "integrated (e2cb5e4)"),
            Triple("example-f", "Subject F", "Reviewing: 2 of 3"),
            Triple("example-task", "Example subject", "DONE?  as written"),
        )
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    tasks = rows.mapIndexed { index, (slug, subject, status) ->
                        task(index + 1, slug, subject, status)
                    },
                ),
            )
            setScreen(server)
            awaitText("Subject A")

            // The chart takes its share of the screen, so the list scrolls to each row before it is checked.
            for ((index, row) in rows.withIndex()) {
                val (slug, subject, status) = row
                scrollToRow(index)
                assertEquals(1, countNodes(hasText(slug)))
                assertEquals(1, countNodes(hasText(subject)))
                assertEquals(1, countNodes(hasText(status)))
            }
        }
    }

    @Test
    fun everyTaskRowShowsEvenWhenSlugsRepeatOrAreEmpty() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    tasks = listOf(
                        task(1, "example-dup", "Subject A", "done"),
                        task(1, "example-dup", "Subject B", "gated"),
                        task(1, "", "Subject C", "pending"),
                        task(1, "", "Subject D", "dispatched"),
                    ),
                ),
            )
            setScreen(server)
            awaitText("Subject D")

            for (subject in listOf("Subject A", "Subject B", "Subject C", "Subject D")) {
                assertEquals(1, countNodes(hasText(subject)))
            }
            assertEquals(2, countNodes(hasText("example-dup")))
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

    private fun selectionBody(): EpicsGetResponse {
        val numbered = numberedTasks(35)
        val orphan = task(1, "example-orphan", "Subject orphan", "pending")
        return body(tasks = listOf(orphan) + numbered, slices = numbered.reversed().map { slice(it.slug) })
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun tappingATaskRowMarksItAndOutlinesItsSlice() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, selectionBody())
            setScreen(server)
            awaitText("Subject orphan")

            scrollToRow(35)
            clickTag("task-row:35")

            assertEquals(1, countNodes(isSelected()))
            assertEquals(1, countNodes(hasTestTag("task-row:35") and isSelected()))
            composeRule.onNodeWithTag("epic-chart").assertIsDisplayed()
            assertEquals("example-task-35", chartStateDescription())

            clickTag("task-row:35")

            assertEquals(0, countNodes(isSelected()))
            assertEquals(1, countNodes(hasTestTag("task-row:35") and isNotSelected()))
            assertNull(chartStateDescription())

            clickTag("task-row:35")
            scrollToRow(1)
            clickTag("task-row:1")

            assertEquals(1, countNodes(isSelected()))
            assertEquals(1, countNodes(hasTestTag("task-row:1") and isSelected()))
            assertEquals("example-task-1", chartStateDescription())
        }
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun tappingARowWithNoSliceChangesNothing() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, selectionBody())
            setScreen(server)
            awaitText("Subject orphan")

            clickTag("task-row:0")

            assertEquals(0, countNodes(isSelected()))
            assertNull(chartStateDescription())

            clickTag("task-row:1")
            clickTag("task-row:0")

            assertEquals(1, countNodes(isSelected()))
            assertEquals(1, countNodes(hasTestTag("task-row:1") and isSelected()))
            assertEquals("example-task-1", chartStateDescription())
        }
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun aChartTapSelectsItsSliceAndClearsTheMarkedRow() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, selectionBody())
            setScreen(server)
            awaitText("Subject orphan")

            scrollToRow(35)
            clickTag("task-row:35")
            assertEquals(1, countNodes(isSelected()))

            // The slices run in reverse, so slice 17 is example-task-18.
            tapChart(chartPoint(150f, sliceMiddle(17, 35)))

            assertEquals(0, countNodes(isSelected()))
            assertEquals("example-task-18", chartStateDescription())

            tapChart(chartPoint(0f, 0f))

            assertNull(chartStateDescription())
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

    private fun assertChartStaysOnScreenWhileTheListScrolls(count: Int) {
        val tasks = numberedTasks(count)
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(tasks = tasks, slices = tasks.map { slice(it.slug) }),
            )
            setScreen(server)
            awaitText("Subject 1")

            composeRule.onNodeWithTag("epic-chart").assertIsDisplayed()
            composeRule.onNodeWithText("Subject 1").assertIsDisplayed()
            assertRowBelowChart("Subject 1")

            composeRule.onNode(hasScrollAction()).performScrollToNode(hasText("Subject $count"))

            composeRule.onNodeWithText("Subject $count").assertIsDisplayed()
            composeRule.onNodeWithTag("epic-chart").assertIsDisplayed()
            assertRowBelowChart("Subject $count")
        }
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun chartStaysAboveTheListWhileItScrollsInPortrait() {
        assertChartStaysOnScreenWhileTheListScrolls(35)
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun chartStaysAboveTheListWhileItScrollsInPortraitWith150Tasks() {
        assertChartStaysOnScreenWhileTheListScrolls(150)
    }

    @Test
    @Config(qualifiers = "w411dp-h891dp")
    fun chartDrawingIsAtMost420WideAndFillsTheWidthLessItsPaddingInPortrait() {
        val tasks = numberedTasks(35)
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(tasks = tasks, slices = tasks.map { slice(it.slug) }),
            )
            setScreen(server)
            awaitText("Subject 1")

            val size = composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().size
            val drawing = min(size.width, size.height).toFloat()
            with(composeRule.density) {
                assertTrue(drawing <= 420.dp.toPx())
                assertEquals((411.dp - 28.dp).toPx(), drawing, 0.5f)
            }
        }
    }

    @Test
    @Config(qualifiers = "w411dp-h560dp")
    fun aShortPortraitWindowShrinksTheChartSoATaskRowStaysVisible() {
        val tasks = numberedTasks(35)
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(tasks = tasks, slices = tasks.map { slice(it.slug) }),
            )
            setScreen(server)
            awaitText("Subject 1")

            val size = composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().size
            val drawing = min(size.width, size.height).toFloat()
            with(composeRule.density) {
                assertTrue(drawing < (411.dp - 28.dp).toPx())
            }
            composeRule.onNodeWithText("Subject 1").assertIsDisplayed()
            assertRowBelowChart("Subject 1")
        }
    }

    @Test
    @Config(qualifiers = "w891dp-h411dp-land")
    fun chartSitsBesideTheListInLandscape() {
        val tasks = numberedTasks(35)
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(tasks = tasks, slices = tasks.map { slice(it.slug) }),
            )
            setScreen(server)
            awaitText("Subject 1")

            composeRule.onNodeWithTag("epic-chart").assertIsDisplayed()
            composeRule.onNodeWithText("Subject 1").assertIsDisplayed()
        }
    }

    @Test
    fun withNoSlicesTheEmptyLineShowsInPlaceOfTheChart() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body())
            setScreen(server)

            awaitText("No orrery-tracked steps")
            assertEquals(0, countNodes(hasTestTag("epic-chart")))
            composeRule.onNodeWithText("No orrery-tracked steps").assertIsDisplayed()
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
    fun requirementsShowUnderTheHeaderInContractOrderWithTheAcceptedPhase() {
        val requirements = listOf(
            Triple("R1", "Example requirement one.", "check"),
            Triple("R2", "Example requirement two.", "review"),
            Triple("R3", "Example requirement three.", "check"),
        )
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                body(
                    outcome = "accepted",
                    requirements = requirements.map { (id, text, kind) -> requirement(id, text, kind) },
                ),
            )
            setScreen(server)
            awaitText("example-epic")

            assertEquals(1, countNodes(hasText("example-repo · accepted")))
            assertEquals(1, countNodes(hasText("Requirements")))
            val heading = composeRule.onNodeWithText("Requirements").fetchSemanticsNode().boundsInRoot.top
            val tops = requirements.indices.map { index ->
                composeRule.onNodeWithTag("requirement-row:$index", useUnmergedTree = true)
                    .fetchSemanticsNode()
                    .boundsInRoot
                    .top
            }
            assertTrue(heading < tops.first())
            assertTrue(tops.zipWithNext().all { (above, below) -> above < below })
            for ((index, row) in requirements.withIndex()) {
                val (id, text, kind) = row
                val inRow = hasAnyAncestor(hasTestTag("requirement-row:$index"))
                assertEquals(1, countNodes(hasText(id) and inRow))
                assertEquals(1, countNodes(hasText(text) and inRow))
                assertEquals(1, countNodes(hasText(kind) and inRow))
            }
        }
    }

    @Test
    fun anEpicWithNoRequirementsShowsNoRequirementsHeader() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body())
            setScreen(server)
            awaitText("example-epic")

            assertEquals(0, countNodes(hasText("Requirements")))
            assertEquals(0, countNodes(hasTestTag("requirement-row:0")))
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
