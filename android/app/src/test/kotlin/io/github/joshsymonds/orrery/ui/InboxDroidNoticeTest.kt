package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.Collections

/**
 * Invented tmux panes, each bound to a Claude session, served through the fake server. A read that moves a row shows one
 * move notice at the foot, which names the row and its new place, or the count and every row. The screen is removed and
 * shown again with the same state, which is the second answer, so the notice is the second answer's. Undo and Show are
 * taken on the notice; the rows the server holds change only through the calls the test makes and through Unarchive,
 * which takes the archive off the session the call names. The clock is held still on the fake clock, and the checks are
 * Compose semantics.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidNoticeTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val HOST = "example-host"
        const val ALPHA_KEY = "|$ALPHA"
        const val ALPHA_TAG = "project:$ALPHA_KEY"
        const val ALPHA_FOLD_TAG = "fold:$ALPHA_KEY"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_UNARCHIVE = "/orrery.v1.Epics/Unarchive"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val NOTICE = "move-notice"
        const val NOTICE_LINE = "move-notice-line"
        const val NOTICE_SHOW = "move-notice-show"
        const val NOTICE_UNDO = "move-notice-undo"
        const val NOTICE_FAILED = "move-notice-failed"
        const val BACK_TO_INBOX = "back-to-inbox"
        const val UNDO_NO_ANSWER = "Could not bring it back: the server did not answer."
        const val STAYING_PANE = "%9"
    }

    /** An invented tmux pane served with its session: where the pane is, and whether the session has ended or is archived. */
    private data class Served(val pane: String, val cwd: String, val ended: Boolean, val archived: Boolean)

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val state = PaneListState()
    private var showing by mutableStateOf(true)

    /** The invented panes the server serves, by pane id. The fake server's handlers read it on their own threads. */
    private val served: MutableMap<String, Served> = Collections.synchronizedMap(LinkedHashMap())

    private fun sessionOf(pane: String): String = pane.removePrefix("%").padStart(32, '0')

    private fun convoTag(pane: String): String = "convo:pane:$HOST:$pane"

    /** Serves [pane] in [cwd], with its session ended or archived as given. */
    private fun put(pane: String, cwd: String, ended: Boolean = false, archived: Boolean = false) {
        served[pane] = Served(pane, cwd, ended, archived)
    }

    private fun snapshot(): List<Served> = synchronized(served) { served.values.toList() }

    private fun tmuxPane(row: Served): PaneProto = PaneProto.newBuilder()
        .setId(row.pane)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(row.cwd)
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel("label-${row.pane}")
        .setChanged(NOW - 60)
        .setSeen(NOW - 60)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(NOW - 60)
        .setOrreryId(sessionOf(row.pane))
        .setClaudeSession("synthetic-claude-${row.pane}")
        .build()

    /**
     * Answers the host, the empty lists and the panes from [served]. Unarchive takes the archive off the session it names,
     * so the next read shows the row back where its session puts it.
     */
    private fun serveRoutes(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.route(PANES_LIST) {
            Reply.Message(PanesListResponse.newBuilder().addAllPanes(snapshot().map { tmuxPane(it) }).build(), 0L)
        }
        server.route(SESSIONS_LIST) {
            val sessions = snapshot().map {
                SessionProto.newBuilder()
                    .setId(sessionOf(it.pane))
                    .setEnded(it.ended)
                    .setArchived(it.archived)
                    .build()
            }
            Reply.Message(ListResponse.newBuilder().addAllSessions(sessions).build(), 0L)
        }
        server.route(EPICS_UNARCHIVE) { request ->
            val session = EpicsUnarchiveRequest.parseFrom(request.body).id
            synchronized(served) {
                served.replaceAll { _, row -> if (sessionOf(row.pane) == session) row.copy(archived = false) else row }
            }
            Reply.Message(EpicsUnarchiveResponse.getDefaultInstance(), 0L)
        }
    }

    /** Shows the inbox with the test's state. */
    private fun setScreen(server: FakeOrreryServer) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            if (showing) {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                    state = state,
                )
            }
        }
    }

    /**
     * Removes the screen from the composition and shows it again with the same state. The read that the screen starts is
     * the second answer, and the wait returns once that answer is applied and drawn.
     */
    private fun answerAgain() {
        val firstRead = state.held
        composeRule.runOnUiThread { showing = false }
        composeRule.waitForIdle()
        composeRule.runOnUiThread { showing = true }
        composeRule.awaitUntil("the second answer is applied") { state.held !== firstRead }
        composeRule.waitForIdle()
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    private fun unarchivesSent(server: FakeOrreryServer): List<Pair<String, String>> =
        server.requests.filter { it.rawPath == EPICS_UNARCHIVE }
            .map { EpicsUnarchiveRequest.parseFrom(it.body) }
            .map { it.kind to it.id }

    /**
     * Shows the inbox with [pane] and a staying row in alpha, applies [change] to the server, and answers again: the notice
     * the second answer shows is the one the test checks.
     */
    private fun noticeAfter(server: FakeOrreryServer, pane: String, change: () -> Unit) {
        serveRoutes(server)
        put(pane, ALPHA)
        put(STAYING_PANE, ALPHA)
        setScreen(server)
        awaitNode(hasTestTag(convoTag(pane)))
        change()
        answerAgain()
    }

    @Test
    fun oneMovedRowShowsOneNoticeNamingTheRowAndItsNewPlace() {
        FakeOrreryServer().use { server ->
            noticeAfter(server, "%1") { put("%1", BETA) }

            assertEquals(1, countNodes(hasTestTag(NOTICE)))
            composeRule.onNodeWithTag(NOTICE_LINE).assertTextEquals("Moved “label-%1” to beta.")
            assertEquals(1, countNodes(hasTestTag(NOTICE_SHOW)))
            assertEquals(1, countNodes(hasTestTag(NOTICE_UNDO)))
        }
    }

    @Test
    fun severalMovedRowsShowOneNoticeWithTheCountAndEveryName() {
        FakeOrreryServer().use { server ->
            serveRoutes(server)
            put("%1", ALPHA)
            put("%2", ALPHA)
            put("%3", ALPHA)
            put(STAYING_PANE, ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            put("%1", BETA)
            put("%2", BETA)
            put("%3", BETA)
            answerAgain()

            assertEquals(1, countNodes(hasTestTag(NOTICE)))
            assertEquals(1, countNodes(hasTestTag(NOTICE_LINE) and hasText("Moved 3 conversations: ", substring = true)))
            for (pane in listOf("%1", "%2", "%3")) {
                assertEquals(1, countNodes(hasTestTag(NOTICE_LINE) and hasText("“label-$pane”", substring = true)))
            }
            assertEquals(0, countNodes(hasTestTag(NOTICE_LINE) and hasText("label-$STAYING_PANE", substring = true)))
        }
    }

    @Test
    fun undoSendsUnarchiveForEveryMovedRowAndTakesTheNoticeAway() {
        FakeOrreryServer().use { server ->
            serveRoutes(server)
            put("%1", ALPHA)
            put("%2", ALPHA)
            put(STAYING_PANE, ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%2")))

            // Both rows are archived by a change the server made: the read moves them to the archived line.
            put("%1", ALPHA, archived = true)
            put("%2", ALPHA, archived = true)
            answerAgain()
            assertEquals(1, countNodes(hasTestTag(NOTICE_LINE) and hasText("Moved 2 conversations: ", substring = true)))

            composeRule.onNodeWithTag(NOTICE_UNDO).performClick()
            // The rows come back into alpha once the read that Undo asked for is applied.
            composeRule.awaitUntil("the rows are back in alpha") {
                countNodes(hasTestTag(convoTag("%1")) and hasAnyAncestor(hasTestTag(ALPHA_TAG))) == 1 &&
                    countNodes(hasTestTag(convoTag("%2")) and hasAnyAncestor(hasTestTag(ALPHA_TAG))) == 1
            }

            assertEquals(2, unarchivesSent(server).size)
            assertEquals(
                setOf("session" to sessionOf("%1"), "session" to sessionOf("%2")),
                unarchivesSent(server).toSet(),
            )
            // The read that brought them back leaves no notice: the owner's Undo made the move, so it is not a move.
            assertEquals(0, countNodes(hasTestTag(NOTICE)))
        }
    }

    @Test
    fun aFailedUndoKeepsTheNoticeAndSaysWhy() {
        FakeOrreryServer().use { server ->
            noticeAfter(server, "%1") { put("%1", BETA) }
            server.drop(EPICS_UNARCHIVE)

            composeRule.onNodeWithTag(NOTICE_UNDO).performClick()
            composeRule.awaitUntil("the failed Undo says why") { countNodes(hasTestTag(NOTICE_FAILED)) > 0 }

            composeRule.onNodeWithTag(NOTICE_FAILED).assertTextEquals(UNDO_NO_ANSWER)
            assertEquals(1, countNodes(hasTestTag(NOTICE)))
            assertEquals(1, countNodes(hasTestTag(NOTICE_UNDO)))
        }
    }

    @Test
    fun showOnAnArchivedMoveOpensTheArchivedView() {
        FakeOrreryServer().use { server ->
            noticeAfter(server, "%1") { put("%1", ALPHA, archived = true) }
            composeRule.onNodeWithTag(NOTICE_LINE).assertTextEquals("Moved “label-%1” to Archived.")

            composeRule.onNodeWithTag(NOTICE_SHOW).performClick()
            composeRule.awaitUntil("the archived view is open") { countNodes(hasTestTag(BACK_TO_INBOX)) > 0 }
            composeRule.waitForIdle()

            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasTestTag(NOTICE)))
        }
    }

    @Test
    fun showOnAnEndedMoveOpensTheProjectsEndedLine() {
        FakeOrreryServer().use { server ->
            noticeAfter(server, "%1") { put("%1", ALPHA, ended = true) }
            composeRule.onNodeWithTag(NOTICE_LINE).assertTextEquals("Moved “label-%1” to alpha’s ended line.")

            composeRule.onNodeWithTag(NOTICE_SHOW).performClick()
            composeRule.awaitUntil("the ended line is open") { ALPHA_KEY in state.endedOpen }
            composeRule.waitForIdle()

            assertEquals(1, countNodes(hasTestTag(convoTag("%1")) and hasAnyAncestor(hasTestTag(ALPHA_TAG))))
            assertEquals(0, countNodes(hasTestTag(NOTICE)))
        }
    }

    @Test
    fun showOnATreeMoveUnfoldsTheProjectItLandsIn() {
        FakeOrreryServer().use { server ->
            serveRoutes(server)
            put("%1", ALPHA, archived = true)
            put(STAYING_PANE, ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(convoTag(STAYING_PANE)))
            composeRule.onNodeWithTag(ALPHA_FOLD_TAG).performClick()
            composeRule.awaitUntil("alpha is folded") { countNodes(hasTestTag(convoTag(STAYING_PANE))) == 0 }

            // The archived row comes back into alpha, which is folded, so its notice names alpha.
            put("%1", ALPHA)
            answerAgain()
            composeRule.onNodeWithTag(NOTICE_LINE).assertTextEquals("Moved “label-%1” to alpha.")

            composeRule.onNodeWithTag(NOTICE_SHOW).performClick()
            composeRule.awaitUntil("alpha is unfolded") { ALPHA_KEY !in state.folded }
            composeRule.waitForIdle()

            assertEquals(1, countNodes(hasTestTag(convoTag("%1")) and hasAnyAncestor(hasTestTag(ALPHA_TAG))))
            assertEquals(1, countNodes(hasTestTag(convoTag(STAYING_PANE))))
            assertEquals(0, countNodes(hasTestTag(NOTICE)))
        }
    }

    /** The notice of one row moved to beta, at the width the test runs at: it is drawn, with Show and Undo. */
    private fun noticeIsDrawnAtThisWidth() {
        FakeOrreryServer().use { server ->
            noticeAfter(server, "%1") { put("%1", BETA) }

            composeRule.onNodeWithTag(NOTICE).assertIsDisplayed()
            composeRule.onNodeWithTag(NOTICE_LINE).assertIsDisplayed()
            composeRule.onNodeWithTag(NOTICE_SHOW).assertIsDisplayed()
            composeRule.onNodeWithTag(NOTICE_UNDO).assertIsDisplayed()
        }
    }

    @Test
    fun theNoticeIsDrawnAtANarrowWidth() {
        noticeIsDrawnAtThisWidth()
    }

    @Test
    @Config(sdk = [35], qualifiers = "w900dp-h1200dp")
    fun theNoticeIsDrawnAtAWideWidth() {
        noticeIsDrawnAtThisWidth()
    }
}
