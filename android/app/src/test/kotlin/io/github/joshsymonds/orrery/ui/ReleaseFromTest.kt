package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsResumeResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ReleaseFromTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val SLUG = "example-epic"
        const val NOW = 1_800_000_000L
        const val CHANGED = 4_102_444_800L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_RELEASE = "/orrery.v1.Epics/Release"
        const val EPICS_RESUME = "/orrery.v1.Epics/Resume"
        const val RELEASE_FROM = "release-from"
        const val CALL_LINE = "release-call-line"
        const val SENDING = "Sending…"
        const val NO_ANSWER_READING = "No answer came: it may have gone through. Reading the epic again…"
        const val NO_ANSWER = "No answer came: it may have gone through."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun releaseStep(
        position: Long,
        state: String,
        step: String = "Example release step $position.",
        undo: String = "none",
    ): EpicReleaseProof =
        EpicReleaseProof.newBuilder()
            .setPosition(position)
            .setStep(step)
            .setTarget("example-target")
            .setUndo(undo)
            .setState(state)
            .build()

    private fun body(outcome: String, steps: List<EpicReleaseProof>, paused: Boolean = false): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug(SLUG)
                    .setOutcome(outcome)
                    .setChanged(CHANGED)
                    .setLive(true)
                    .setSource("orreryd"),
            )
            .setRepository("/home/user/example-repo")
            .setContract(EpicContract.newBuilder().setReleaseThen("Example then."))
            .setPaused(paused)
            .addAllReleaseSteps(steps)
            .build()

    /** Serves the epic and its empty history, the two calls a load makes. */
    private fun serve(server: FakeOrreryServer, epic: EpicsGetResponse) {
        server.respond(EPICS_GET, epic)
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer, listState: LazyListState = LazyListState()) {
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
                openAt = null,
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countNodes(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) > 0 }
    }

    /** Waits until the epic has loaded: the now section is drawn only once it has. */
    private fun awaitLoaded() = awaitTag("epic-section:now")

    private fun scrollTo(tag: String) {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag(tag))
    }

    /** Taps the control [tag] on the screen itself. */
    private fun tapOnScreen(tag: String) {
        scrollTo(tag)
        composeRule.onNodeWithTag(tag).performClick()
    }

    /**
     * Taps [tag] inside a sheet through its click action. A touch injected into the sheet's window does not reach the
     * sheet's content under Robolectric, so the tap is the accessibility click that the same tap runs.
     */
    private fun tapInSheet(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Waits until the call line reads [text]. */
    private fun awaitCallLine(text: String) {
        composeRule.awaitUntil("the call line to read $text") {
            countNodes(hasTestTag(CALL_LINE) and hasText(text)) == 1
        }
    }

    private fun releaseRequests(server: FakeOrreryServer): List<RecordedRequest> =
        server.requests.filter { it.rawPath == EPICS_RELEASE }

    private fun getCount(server: FakeOrreryServer): Int =
        server.requests.count { it.rawPath == EPICS_GET }

    /** Serves the epic reads in order, the read at [held] (1-based) waiting for [gate] before it is answered. */
    private fun routeReads(server: FakeOrreryServer, gate: CountDownLatch, held: Int, vararg reads: EpicsGetResponse) {
        val count = AtomicInteger()
        server.route(EPICS_GET) {
            val read = count.incrementAndGet()
            if (read == held) gate.await()
            Reply.Message(reads[read - 1], 0L)
        }
    }

    /** Resumes the paused epic from its More sheet, which reads it again, and waits for read [reads] to arrive. */
    private fun resumeAndAwaitRead(server: FakeOrreryServer, reads: Int) {
        composeRule.onNodeWithTag("epic-more").performClick()
        awaitTag("epic-sheet-resume")
        tapInSheet("epic-sheet-resume")
        composeRule.awaitNone(hasTestTag("epic-more-title"))
        composeRule.awaitUntil("the epic to be read $reads times") { getCount(server) == reads }
    }

    @Test
    fun releaseFromIsOfferedOnlyForTheFirstUnpassedFailedStepWithNoUndoOfAStoppedEpic() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "stopped",
                    steps = listOf(
                        releaseStep(1, "passed", undo = "rollback"),
                        releaseStep(2, "failed"),
                        releaseStep(3, "failed"),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countNodes(hasTestTag(RELEASE_FROM)))
            assertEquals(1, countIn("release-step:2", hasTestTag(RELEASE_FROM)))
            scrollTo(RELEASE_FROM)
            composeRule.onNodeWithTag(RELEASE_FROM).assertTextEquals("Release from step 2")
        }
    }

    @Test
    fun releaseFromIsNotOfferedForAFailedStepThatHasAnUndo() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(outcome = "stopped", steps = listOf(releaseStep(1, "failed", undo = "rollback"))),
            )
            setScreen(server)
            awaitLoaded()
            awaitTag("release-state:1")

            assertEquals(0, countNodes(hasTestTag(RELEASE_FROM)))
        }
    }

    @Test
    fun releaseFromIsNotOfferedOutsideAStoppedEpic() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "releasing", steps = listOf(releaseStep(1, "failed"))))
            setScreen(server)
            awaitLoaded()
            awaitTag("release-state:1")

            assertEquals(0, countNodes(hasTestTag(RELEASE_FROM)))
        }
    }

    @Test
    fun keepShowsTheSheetsWordsAndSendsNothing() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(outcome = "stopped", steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed", step = "Example step two."))),
            )
            setScreen(server)
            awaitLoaded()

            tapOnScreen(RELEASE_FROM)
            composeRule.awaitUntil { countNodes(hasTestTag("release-from-title")) > 0 }
            composeRule.onNodeWithTag("release-from-title", useUnmergedTree = true)
                .assertTextEquals("Release from step 2?")
            composeRule.onNodeWithTag("release-from-line", useUnmergedTree = true)
                .assertTextEquals("Step 2, Example step two., cannot be undone.")
            composeRule.onNodeWithTag("release-from-confirm").assertTextEquals("Release from step 2")
            composeRule.onNodeWithTag("release-from-keep").assertTextEquals("Keep it stopped")

            tapInSheet("release-from-keep")
            composeRule.awaitNone(hasTestTag("release-from-title"))
            assertEquals(0, releaseRequests(server).size)
        }
    }

    @Test
    fun confirmSendsExactlyOneReleaseWithItsIdAndUndoNoneAndNothingMoreWhileItSends() {
        val landing = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                serve(
                    server,
                    body(outcome = "stopped", steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed"))),
                )
                server.route(EPICS_RELEASE) {
                    landing.await()
                    Reply.Message(EpicsReleaseResponse.getDefaultInstance(), 0L)
                }
                setScreen(server)
                awaitLoaded()

                tapOnScreen(RELEASE_FROM)
                composeRule.awaitUntil { countNodes(hasTestTag("release-from-confirm")) > 0 }
                tapInSheet("release-from-confirm")
                composeRule.awaitUntil("the release to reach the server") { releaseRequests(server).isNotEmpty() }

                composeRule.onNodeWithTag(RELEASE_FROM).assertIsNotEnabled()
                composeRule.onNodeWithTag(CALL_LINE, useUnmergedTree = true).assertTextEquals(SENDING)
                assertEquals(1, releaseRequests(server).size)

                landing.countDown()
                composeRule.awaitNone(hasTestTag(CALL_LINE))
                composeRule.onNodeWithTag(RELEASE_FROM).assertIsEnabled()

                val request = releaseRequests(server).single()
                val sent = EpicsReleaseRequest.parseFrom(request.body)
                assertEquals(EPIC_ID, sent.id)
                assertEquals(listOf(2), sent.undoNoneList)
                assertEquals(2, getCount(server))
            }
        } finally {
            landing.countDown()
        }
    }

    @Test
    fun aRefusalShowsItsLineAndSendsNothingMore() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(outcome = "stopped", steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed"))),
            )
            server.fail(EPICS_RELEASE, Code.FAILED_PRECONDITION, "Synthetic refusal reason")
            setScreen(server)
            awaitLoaded()

            tapOnScreen(RELEASE_FROM)
            composeRule.awaitUntil { countNodes(hasTestTag("release-from-confirm")) > 0 }
            tapInSheet("release-from-confirm")

            awaitCallLine("Refused: Synthetic refusal reason")
            composeRule.onNodeWithTag(RELEASE_FROM).assertIsEnabled()
            assertEquals(1, releaseRequests(server).size)
            assertEquals(1, getCount(server))
        }
    }

    @Test
    fun aDroppedCallShowsNoAnswerRereadsOnceAndStaysDisabledUntilTheReadLands() {
        val reading = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val gets = AtomicInteger()
                val epic = body(outcome = "stopped", steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed")))
                serve(server, epic)
                server.route(EPICS_GET) {
                    if (gets.incrementAndGet() > 1) reading.await()
                    Reply.Message(epic, 0L)
                }
                server.drop(EPICS_RELEASE)
                setScreen(server)
                awaitLoaded()

                tapOnScreen(RELEASE_FROM)
                composeRule.awaitUntil { countNodes(hasTestTag("release-from-confirm")) > 0 }
                tapInSheet("release-from-confirm")

                awaitCallLine(NO_ANSWER_READING)
                composeRule.awaitUntil("the second read of the epic to reach the server") { getCount(server) == 2 }
                composeRule.onNodeWithTag(RELEASE_FROM).assertIsNotEnabled()
                composeRule.onNodeWithTag(CALL_LINE, useUnmergedTree = true).assertTextEquals(NO_ANSWER_READING)

                reading.countDown()
                awaitCallLine(NO_ANSWER)
                composeRule.onNodeWithTag(RELEASE_FROM).assertIsEnabled()
                assertEquals(1, releaseRequests(server).size)
                assertEquals(2, getCount(server))
            }
        } finally {
            reading.countDown()
        }
    }

    @Test
    fun anOpenStepTwoSheetClosesWithNoReleaseWhenARereadOffersStepThree() {
        val reading = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val first = body(
                    outcome = "stopped",
                    steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed"), releaseStep(3, "failed")),
                    paused = true,
                )
                val reread = body(
                    outcome = "stopped",
                    steps = listOf(releaseStep(1, "passed"), releaseStep(2, "passed"), releaseStep(3, "failed")),
                    paused = true,
                )
                serve(server, first)
                routeReads(server, reading, 2, first, reread, first)
                server.respond(EPICS_RESUME, EpicsResumeResponse.getDefaultInstance())
                setScreen(server)
                awaitLoaded()

                resumeAndAwaitRead(server, 2)
                tapOnScreen(RELEASE_FROM)
                awaitTag("release-from-title")
                reading.countDown()

                composeRule.awaitNone(hasTestTag("release-from-title"))
                assertEquals(1, countIn("release-step:3", hasTestTag(RELEASE_FROM)))
                assertEquals(0, releaseRequests(server).size)

                // Step 2 offered again is at rest: the confirm closed when step 3 was offered, so it does not reopen.
                resumeAndAwaitRead(server, 3)
                composeRule.awaitUntil { countIn("release-step:2", hasTestTag(RELEASE_FROM)) == 1 }
                assertEquals(0, countNodes(hasTestTag("release-from-title")))
                assertEquals(0, releaseRequests(server).size)
            }
        } finally {
            reading.countDown()
        }
    }

    @Test
    fun anOpenStepTwoSheetClosesWithNoReleaseWhenARereadOffersNoStepAndItsControlIsAtRestWhenOfferedAgain() {
        val reading = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val offered = body(
                    outcome = "stopped",
                    steps = listOf(releaseStep(1, "passed"), releaseStep(2, "failed"), releaseStep(3, "failed")),
                    paused = true,
                )
                val none = body(
                    outcome = "stopped",
                    steps = listOf(
                        releaseStep(1, "passed"),
                        releaseStep(2, "failed", undo = "Example undo."),
                        releaseStep(3, "failed"),
                    ),
                    paused = true,
                )
                serve(server, offered)
                routeReads(server, reading, 2, offered, none, offered)
                server.respond(EPICS_RESUME, EpicsResumeResponse.getDefaultInstance())
                setScreen(server)
                awaitLoaded()

                resumeAndAwaitRead(server, 2)
                tapOnScreen(RELEASE_FROM)
                awaitTag("release-from-title")
                reading.countDown()

                composeRule.awaitNone(hasTestTag("release-from-title"))
                assertEquals(0, countNodes(hasTestTag(RELEASE_FROM)))

                resumeAndAwaitRead(server, 3)
                awaitTag(RELEASE_FROM)
                composeRule.onNodeWithTag(RELEASE_FROM).assertIsEnabled()
                assertEquals(0, countNodes(hasTestTag("release-from-title")))
                assertEquals(0, releaseRequests(server).size)

                tapOnScreen(RELEASE_FROM)
                awaitTag("release-from-title")
                assertEquals(0, releaseRequests(server).size)
            }
        } finally {
            reading.countDown()
        }
    }
}
