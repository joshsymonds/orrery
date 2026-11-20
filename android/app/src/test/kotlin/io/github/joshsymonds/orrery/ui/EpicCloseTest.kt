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
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsCloseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsCloseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseResponse
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
class EpicCloseTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val SLUG = "example-epic"
        const val PATH = "/home/user/example-repo"
        const val OTHER_ID = "example-repo/other-epic"
        const val OTHER_SLUG = "other-epic"
        const val ELSEWHERE_ID = "other-repo/elsewhere-epic"
        const val ELSEWHERE_SLUG = "elsewhere-epic"
        const val ELSEWHERE_PATH = "/home/user/other-repo"
        const val CHANGED = 4_102_444_800L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_RELEASE = "/orrery.v1.Epics/Release"
        const val EPICS_CLOSE = "/orrery.v1.Epics/Close"
        const val GAPS_CALL_LINE = "gaps-call-line"
        const val CLOSE_CONFIRM = "close-confirm"
        const val SUPERSEDED = "close-kind:superseded"
        const val SHIPPED = "close-kind:shipped"
        const val ABANDONED = "close-kind:abandoned"
        const val EPICS_LINE = "close-epics-line"
        const val SENDING = "Sending…"
        const val LOADING_EPICS = "loading the epics…"
        const val NO_OTHER_EPIC = "no other epic in this repository"
        const val COULD_NOT_READ = "The epics could not be read."
        const val NO_ANSWER_READING = "No answer came: it may have gone through. Reading the epic again…"
        const val NO_ANSWER = "No answer came: it may have gone through."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** An ended epic with gaps, or any other ending, with its Report set so the Report section is drawn. */
    private fun body(outcome: String): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug(SLUG)
                    .setPath(PATH)
                    .setOutcome(outcome)
                    .setChanged(CHANGED)
                    .setLive(false)
                    .setSource("orreryd"),
            )
            .setRepository("/home/user/example-repo")
            .setReport(EpicReport.newBuilder().setOutcome(outcome).setWhy("Example why."))
            .build()

    private fun listedEpic(id: String, slug: String, path: String): Epic =
        Epic.newBuilder()
            .setId(id)
            .setRepository("example-repo")
            .setSlug(slug)
            .setPath(path)
            .setOutcome("ended_with_gaps")
            .setSource("orreryd")
            .build()

    /** The epics the Close sheet lists: this epic, another on its path, and one on a different path. */
    private fun listOfEpics(): EpicsListResponse =
        EpicsListResponse.newBuilder()
            .addEpics(listedEpic(EPIC_ID, SLUG, PATH))
            .addEpics(listedEpic(OTHER_ID, OTHER_SLUG, PATH))
            .addEpics(listedEpic(ELSEWHERE_ID, ELSEWHERE_SLUG, ELSEWHERE_PATH))
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
                now = { 1_800_000_000L },
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
            countNodes(hasTestTag(GAPS_CALL_LINE) and hasText(text)) == 1
        }
    }

    private fun releaseRequests(server: FakeOrreryServer): List<RecordedRequest> =
        server.requests.filter { it.rawPath == EPICS_RELEASE }

    private fun closeRequests(server: FakeOrreryServer): List<RecordedRequest> =
        server.requests.filter { it.rawPath == EPICS_CLOSE }

    private fun getCount(server: FakeOrreryServer): Int =
        server.requests.count { it.rawPath == EPICS_GET }

    /** Opens the Release it anyway sheet from the Report's control. */
    private fun openReleaseAnyway() {
        tapOnScreen("report-release-anyway")
        composeRule.awaitUntil { countNodes(hasTestTag("anyway-confirm")) > 0 }
    }

    /** Opens the Close as… sheet from the Report's control, and waits until the epics are listed. */
    private fun openClose() {
        tapOnScreen("report-close")
        awaitTag("close-title")
    }

    /** Opens the Close as… sheet and waits until its epics have been read. */
    private fun openCloseWithList() {
        openClose()
        awaitTag("close-by:$OTHER_ID")
    }

    @Test
    fun anEndedWithGapsEpicDrawsReleaseItAnywayAndCloseAs() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            setScreen(server)
            awaitLoaded()

            awaitTag("report-release-anyway")
            composeRule.onNodeWithTag("report-release-anyway").assertTextEquals("Release it anyway")
            composeRule.onNodeWithTag("report-close").assertTextEquals("Close as…")
        }
    }

    @Test
    fun aClosedEpicDrawsNeitherControl() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "closed_abandoned"))
            setScreen(server)
            awaitLoaded()
            awaitTag("report-sentence")

            assertEquals(0, countNodes(hasTestTag("report-release-anyway")))
            assertEquals(0, countNodes(hasTestTag("report-close")))
        }
    }

    @Test
    fun releaseItAnywaySheetSaysWhatItDoesAndKeepSendsNothing() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            setScreen(server)
            awaitLoaded()

            openReleaseAnyway()
            composeRule.onNodeWithTag("anyway-title", useUnmergedTree = true)
                .assertTextEquals("Release $SLUG anyway?")
            composeRule.onNodeWithTag("anyway-line", useUnmergedTree = true)
                .assertTextEquals("Its gaps stay, and its release steps run.")
            composeRule.onNodeWithTag("anyway-confirm").assertTextEquals("Release it anyway")
            composeRule.onNodeWithTag("anyway-keep").assertTextEquals("Keep it")

            tapInSheet("anyway-keep")
            composeRule.awaitNone(hasTestTag("anyway-title"))
            assertEquals(0, releaseRequests(server).size)
        }
    }

    @Test
    fun releaseItAnywayConfirmSendsOneReleaseWithNoUndoAndNothingMoreWhileItSends() {
        val landing = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                serve(server, body(outcome = "ended_with_gaps"))
                server.route(EPICS_RELEASE) {
                    landing.await()
                    Reply.Message(EpicsReleaseResponse.getDefaultInstance(), 0L)
                }
                setScreen(server)
                awaitLoaded()

                openReleaseAnyway()
                tapInSheet("anyway-confirm")
                composeRule.awaitUntil("the release to reach the server") { releaseRequests(server).isNotEmpty() }

                composeRule.onNodeWithTag("report-release-anyway").assertIsNotEnabled()
                composeRule.onNodeWithTag("report-close").assertIsNotEnabled()
                composeRule.onNodeWithTag(GAPS_CALL_LINE, useUnmergedTree = true).assertTextEquals(SENDING)
                assertEquals(1, releaseRequests(server).size)

                landing.countDown()
                composeRule.awaitNone(hasTestTag(GAPS_CALL_LINE))
                composeRule.onNodeWithTag("report-release-anyway").assertIsEnabled()

                assertEquals(1, releaseRequests(server).size)
                val sent = EpicsReleaseRequest.parseFrom(releaseRequests(server).single().body)
                assertEquals(EPIC_ID, sent.id)
                assertEquals(emptyList<Int>(), sent.undoNoneList)
                assertEquals(2, getCount(server))
            }
        } finally {
            landing.countDown()
        }
    }

    @Test
    fun aRefusedReleaseItAnywayShowsItsLineAndIsEnabledAgain() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.fail(EPICS_RELEASE, Code.FAILED_PRECONDITION, "Synthetic refusal reason")
            setScreen(server)
            awaitLoaded()

            openReleaseAnyway()
            tapInSheet("anyway-confirm")

            awaitCallLine("Refused: Synthetic refusal reason")
            composeRule.onNodeWithTag("report-release-anyway").assertIsEnabled()
            assertEquals(1, releaseRequests(server).size)
            assertEquals(1, getCount(server))
        }
    }

    @Test
    fun closeAsListsTheEpicsOnThisPathButItself() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            composeRule.onNodeWithTag("close-by:$OTHER_ID").assertTextEquals(OTHER_SLUG)
            assertEquals(0, countNodes(hasTestTag("close-by:$EPIC_ID")))
            assertEquals(0, countNodes(hasTestTag("close-by:$ELSEWHERE_ID")))
        }
    }

    @Test
    fun closeAsConfirmIsDisabledUntilAKindIsPickedAndForSupersededAnEpic() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertIsNotEnabled()
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertTextEquals("Close as…")

            tapInSheet(SUPERSEDED)
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertIsNotEnabled()

            tapInSheet("close-by:$OTHER_ID")
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertIsEnabled()
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertTextEquals("Close as superseded")
        }
    }

    @Test
    fun closeAsSupersededSendsOneCloseWithTheEpicPicked() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            server.respond(EPICS_CLOSE, EpicsCloseResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            tapInSheet("close-by:$OTHER_ID")
            tapInSheet(CLOSE_CONFIRM)
            composeRule.awaitUntil("the close to reach the server") { closeRequests(server).isNotEmpty() }
            composeRule.awaitNone(hasTestTag(GAPS_CALL_LINE))

            assertEquals(1, closeRequests(server).size)
            val sent = EpicsCloseRequest.parseFrom(closeRequests(server).single().body)
            assertEquals(EPIC_ID, sent.id)
            assertEquals("superseded", sent.kind)
            assertEquals(OTHER_ID, sent.by)
        }
    }

    @Test
    fun closeAsShippedSendsOneCloseWithNoBy() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            server.respond(EPICS_CLOSE, EpicsCloseResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            tapInSheet(SHIPPED)
            tapInSheet(CLOSE_CONFIRM)
            composeRule.awaitUntil("the close to reach the server") { closeRequests(server).isNotEmpty() }
            composeRule.awaitNone(hasTestTag(GAPS_CALL_LINE))

            assertEquals(1, closeRequests(server).size)
            val sent = EpicsCloseRequest.parseFrom(closeRequests(server).single().body)
            assertEquals(EPIC_ID, sent.id)
            assertEquals("shipped", sent.kind)
            assertEquals("", sent.by)
        }
    }

    @Test
    fun closeAsAbandonedSendsOneCloseWithNoBy() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            server.respond(EPICS_CLOSE, EpicsCloseResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            tapInSheet(ABANDONED)
            tapInSheet(CLOSE_CONFIRM)
            composeRule.awaitUntil("the close to reach the server") { closeRequests(server).isNotEmpty() }
            composeRule.awaitNone(hasTestTag(GAPS_CALL_LINE))

            assertEquals(1, closeRequests(server).size)
            val sent = EpicsCloseRequest.parseFrom(closeRequests(server).single().body)
            assertEquals(EPIC_ID, sent.id)
            assertEquals("abandoned", sent.kind)
            assertEquals("", sent.by)
        }
    }

    @Test
    fun pickingSupersededThenShippedSendsAShippedCloseWithNoBy() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            server.respond(EPICS_CLOSE, EpicsCloseResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            tapInSheet("close-by:$OTHER_ID")
            tapInSheet(SHIPPED)
            tapInSheet(CLOSE_CONFIRM)
            composeRule.awaitUntil("the close to reach the server") { closeRequests(server).isNotEmpty() }
            composeRule.awaitNone(hasTestTag(GAPS_CALL_LINE))

            assertEquals(1, closeRequests(server).size)
            val sent = EpicsCloseRequest.parseFrom(closeRequests(server).single().body)
            assertEquals("shipped", sent.kind)
            assertEquals("", sent.by)
        }
    }

    @Test
    fun aReopenedCloseSheetHasNoPick() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(EPICS_LIST, listOfEpics())
            setScreen(server)
            awaitLoaded()

            openCloseWithList()
            tapInSheet(SHIPPED)
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertIsEnabled()
            tapInSheet("close-keep")
            composeRule.awaitNone(hasTestTag("close-title"))

            openCloseWithList()
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertIsNotEnabled()
            composeRule.onNodeWithTag(CLOSE_CONFIRM).assertTextEquals("Close as…")
            assertEquals(0, closeRequests(server).size)
        }
    }

    @Test
    fun withNoOtherEpicOnThePathSupersededIsDisabledWithItsWords() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder().addEpics(listedEpic(EPIC_ID, SLUG, PATH))
                    .addEpics(listedEpic(ELSEWHERE_ID, ELSEWHERE_SLUG, ELSEWHERE_PATH)).build(),
            )
            setScreen(server)
            awaitLoaded()

            openClose()
            composeRule.awaitUntil("the epics line to read $NO_OTHER_EPIC") {
                countNodes(hasTestTag(EPICS_LINE) and hasText(NO_OTHER_EPIC)) == 1
            }
            composeRule.onNodeWithTag(SUPERSEDED).assertIsNotEnabled()
            assertEquals(0, countNodes(hasTestTag("close-by:$ELSEWHERE_ID")))
        }
    }

    @Test
    fun aFailedEpicsReadDisablesSupersededWithItsWords() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "ended_with_gaps"))
            server.fail(EPICS_LIST, Code.INTERNAL_ERROR, "Synthetic list failure")
            setScreen(server)
            awaitLoaded()

            openClose()
            composeRule.awaitUntil("the epics line to read $COULD_NOT_READ") {
                countNodes(hasTestTag(EPICS_LINE) and hasText(COULD_NOT_READ)) == 1
            }
            composeRule.onNodeWithTag(SUPERSEDED).assertIsNotEnabled()
        }
    }

    @Test
    fun whileTheEpicsAreReadTheSupersededRowReadsLoading() {
        val listing = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                serve(server, body(outcome = "ended_with_gaps"))
                server.route(EPICS_LIST) {
                    listing.await()
                    Reply.Message(listOfEpics(), 0L)
                }
                setScreen(server)
                awaitLoaded()

                openClose()
                composeRule.awaitUntil("the epics line to read $LOADING_EPICS") {
                    countNodes(hasTestTag(EPICS_LINE) and hasText(LOADING_EPICS)) == 1
                }
                composeRule.onNodeWithTag(SUPERSEDED).assertIsNotEnabled()

                listing.countDown()
                awaitTag("close-by:$OTHER_ID")
                composeRule.awaitNone(hasTestTag(EPICS_LINE))
                composeRule.onNodeWithTag(SUPERSEDED).assertIsEnabled()
            }
        } finally {
            listing.countDown()
        }
    }

    @Test
    fun aDroppedCloseShowsNoAnswerRereadsOnceAndStaysDisabledUntilTheReadLands() {
        val reading = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val gets = AtomicInteger()
                val epic = body(outcome = "ended_with_gaps")
                serve(server, epic)
                server.route(EPICS_GET) {
                    if (gets.incrementAndGet() > 1) reading.await()
                    Reply.Message(epic, 0L)
                }
                server.respond(EPICS_LIST, listOfEpics())
                server.drop(EPICS_CLOSE)
                setScreen(server)
                awaitLoaded()

                openCloseWithList()
                tapInSheet("close-by:$OTHER_ID")
                tapInSheet(CLOSE_CONFIRM)

                awaitCallLine(NO_ANSWER_READING)
                composeRule.awaitUntil("the second read of the epic to reach the server") { getCount(server) == 2 }
                composeRule.onNodeWithTag("report-close").assertIsNotEnabled()
                composeRule.onNodeWithTag("report-release-anyway").assertIsNotEnabled()

                reading.countDown()
                awaitCallLine(NO_ANSWER)
                composeRule.onNodeWithTag("report-close").assertIsEnabled()
                assertEquals(1, closeRequests(server).size)
                assertEquals(2, getCount(server))
            }
        } finally {
            reading.countDown()
        }
    }
}
