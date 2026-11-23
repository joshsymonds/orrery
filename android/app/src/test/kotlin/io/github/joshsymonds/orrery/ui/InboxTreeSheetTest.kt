package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTouchInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The long-press sheet of a conversation or an epic on the inbox: what it offers, and the calls each item makes with its
// row's kind and id, from synthetic rows at a synthetic clock, checked against the requests the fake server recorded.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxTreeSheetTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HOST = "example-host"
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val TMUX_PANE = "%1"
        const val OTHER_PANE = "%2"
        const val TMUX_SESSION = "00000000000000000000000000000001"
        const val OTHER_SESSION = "00000000000000000000000000000002"
        const val HEADLESS_SESSION = "00000000000000000000000000000005"
        const val EPIC_ID = "example-repo/example-epic"
        const val EPIC_REPOSITORY = "example-repo"
        const val EPIC_SLUG = "example-epic"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val MARK_SEEN = "/orrery.v1.Panes/MarkSeen"
        const val CONTROL_SEEN = "/orrery.v1.Control/Seen"
        const val EPICS_ARCHIVE = "/orrery.v1.Epics/Archive"
        const val EPICS_UNARCHIVE = "/orrery.v1.Epics/Unarchive"
        const val MARK_SEEN_FAILED = "could not mark seen"
        const val ARCHIVE_FAILED = "could not change archive"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun sessionTag(sessionId: String): String = "convo:session:$sessionId"

    private fun epicTag(): String = "hung:epic:$EPIC_ID"

    private fun paneOf(
        headless: Boolean = false,
        orreryId: String = TMUX_SESSION,
        archived: Boolean = false,
        newestReply: Long = 0L,
        seen: Long = NOW,
    ) = Pane(
        id = TMUX_PANE,
        host = HOST,
        session = "",
        window = 0,
        pane = 0,
        cwd = ALPHA,
        command = "",
        state = "working",
        summary = "",
        label = "label-$TMUX_PANE",
        changed = NOW - 60,
        seen = seen,
        question = "",
        questionHash = "",
        newestReply = newestReply,
        headless = headless,
        orreryId = orreryId,
        archived = archived,
    )

    private fun epicOf(archived: Boolean) = EpicRow(
        id = EPIC_ID,
        repository = EPIC_REPOSITORY,
        slug = EPIC_SLUG,
        outcome = "",
        changed = NOW - 30,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        archived = archived,
    )

    private fun pane(
        id: String,
        cwd: String,
        changed: Long,
        seen: Long,
        newestReply: Long,
        orreryId: String,
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(changed)
        .setSeen(seen)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(newestReply)
        .setOrreryId(orreryId)
        .build()

    private fun headless(id: String, changed: Long, seen: Long): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir(ALPHA)
        .setLabel("synthetic-working")
        .setState("working")
        .setChanged(changed)
        .setSeen(seen)
        .setSummary("Synthetic working session.")
        .setNewestReply(changed)
        .build()

    private fun liveEpic(steering: String): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setProblem("")
        .setSteering(steering)
        .build()

    /** The host and the empty lists, and a landed answer to each write a sheet item makes. */
    private fun serveDefaults(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(MARK_SEEN, MarkSeenResponse.getDefaultInstance())
        server.respond(CONTROL_SEEN, SeenResponse.getDefaultInstance())
        server.respond(EPICS_ARCHIVE, EpicsArchiveResponse.getDefaultInstance())
        server.respond(EPICS_UNARCHIVE, EpicsUnarchiveResponse.getDefaultInstance())
    }

    private fun servePanes(server: FakeOrreryServer, vararg panes: PaneProto) {
        serveDefaults(server)
        server.respond(PANES_LIST, PanesListResponse.newBuilder().addAllPanes(panes.toList()).build())
    }

    private fun setScreen(server: FakeOrreryServer) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
                onOpenPane = {},
                onOpenPaneId = {},
                onStartedSession = {},
                onOpenSettings = {},
                now = { NOW },
            )
        }
    }

    private fun countNodes(tag: String): Int =
        composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(tag) > 0 }
    }

    private fun requestsTo(server: FakeOrreryServer, path: String): Int = server.requests.count { it.rawPath == path }

    /** Opens the sheet of the row [tag] with a long press on it. */
    private fun longPress(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performTouchInput { longClick() }
    }

    /**
     * Taps a sheet item through its click action. A touch injected into the sheet's window does not reach the sheet's
     * content under Robolectric, so a tap inside the sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tapSheet(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
    }

    @Test
    fun anUnseenPaneOffersMarkSeenThenArchive() {
        val row = SheetRow.Conversation(paneOf(newestReply = NOW - 30, seen = NOW - 600))

        assertEquals(listOf(SheetItem.MARK_SEEN, SheetItem.ARCHIVE), sheetItemsOf(row))
    }

    @Test
    fun aSeenPaneOffersArchiveAndNoMarkSeen() {
        val row = SheetRow.Conversation(paneOf(newestReply = NOW - 600, seen = NOW - 30))

        assertEquals(listOf(SheetItem.ARCHIVE), sheetItemsOf(row))
    }

    @Test
    fun aPaneWithNoOrreryIdOffersNoArchive() {
        val row = SheetRow.Conversation(paneOf(orreryId = "", newestReply = NOW - 30, seen = NOW - 600))

        assertEquals(listOf(SheetItem.MARK_SEEN), sheetItemsOf(row))
    }

    @Test
    fun anArchivedPaneOffersUnarchiveAndNoArchive() {
        val row = SheetRow.Conversation(paneOf(archived = true, newestReply = NOW - 600, seen = NOW - 30))

        assertEquals(listOf(SheetItem.UNARCHIVE), sheetItemsOf(row))
    }

    @Test
    fun anEpicOffersArchiveOnly() {
        assertEquals(listOf(SheetItem.ARCHIVE), sheetItemsOf(SheetRow.Epic(epicOf(archived = false))))
    }

    @Test
    fun anArchivedEpicOffersUnarchiveOnly() {
        assertEquals(listOf(SheetItem.UNARCHIVE), sheetItemsOf(SheetRow.Epic(epicOf(archived = true))))
    }

    @Test
    fun aLongPressOnAnUnseenPaneOpensASheetHeadedByItsTitleWithMarkSeenAndArchive() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 120, NOW - 60, TMUX_SESSION))
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))

            longPress(convoTag(TMUX_PANE))

            awaitTag("sheet-title")
            composeRule.onNodeWithTag("sheet-title").assertTextEquals("label-$TMUX_PANE")
            assertEquals(1, countNodes("sheet:MARK_SEEN"))
            assertEquals(1, countNodes("sheet:ARCHIVE"))
        }
    }

    @Test
    fun markSeenOnAPaneSendsPanesMarkSeenForThatPaneOnlyAndReadsTheTreeAgain() {
        FakeOrreryServer().use { server ->
            servePanes(
                server,
                pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 120, NOW - 60, TMUX_SESSION),
                pane(OTHER_PANE, BETA, NOW - 90, NOW - 150, NOW - 90, OTHER_SESSION),
            )
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:MARK_SEEN")

            tapSheet("sheet:MARK_SEEN")

            composeRule.awaitUntil { requestsTo(server, PANES_LIST) == 2 }
            val sent = server.requests.filter { it.rawPath == MARK_SEEN }.map { MarkSeenRequest.parseFrom(it.body) }
            assertEquals(listOf(TMUX_PANE to NOW - 60), sent.map { it.pane to it.readUpTo })
            composeRule.awaitUntil { countNodes("sheet-title") == 0 }
        }
    }

    @Test
    fun markSeenOnAHeadlessRowSendsControlSeenWithItsId() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder().addSessions(headless(HEADLESS_SESSION, NOW - 60, NOW - 120)).build(),
            )
            setScreen(server)
            awaitTag(sessionTag(HEADLESS_SESSION))
            longPress(sessionTag(HEADLESS_SESSION))
            awaitTag("sheet:MARK_SEEN")

            tapSheet("sheet:MARK_SEEN")

            composeRule.awaitUntil { requestsTo(server, LIST_HEADLESS) == 2 }
            val sent = server.requests.filter { it.rawPath == CONTROL_SEEN }.map { SeenRequest.parseFrom(it.body) }
            assertEquals(listOf(HEADLESS_SESSION), sent.map { it.id })
            assertEquals(0, requestsTo(server, MARK_SEEN))
        }
    }

    @Test
    fun archiveOnAPaneSendsItsOrreryIdAsTheSessionKindAndNoOtherRow() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 10, NOW - 60, TMUX_SESSION))
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:ARCHIVE")

            tapSheet("sheet:ARCHIVE")

            composeRule.awaitUntil { requestsTo(server, PANES_LIST) == 2 }
            val sent = server.requests.filter { it.rawPath == EPICS_ARCHIVE }.map { EpicsArchiveRequest.parseFrom(it.body) }
            assertEquals(listOf("session" to TMUX_SESSION), sent.map { it.kind to it.id })
            assertEquals(0, requestsTo(server, MARK_SEEN))
        }
    }

    @Test
    fun archiveOnAHeadlessRowSendsItsIdAsTheHeadlessKind() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder().addSessions(headless(HEADLESS_SESSION, NOW - 60, NOW - 10)).build(),
            )
            setScreen(server)
            awaitTag(sessionTag(HEADLESS_SESSION))
            longPress(sessionTag(HEADLESS_SESSION))
            awaitTag("sheet:ARCHIVE")

            tapSheet("sheet:ARCHIVE")

            composeRule.awaitUntil { requestsTo(server, LIST_HEADLESS) == 2 }
            val sent = server.requests.filter { it.rawPath == EPICS_ARCHIVE }.map { EpicsArchiveRequest.parseFrom(it.body) }
            assertEquals(listOf("headless" to HEADLESS_SESSION), sent.map { it.kind to it.id })
        }
    }

    @Test
    fun archiveOnAnEpicSendsItsIdAsTheEpicKind() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 10, NOW - 60, TMUX_SESSION))
            server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(TMUX_SESSION)).build())
            server.respond(EPICS_GET, EpicsGetResponse.newBuilder().setEpic(liveEpic(TMUX_SESSION)).build())
            setScreen(server)
            awaitTag(epicTag())
            longPress(epicTag())
            awaitTag("sheet:ARCHIVE")
            assertEquals(0, countNodes("sheet:MARK_SEEN"))

            tapSheet("sheet:ARCHIVE")

            composeRule.awaitUntil { requestsTo(server, EPICS_LIST) == 2 }
            val sent = server.requests.filter { it.rawPath == EPICS_ARCHIVE }.map { EpicsArchiveRequest.parseFrom(it.body) }
            assertEquals(listOf("epic" to EPIC_ID), sent.map { it.kind to it.id })
        }
    }

    @Test
    fun aFailedMarkSeenShowsItsLineUnderTheHeadAndReadsNothingAgain() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 120, NOW - 60, TMUX_SESSION))
            server.fail(MARK_SEEN, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:MARK_SEEN")

            tapSheet("sheet:MARK_SEEN")

            awaitTag("sheet-failed")
            composeRule.onNodeWithTag("sheet-failed").assertTextEquals(MARK_SEEN_FAILED)
            assertEquals(1, requestsTo(server, PANES_LIST))
            assertEquals(1, countNodes("sheet-title"))
        }
    }

    @Test
    fun aRefusedArchiveShowsTheRefusalUnderTheHeadAndReadsNothingAgain() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 10, NOW - 60, TMUX_SESSION))
            server.fail(EPICS_ARCHIVE, Code.FAILED_PRECONDITION, "still running")
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:ARCHIVE")

            tapSheet("sheet:ARCHIVE")

            awaitTag("sheet-failed")
            composeRule.onNodeWithTag("sheet-failed").assertTextEquals("$ARCHIVE_FAILED: still running")
            assertEquals(1, requestsTo(server, PANES_LIST))
            assertEquals(1, countNodes("sheet-title"))
        }
    }

    @Test
    fun unarchiveInTheArchivedViewSendsEpicsUnarchiveForTheEpic() {
        FakeOrreryServer().use { server ->
            servePanes(server, pane(TMUX_PANE, ALPHA, NOW - 60, NOW - 10, NOW - 60, TMUX_SESSION))
            val archived = liveEpic(TMUX_SESSION).toBuilder().setArchived(true).build()
            server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(archived).build())
            server.respond(EPICS_GET, EpicsGetResponse.newBuilder().setEpic(archived).build())
            setScreen(server)
            awaitTag(convoTag(TMUX_PANE))
            composeRule.onNodeWithTag("open-archived").performClick()
            awaitTag(epicTag())

            longPress(epicTag())

            awaitTag("sheet:UNARCHIVE")
            assertEquals(0, countNodes("sheet:ARCHIVE"))
            tapSheet("sheet:UNARCHIVE")

            composeRule.awaitUntil { requestsTo(server, EPICS_LIST) == 2 }
            val sent = server.requests.filter { it.rawPath == EPICS_UNARCHIVE }.map { EpicsUnarchiveRequest.parseFrom(it.body) }
            assertEquals(listOf("epic" to EPIC_ID), sent.map { it.kind to it.id })
            assertEquals(0, requestsTo(server, EPICS_ARCHIVE))
        }
    }
}
