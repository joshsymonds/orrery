package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTouchInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.ConcurrentHashMap

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ArchiveToastTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HOST = "example-host"
        const val ALPHA = "/home/user/alpha"
        const val TMUX_PANE = "%1"
        const val OTHER_PANE = "%2"
        const val TMUX_SESSION = "00000000000000000000000000000001"
        const val OTHER_SESSION = "00000000000000000000000000000002"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val MARK_SEEN = "/orrery.v1.Panes/MarkSeen"
        const val CONTROL_SEEN = "/orrery.v1.Control/Seen"
        const val EPICS_ARCHIVE = "/orrery.v1.Epics/Archive"
        const val EPICS_UNARCHIVE = "/orrery.v1.Epics/Unarchive"
        const val UNDO_NO_ANSWER = "Could not bring it back: the server did not answer."
        const val UNDO_REFUSED = "Could not bring it back: the server refused it."
        const val ONE_DAY_MILLIS = 24L * 60 * 60 * 1000
        const val TWO_DAYS_SECONDS = 2L * 24 * 60 * 60
        const val CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val BASH_QUESTION = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
        const val PAGE_TITLE = "label-%1"
        const val HEADLESS = "0123456789abcdef0123456789abcdef"
        const val SESSION_TITLE = "synthetic session"
        const val SESSION_REQUEST = "synthetic-request"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun archivedSet(vararg ids: String): MutableSet<String> =
        ConcurrentHashMap.newKeySet<String>().apply { addAll(ids) }

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun pane(id: String, session: String, seen: Long = NOW - 10, newestReply: Long = NOW - 60): PaneProto =
        PaneProto.newBuilder()
            .setId(id)
            .setHost(HOST)
            .setSession("example-session")
            .setWindow(1)
            .setPane(0)
            .setCwd(ALPHA)
            .setCommand("claude")
            .setState("working")
            .setSummary("Synthetic summary.")
            .setLabel("label-$id")
            .setChanged(NOW - 60)
            .setSeen(seen)
            .setQuestion("")
            .setQuestionHash("")
            .setNewestReply(newestReply)
            .setOrreryId(session)
            .build()

    /** The host and the empty lists, and a landed answer to each write a sheet item makes. */
    private fun serveDefaults(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(MARK_SEEN, MarkSeenResponse.getDefaultInstance())
        server.respond(CONTROL_SEEN, SeenResponse.getDefaultInstance())
    }

    /**
     * The panes, and the sessions the owner has archived, which the server keeps: an archive adds its session's id and an
     * unarchive takes that id away, so the list the inbox reads again shows what the call changed and nothing else.
     */
    private fun serveRows(server: FakeOrreryServer, archived: MutableSet<String>, vararg panes: PaneProto) {
        serveDefaults(server)
        server.respond(PANES_LIST, PanesListResponse.newBuilder().addAllPanes(panes.toList()).build())
        server.route(SESSIONS_LIST) {
            val sessions = archived.map { SessionProto.newBuilder().setId(it).setArchived(true).build() }
            Reply.Message(ListResponse.newBuilder().addAllSessions(sessions).build(), 0L)
        }
        server.route(EPICS_ARCHIVE) { request ->
            archived.add(EpicsArchiveRequest.parseFrom(request.body).id)
            Reply.Message(EpicsArchiveResponse.getDefaultInstance(), 0L)
        }
        server.route(EPICS_UNARCHIVE) { request ->
            archived.remove(EpicsUnarchiveRequest.parseFrom(request.body).id)
            Reply.Message(EpicsUnarchiveResponse.getDefaultInstance(), 0L)
        }
    }

    /** Mounts the home page; [showing] false swaps it out, which is leaving the page. */
    private fun setHome(server: FakeOrreryServer, showing: MutableState<Boolean> = mutableStateOf(true)) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            if (showing.value) {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                )
            } else {
                Text("elsewhere", modifier = Modifier.testTag("elsewhere"))
            }
        }
    }

    private fun countNodes(tag: String): Int =
        composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countText(tag: String, text: String): Int =
        composeRule.onAllNodes(hasTestTag(tag) and hasText(text), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(tag) > 0 }
    }

    private fun awaitText(tag: String, text: String) {
        composeRule.awaitUntil { countText(tag, text) > 0 }
    }

    private fun requestsTo(server: FakeOrreryServer, path: String): Int = server.requests.count { it.rawPath == path }

    private fun unarchivesSent(server: FakeOrreryServer): List<Pair<String, String>> =
        server.requests.filter { it.rawPath == EPICS_UNARCHIVE }
            .map { EpicsUnarchiveRequest.parseFrom(it.body) }
            .map { it.kind to it.id }

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

    /** Archives the row [paneId] from its sheet, and waits for the sheet to close on the landed call. */
    private fun archiveFromSheet(paneId: String) {
        longPress(convoTag(paneId))
        awaitTag("sheet:ARCHIVE")
        tapSheet("sheet:ARCHIVE")
    }

    private fun undo() {
        composeRule.onNodeWithTag("archive-toast-undo").performClick()
    }

    private fun archivesSent(server: FakeOrreryServer): List<Pair<String, String>> =
        server.requests.filter { it.rawPath == EPICS_ARCHIVE }
            .map { EpicsArchiveRequest.parseFrom(it.body) }
            .map { it.kind to it.id }

    /** The conversation page's stale Ask: a question left waiting two days, which offers Archive this session. */
    private fun stalePane(): PaneProto = pane(TMUX_PANE, TMUX_SESSION).toBuilder()
        .setState("waiting")
        .setLabel(PAGE_TITLE)
        .setChanged(NOW - TWO_DAYS_SECONDS)
        .setQuestion(BASH_QUESTION)
        .setQuestionHash("abc123")
        .build()

    /** Serves the pane [pane] to the conversation page, with an empty conversation and archive calls that land. */
    private fun servePage(server: FakeOrreryServer, pane: PaneProto) {
        serveDefaults(server)
        server.respond(PANES_LIST, PanesListResponse.newBuilder().addPanes(pane).build())
        server.respond(CONVERSATION_PATH, ConversationResponse.newBuilder().setOrigin("synthetic origin").build())
        server.respond(EPICS_ARCHIVE, EpicsArchiveResponse.getDefaultInstance())
        server.respond(EPICS_UNARCHIVE, EpicsUnarchiveResponse.getDefaultInstance())
    }

    /** One watch answer for the headless session [HEADLESS]: stale since two days ago, with a question open. */
    private fun sessionDelta(): SessionsWatchConversationResponse = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(HEADLESS)
        .setSeq(1L)
        .setState(
            ConversationState.newBuilder()
                .setOrigin("synthetic origin")
                .setSession(
                    HeadlessSession.newBuilder()
                        .setId(HEADLESS)
                        .setDir(ALPHA)
                        .setLabel(SESSION_TITLE)
                        .setState("waiting")
                        .setChanged(NOW - TWO_DAYS_SECONDS)
                        .setSeen(NOW - TWO_DAYS_SECONDS),
                )
                .setRequest(
                    OpenRequest.newBuilder()
                        .setId(SESSION_REQUEST)
                        .setKind("question")
                        .setQuestion("Pick a synthetic option?")
                        .addAllOptions(listOf("Synthetic one", "Synthetic two")),
                ),
        )
        .build()

    /** Serves the headless session to the session page; every watch answers the same state, so a reconnect adds nothing. */
    private fun serveSession(server: FakeOrreryServer) {
        serveDefaults(server)
        server.route(WATCH_PATH) { Reply.Stream(listOf(sessionDelta()), StreamEnd.Open) }
        server.respond(EPICS_ARCHIVE, EpicsArchiveResponse.getDefaultInstance())
        server.respond(EPICS_UNARCHIVE, EpicsUnarchiveResponse.getDefaultInstance())
    }

    /**
     * Mounts a page through [page]; [showing] false swaps it out, which is leaving the page. The client is made once,
     * outside the composition, so a recomposition does not hand the page a client it has not read with.
     */
    private fun mountPage(
        server: FakeOrreryServer,
        showing: MutableState<Boolean>,
        page: @Composable (OrreryClient) -> Unit,
    ) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            if (showing.value) {
                page(client)
            } else {
                Text("elsewhere", modifier = Modifier.testTag("elsewhere"))
            }
        }
    }

    private fun mountPane(server: FakeOrreryServer, showing: MutableState<Boolean> = mutableStateOf(true)) {
        mountPage(server, showing) { client -> PaneScreen(TMUX_PANE, client, clock = { NOW }) }
    }

    private fun mountSession(server: FakeOrreryServer, showing: MutableState<Boolean> = mutableStateOf(true)) {
        mountPage(server, showing) { client -> SessionScreen(HEADLESS, client, clock = { NOW }) }
    }

    /** Archives this session from the stale Ask's foot. */
    private fun archiveFromAsk() {
        awaitTag("ask-archive")
        composeRule.onNodeWithTag("ask-archive").performClick()
    }

    /** The toast sits in the lower half of the page and ends above the dock, so it never covers the dock's input. */
    private fun assertToastAboveDock() {
        val toast = composeRule.onNodeWithTag("archive-toast").fetchSemanticsNode().boundsInRoot
        val dock = composeRule.onNodeWithTag("dock").fetchSemanticsNode().boundsInRoot
        val root = composeRule.onRoot().fetchSemanticsNode().boundsInRoot
        assertTrue("the toast sits in the lower half of the page", toast.top > root.bottom / 2f)
        assertTrue("the toast ends above the dock", toast.bottom <= dock.top + 1f)
    }

    @Test
    fun archivingARowFromItsSheetShowsTheToastAtTheFootWithUndo() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))

            archiveFromSheet(TMUX_PANE)

            awaitTag("archive-toast")
            composeRule.onNodeWithTag("archive-toast-line").assertTextEquals("Archived “label-%1”.")
            assertEquals(1, countNodes("archive-toast-undo"))
            val toast = composeRule.onNodeWithTag("archive-toast").fetchSemanticsNode().boundsInRoot
            val root = composeRule.onRoot().fetchSemanticsNode().boundsInRoot
            assertTrue("the toast sits in the lower half of the page", toast.top > root.bottom / 2f)
        }
    }

    @Test
    fun undoBringsBackTheArchivedRowAndEndsTheToast() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitTag("archive-toast")
            composeRule.awaitUntil { countNodes(convoTag(TMUX_PANE)) == 0 }

            undo()

            composeRule.awaitUntil { countNodes("archive-toast") == 0 }
            composeRule.awaitUntil { countNodes(convoTag(TMUX_PANE)) > 0 }
            assertEquals(listOf("session" to TMUX_SESSION), unarchivesSent(server))
            assertEquals(3, requestsTo(server, PANES_LIST))
        }
    }

    @Test
    fun aFailedUndoKeepsTheToastAndItsUndoAndSaysWhy() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            server.drop(EPICS_UNARCHIVE)
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitTag("archive-toast")
            composeRule.awaitUntil { countNodes(convoTag(TMUX_PANE)) == 0 }

            undo()

            awaitTag("archive-toast-failed")
            composeRule.onNodeWithTag("archive-toast-failed").assertTextEquals(UNDO_NO_ANSWER)
            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countNodes("archive-toast-undo"))
            assertEquals(listOf("session" to TMUX_SESSION), unarchivesSent(server))
            assertEquals(2, requestsTo(server, PANES_LIST))
        }
    }

    @Test
    fun aRefusedUndoSaysTheServerRefusedIt() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            server.fail(EPICS_UNARCHIVE, Code.FAILED_PRECONDITION, "still running")
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitTag("archive-toast")
            composeRule.awaitUntil { countNodes(convoTag(TMUX_PANE)) == 0 }

            undo()

            awaitTag("archive-toast-failed")
            composeRule.onNodeWithTag("archive-toast-failed").assertTextEquals(UNDO_REFUSED)
            assertEquals(1, countNodes("archive-toast"))
        }
    }

    @Test
    fun anotherArchiveReplacesTheToastAndItsUndoBringsBackOnlyItsRow() {
        FakeOrreryServer().use { server ->
            serveRows(
                server,
                archivedSet(),
                pane(TMUX_PANE, TMUX_SESSION),
                pane(OTHER_PANE, OTHER_SESSION),
            )
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitText("archive-toast-line", "Archived “label-%1”.")
            composeRule.awaitUntil { countNodes(convoTag(TMUX_PANE)) == 0 }
            awaitTag(convoTag(OTHER_PANE))

            archiveFromSheet(OTHER_PANE)

            awaitText("archive-toast-line", "Archived “label-%2”.")
            assertEquals(1, countNodes("archive-toast"))
            composeRule.awaitUntil { countNodes(convoTag(OTHER_PANE)) == 0 }

            undo()

            composeRule.awaitUntil { countNodes("archive-toast") == 0 }
            composeRule.awaitUntil { countNodes(convoTag(OTHER_PANE)) > 0 }
            assertEquals(0, countNodes(convoTag(TMUX_PANE)))
            assertEquals(listOf("session" to OTHER_SESSION), unarchivesSent(server))
        }
    }

    @Test
    fun leavingTheHomePageEndsTheToast() {
        FakeOrreryServer().use { server ->
            val showing = mutableStateOf(true)
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            setHome(server, showing)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitTag("archive-toast")

            composeRule.runOnIdle { showing.value = false }
            composeRule.awaitUntil { countNodes("elsewhere") > 0 }
            composeRule.runOnIdle { showing.value = true }
            composeRule.awaitUntil { countNodes("open-archived") > 0 }

            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun theToastStaysPastAnyTimer() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(), pane(TMUX_PANE, TMUX_SESSION))
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            archiveFromSheet(TMUX_PANE)
            awaitTag("archive-toast")

            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(ONE_DAY_MILLIS, ignoreFrameDuration = true)
            composeRule.waitForIdle()

            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countText("archive-toast-line", "Archived “label-%1”."))
            assertEquals(1, countNodes("archive-toast-undo"))
        }
    }

    @Test
    fun markingSeenShowsNoToast() {
        FakeOrreryServer().use { server ->
            serveRows(
                server,
                archivedSet(),
                pane(TMUX_PANE, TMUX_SESSION, seen = NOW - 120, newestReply = NOW - 60),
            )
            setHome(server)
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:MARK_SEEN")

            tapSheet("sheet:MARK_SEEN")

            composeRule.awaitUntil { requestsTo(server, PANES_LIST) == 2 }
            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun unarchivingFromTheArchivedViewShowsNoToast() {
        FakeOrreryServer().use { server ->
            serveRows(server, archivedSet(TMUX_SESSION), pane(TMUX_PANE, TMUX_SESSION))
            setHome(server)
            awaitTag("open-archived")
            composeRule.onNodeWithTag("open-archived").performClick()
            awaitTag(convoTag(TMUX_PANE))
            longPress(convoTag(TMUX_PANE))
            awaitTag("sheet:UNARCHIVE")

            tapSheet("sheet:UNARCHIVE")

            composeRule.awaitUntil { requestsTo(server, PANES_LIST) == 2 }
            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun archivingFromAStaleAskOnAPaneShowsTheToastAtTheFootWithTheHeadTitle() {
        FakeOrreryServer().use { server ->
            servePage(server, stalePane())
            mountPane(server)

            archiveFromAsk()

            awaitTag("archive-toast")
            composeRule.onNodeWithTag("head-text").assertTextEquals(PAGE_TITLE)
            composeRule.onNodeWithTag("archive-toast-line").assertTextEquals("Archived “$PAGE_TITLE”.")
            assertEquals(1, countNodes("archive-toast-undo"))
            assertEquals(listOf("session" to TMUX_SESSION), archivesSent(server))
            assertToastAboveDock()
        }
    }

    @Test
    fun undoOnAPaneSendsUnarchiveForItsRowAndEndsTheToast() {
        FakeOrreryServer().use { server ->
            servePage(server, stalePane())
            mountPane(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            undo()

            composeRule.awaitUntil { countNodes("archive-toast") == 0 }
            assertEquals(listOf("session" to TMUX_SESSION), unarchivesSent(server))
        }
    }

    @Test
    fun aFailedUndoOnAPaneSaysWhyAndKeepsTheToastAndItsUndo() {
        FakeOrreryServer().use { server ->
            servePage(server, stalePane())
            server.drop(EPICS_UNARCHIVE)
            mountPane(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            undo()

            awaitTag("archive-toast-failed")
            composeRule.onNodeWithTag("archive-toast-failed").assertTextEquals(UNDO_NO_ANSWER)
            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countNodes("archive-toast-undo"))
            assertEquals(listOf("session" to TMUX_SESSION), unarchivesSent(server))
        }
    }

    @Test
    fun leavingAPaneEndsTheToast() {
        FakeOrreryServer().use { server ->
            val showing = mutableStateOf(true)
            servePage(server, stalePane())
            mountPane(server, showing)
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.runOnIdle { showing.value = false }
            composeRule.awaitUntil { countNodes("elsewhere") > 0 }
            composeRule.runOnIdle { showing.value = true }
            awaitTag("ask-archive")

            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun openingAnotherPaneInPlaceEndsTheToast() {
        FakeOrreryServer().use { server ->
            val paneId = mutableStateOf(TMUX_PANE)
            servePage(server, stalePane())
            mountPage(server, mutableStateOf(true)) { client -> PaneScreen(paneId.value, client, clock = { NOW }) }
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.runOnIdle { paneId.value = OTHER_PANE }
            composeRule.waitForIdle()

            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun theToastOnAPaneStaysPastAnyTimer() {
        FakeOrreryServer().use { server ->
            servePage(server, stalePane())
            mountPane(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(ONE_DAY_MILLIS, ignoreFrameDuration = true)
            composeRule.waitForIdle()

            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countText("archive-toast-line", "Archived “$PAGE_TITLE”."))
            assertEquals(1, countNodes("archive-toast-undo"))
        }
    }

    @Test
    fun archivingFromAStaleAskOnASessionShowsTheToastAtTheFootWithTheHeadTitle() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            mountSession(server)

            archiveFromAsk()

            awaitTag("archive-toast")
            composeRule.onNodeWithTag("head-text").assertTextEquals(SESSION_TITLE)
            composeRule.onNodeWithTag("archive-toast-line").assertTextEquals("Archived “$SESSION_TITLE”.")
            assertEquals(1, countNodes("archive-toast-undo"))
            assertEquals(listOf("headless" to HEADLESS), archivesSent(server))
            assertToastAboveDock()
        }
    }

    @Test
    fun undoOnASessionSendsUnarchiveForItsSessionAndEndsTheToast() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            mountSession(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            undo()

            composeRule.awaitUntil { countNodes("archive-toast") == 0 }
            assertEquals(listOf("headless" to HEADLESS), unarchivesSent(server))
        }
    }

    @Test
    fun aFailedUndoOnASessionSaysWhyAndKeepsTheToastAndItsUndo() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            server.drop(EPICS_UNARCHIVE)
            mountSession(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            undo()

            awaitTag("archive-toast-failed")
            composeRule.onNodeWithTag("archive-toast-failed").assertTextEquals(UNDO_NO_ANSWER)
            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countNodes("archive-toast-undo"))
            assertEquals(listOf("headless" to HEADLESS), unarchivesSent(server))
        }
    }

    @Test
    fun leavingASessionEndsTheToast() {
        FakeOrreryServer().use { server ->
            val showing = mutableStateOf(true)
            serveSession(server)
            mountSession(server, showing)
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.runOnIdle { showing.value = false }
            composeRule.awaitUntil { countNodes("elsewhere") > 0 }
            composeRule.runOnIdle { showing.value = true }
            awaitTag("ask-archive")

            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun openingAnotherSessionInPlaceEndsTheToast() {
        FakeOrreryServer().use { server ->
            val sessionId = mutableStateOf(HEADLESS)
            serveSession(server)
            mountPage(server, mutableStateOf(true)) { client -> SessionScreen(sessionId.value, client, clock = { NOW }) }
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.runOnIdle { sessionId.value = OTHER_SESSION }
            composeRule.waitForIdle()

            assertEquals(0, countNodes("archive-toast"))
        }
    }

    @Test
    fun theToastOnASessionStaysPastAnyTimer() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            mountSession(server)
            archiveFromAsk()
            awaitTag("archive-toast")

            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(ONE_DAY_MILLIS, ignoreFrameDuration = true)
            composeRule.waitForIdle()

            assertEquals(1, countNodes("archive-toast"))
            assertEquals(1, countText("archive-toast-line", "Archived “$SESSION_TITLE”."))
            assertEquals(1, countNodes("archive-toast-undo"))
        }
    }
}
