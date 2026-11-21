package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicReportTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val SLUG = "example-epic"
        const val NOW = 1_800_000_000L
        const val CHANGED = 4_102_444_800L
        const val SUMMARY_BEFORE = "Example summary before the reload."
        const val SUMMARY_AFTER = "Example summary after the reload."
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_PAUSE = "/orrery.v1.Epics/Pause"

        // Enough requirements that the list is taller than the screen, so a section can sit at its top.
        const val TALL = 40
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun requirement(id: String): EpicRequirement =
        EpicRequirement.newBuilder()
            .setId(id)
            .setText("Example requirement text.")
            .setEvidence("example evidence")
            .setKind("test")
            .build()

    private fun step(position: Long, state: String): EpicReleaseProof =
        EpicReleaseProof.newBuilder()
            .setPosition(position)
            .setStep("example release step")
            .setUndo("none")
            .setState(state)
            .build()

    private fun report(
        outcome: String,
        shipped: List<String> = emptyList(),
        notShipped: List<String> = emptyList(),
        why: String = "",
    ): EpicReport =
        EpicReport.newBuilder()
            .setOutcome(outcome)
            .addAllShipped(shipped)
            .addAllNotShipped(notShipped)
            .setWhy(why)
            .build()

    private fun body(
        outcome: String,
        summary: String = SUMMARY_BEFORE,
        requirements: Int = 1,
        releaseSteps: List<EpicReleaseProof> = emptyList(),
        report: EpicReport? = null,
        supersededBy: String = "",
        live: Boolean = true,
        paused: Boolean = false,
    ): EpicsGetResponse {
        val builder = EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug(SLUG)
                    .setOutcome(outcome)
                    .setChanged(CHANGED)
                    .setLive(live)
                    .setSource("orreryd"),
            )
            .setRepository("/home/user/example-repo")
            .setSummary(summary)
            .setPaused(paused)
            .setSupersededBy(supersededBy)
            .addAllReleaseSteps(releaseSteps)
        (1..requirements).forEach { builder.addRequirements(requirement("R$it")) }
        if (report != null) builder.report = report
        return builder.build()
    }

    /** A live reviewing epic with its Report set, so More offers Pause and the Report is drawn. */
    private fun pausableEnded(summary: String): EpicsGetResponse =
        body(
            outcome = "reviewing",
            summary = summary,
            requirements = TALL,
            report = report(outcome = "reviewing", shipped = listOf("R1")),
        )

    /** Taps [tag] through its click action, as a tap inside a sheet runs under Robolectric. */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Serves the epic and its empty history, the two calls a load makes. */
    private fun serve(server: FakeOrreryServer, epic: EpicsGetResponse) {
        server.respond(EPICS_GET, epic)
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer, listState: LazyListState = LazyListState(), openAt: EpicOpening? = null) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
                onOpenContract = {},
                onTalk = {},
                listedSteerer = { null },
                listState = listState,
                openAt = openAt,
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) > 0 }
    }

    /** Waits until the epic has loaded: the now section is drawn only once it has. */
    private fun awaitLoaded() = awaitTag("epic-section:now")

    private fun scrollTo(tag: String) {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag(tag))
    }

    /** The index of the item of section [name] in the list, read once the section is on screen. */
    private fun sectionIndex(listState: LazyListState, name: String): Int {
        scrollTo("epic-section:$name")
        return composeRule.runOnIdle {
            val visible = listState.layoutInfo.visibleItemsInfo.firstOrNull { it.key == "section:$name" }
            checkNotNull(visible) { "section $name is not on screen after it was scrolled to" }.index
        }
    }

    /** The key of the list's first visible item, read on the idle screen. */
    private fun firstVisibleKey(listState: LazyListState): Any? = composeRule.runOnIdle {
        listState.layoutInfo.visibleItemsInfo.firstOrNull()?.key
    }

    /** The keys of the items on screen, read on the idle screen, top first. */
    private fun visibleKeys(listState: LazyListState): List<Any?> = composeRule.runOnIdle {
        listState.layoutInfo.visibleItemsInfo.map { it.key }
    }

    /** Asserts the text of the node tagged [tag], a text node of its own. */
    private fun assertTextAt(tag: String, text: String) {
        scrollTo(tag)
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).assertTextEquals(text)
    }

    @Test
    fun theReportIsDrawnAfterTheReleaseWithItsSentenceWhyShippedNotShippedAndSupersededBy() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "closed_superseded",
                    releaseSteps = listOf(step(1, "passed")),
                    report = report(
                        outcome = "closed_superseded",
                        shipped = listOf("R1"),
                        notShipped = listOf("R2"),
                        why = "Example why it shipped what it did.",
                    ),
                    supersededBy = "example-repo/example-next",
                ),
            )
            setScreen(server, listState)
            awaitLoaded()

            assertTextAt("report-sentence", "Closed as superseded.")
            assertTextAt("report-why", "Example why it shipped what it did.")
            assertTextAt("report-shipped", "shipped R1")
            assertTextAt("report-not-shipped", "not shipped R2")
            assertTextAt("report-replaced", "Superseded by example-next")
            assertTrue(sectionIndex(listState, "report") > sectionIndex(listState, "release"))
        }
    }

    @Test
    fun theShippedLineReadsNoneWhenTheReportShippedNothing() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "closed_abandoned",
                    report = report(outcome = "closed_abandoned", notShipped = listOf("R1")),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertTextAt("report-shipped", "shipped none")
            assertEquals(0, countNodes(hasTestTag("report-why")))
            assertEquals(0, countNodes(hasTestTag("report-replaced")))
        }
    }

    @Test
    fun aReleasingEpicWithNoReportDrawsNoReport() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(outcome = "releasing", releaseSteps = listOf(step(1, "running"))),
            )
            setScreen(server)
            awaitTag("epic-section:release")

            assertEquals(0, countNodes(hasTestTag("epic-section:report")))
        }
    }

    @Test
    fun aShippedEpicOpenedWithNoOpenAtScrollsToItsReport() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "released",
                    requirements = TALL,
                    releaseSteps = listOf(step(1, "passed")),
                    report = report(outcome = "released", shipped = listOf("R1")),
                ),
            )
            setScreen(server, listState)
            awaitTag("epic-section:report")

            composeRule.awaitUntil { visibleKeys(listState).contains("section:report") }
            assertEquals("section:report", visibleKeys(listState).last())
        }
    }

    @Test
    fun anEpicOpenedAtNowOpensAtNowNotAtItsReport() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "released",
                    requirements = TALL,
                    releaseSteps = listOf(step(1, "passed")),
                    report = report(outcome = "released", shipped = listOf("R1")),
                ),
            )
            setScreen(server, listState, openAt = EpicOpening("now"))
            awaitLoaded()

            composeRule.awaitUntil { firstVisibleKey(listState) == "section:now" }
            assertEquals("section:now", firstVisibleKey(listState))
        }
    }

    @Test
    fun aReloadAfterTheOwnerScrolledAwayDoesNotScrollBackToTheReport() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, pausableEnded(SUMMARY_BEFORE))
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            setScreen(server, listState)
            awaitTag("epic-section:report")
            val reportIndex = sectionIndex(listState, "report")

            scrollTo("epic-section:now")
            val awayIndex = composeRule.runOnIdle { listState.firstVisibleItemIndex }
            assertNotEquals(reportIndex, awayIndex)

            server.respond(EPICS_GET, pausableEnded(SUMMARY_AFTER))
            tap("epic-more")
            tap("epic-sheet-pause")
            composeRule.awaitUntil { countNodes(hasTestTag("epic-summary") and hasText(SUMMARY_AFTER)) > 0 }

            assertEquals(awayIndex, composeRule.runOnIdle { listState.firstVisibleItemIndex })
        }
    }
}
