package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performSemanticsAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class SteerMenuTest {
    private companion object {
        const val REPO = "/home/user/projects/synthetic-repo"
        const val EPIC = "synthetic-repo/synthetic-epic"
        const val SLUG = "synthetic-epic"
        const val STEERER = "sess-steerer"
        const val LIVE_TMUX = "sess-live-tmux"
        const val LIVE_HEADLESS = "sess-live-headless"
        const val STOPPED_HEADLESS = "sess-stopped-headless"
        const val ARCHIVED_HEADLESS = "sess-archived-headless"
        const val ARCHIVED_TMUX = "sess-archived-tmux"
        const val OTHER_PROJECT = "sess-other-project"
        const val CONTINUED = "sess-continued"
        const val REFUSAL = "example refusal from orreryd"
        const val NO_ANSWER = "The server did not answer, so the move may have gone through. Reading the epic again…"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val MOVE_PATH = "/orrery.v1.Epics/Move"
        const val CONTINUE_PATH = "/orrery.v1.Epics/Continue"
        const val GET_PATH = "/orrery.v1.Epics/Get"
        val EXCLUDED_LABELS = listOf("shell pane", "archived pane", "archived headless", "stopped headless", "other project")
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The steerings the sheet reported through onSteered, in order. */
    private val steered = CopyOnWriteArrayList<String>()

    private fun tmuxPane(id: String, orreryId: String, cwd: String, label: String, state: String): PaneProto =
        PaneProto.newBuilder()
            .setId(id)
            .setOrreryId(orreryId)
            .setCwd(cwd)
            .setLabel(label)
            .setState(state)
            .build()

    private fun headlessSession(id: String, dir: String, label: String, state: String, archived: Boolean = false) =
        HeadlessSessionProto.newBuilder()
            .setId(id)
            .setDir(dir)
            .setLabel(label)
            .setState(state)
            .setArchived(archived)
            .build()

    /** The conversations of the project, and ones that must not be offered: a shell, archived, stopped, another project. */
    private fun serveConversations(server: FakeOrreryServer) {
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane("%1", STEERER, "$REPO/steerer", "steerer label", "working"))
                .addPanes(tmuxPane("%2", LIVE_TMUX, "$REPO.worktrees/feature", "live tmux", "idle"))
                .addPanes(tmuxPane("%3", "", REPO, "shell pane", "idle"))
                .addPanes(tmuxPane("%4", ARCHIVED_TMUX, REPO, "archived pane", "idle"))
                .addPanes(tmuxPane("%5", OTHER_PROJECT, "/home/user/projects/other-repo", "other project", "idle"))
                .build(),
        )
        server.respond(
            LIST_HEADLESS,
            ListHeadlessResponse.newBuilder()
                .addSessions(headlessSession(LIVE_HEADLESS, "$REPO/other", "live headless", "running"))
                .addSessions(headlessSession(STOPPED_HEADLESS, REPO, "stopped headless", "stopped"))
                .addSessions(headlessSession(ARCHIVED_HEADLESS, REPO, "archived headless", "idle", archived = true))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(SessionProto.newBuilder().setId(ARCHIVED_TMUX).setArchived(true))
                .build(),
        )
    }

    private fun epicGetResponse(steering: String): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(EpicProto.newBuilder().setId(EPIC).setSlug(SLUG).setSteering(steering))
            .build()

    private fun setSheet(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            val send = rememberSteerSend(client, EPIC) { steered.add(it) }
            SteerMenuSheet(
                client = client,
                epicId = EPIC,
                slug = SLUG,
                repository = REPO,
                steering = STEERER,
                send = send,
                onDismiss = {},
            )
        }
    }

    private fun awaitConversations() {
        composeRule.awaitUntil("the conversations load") { nodeExists("steer-row-$LIVE_TMUX") }
    }

    private fun nodeExists(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    private fun enabled(tag: String): Boolean =
        !composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.contains(SemanticsProperties.Disabled)

    /** The text a row shows, its title and its second line, joined with a space. */
    private fun rowText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    /**
     * Taps [tag] through its click action. A touch injected into the sheet's window does not reach the sheet's content
     * under Robolectric, so a tap inside the sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    @Test
    fun theMenuHeadsTheSteererAndListsOnlyTheProjectsLiveConversationsAndANewOne() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            setSheet(server)
            awaitConversations()

            composeRule.onNodeWithText("Steer $SLUG from").assertTextEquals("Steer $SLUG from")
            composeRule.onNodeWithTag("steer-current").assertIsNotEnabled()
            assertEquals("steerer label steers it now", rowText("steer-current"))
            assertEquals("live tmux feature · idle", rowText("steer-row-$LIVE_TMUX"))
            assertEquals("live headless other · running", rowText("steer-row-$LIVE_HEADLESS"))
            composeRule.onNodeWithTag("steer-new").assertIsEnabled()
            assertEquals(
                "A new conversation in synthetic-repo starts with the contract and the epic’s log",
                rowText("steer-new"),
            )
            for (label in EXCLUDED_LABELS) {
                assertEquals(0, composeRule.onAllNodesWithText(label, substring = true).fetchSemanticsNodes().size)
            }
        }
    }

    @Test
    fun aRowSendsOneMoveWithTheEpicAndTheRowsOrreryIdAndHoldsTheRowsUntilTheReadAnswers() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            val release = CountDownLatch(1)
            server.route(MOVE_PATH) {
                release.await()
                Reply.Message(EpicsMoveResponse.getDefaultInstance(), 0L)
            }
            server.respond(GET_PATH, epicGetResponse(LIVE_HEADLESS))
            setSheet(server)
            awaitConversations()

            tap("steer-row-$LIVE_HEADLESS")
            composeRule.awaitUntil("the rows are held while the move is out") {
                !enabled("steer-row-$LIVE_HEADLESS") && !enabled("steer-row-$LIVE_TMUX") && !enabled("steer-new")
            }
            release.countDown()
            composeRule.awaitUntil("the re-read names the steerer") { steered.isNotEmpty() }

            val moves = server.requests.filter { it.rawPath == MOVE_PATH }
            assertEquals(1, moves.size)
            val request = EpicsMoveRequest.parseFrom(moves.single().body)
            assertEquals(EPIC, request.id)
            assertEquals(LIVE_HEADLESS, request.session)
            assertEquals(1, server.requests.count { it.rawPath == GET_PATH })
            assertEquals(listOf(LIVE_HEADLESS), steered.toList())
        }
    }

    @Test
    fun theNewRowSendsOneContinueForTheEpicAndAnswersWhatTheReadNames() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            server.respond(CONTINUE_PATH, EpicsContinueResponse.newBuilder().setSession(CONTINUED).build())
            server.respond(GET_PATH, epicGetResponse(CONTINUED))
            setSheet(server)
            awaitConversations()

            tap("steer-new")
            composeRule.awaitUntil("the re-read names the started conversation") { steered.isNotEmpty() }

            val continues = server.requests.filter { it.rawPath == CONTINUE_PATH }
            assertEquals(1, continues.size)
            assertEquals(EPIC, EpicsContinueRequest.parseFrom(continues.single().body).id)
            assertEquals(0, server.requests.count { it.rawPath == MOVE_PATH })
            assertEquals(listOf(CONTINUED), steered.toList())
        }
    }

    @Test
    fun aRefusedMoveShowsTheServersMessageWithTheRowsOfferedAndNoRead() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            server.fail(MOVE_PATH, Code.FAILED_PRECONDITION, REFUSAL)
            setSheet(server)
            awaitConversations()

            tap("steer-row-$LIVE_TMUX")
            composeRule.awaitUntil("the refusal shows") { nodeExists("steer-note") }

            composeRule.onNodeWithTag("steer-note").assertTextEquals(REFUSAL)
            composeRule.onNodeWithTag("steer-row-$LIVE_TMUX").assertIsEnabled()
            composeRule.onNodeWithTag("steer-new").assertIsEnabled()
            assertEquals(0, server.requests.count { it.rawPath == GET_PATH })
            assertTrue(steered.isEmpty())
        }
    }

    @Test
    fun aDroppedMoveSaysNoAnswerKeepsTheRowsHeldAndOffersThemAgainWithTheReadsSteerer() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            server.drop(MOVE_PATH)
            val release = CountDownLatch(1)
            server.route(GET_PATH) {
                release.await()
                Reply.Message(epicGetResponse(LIVE_TMUX), 0L)
            }
            setSheet(server)
            awaitConversations()

            tap("steer-row-$LIVE_HEADLESS")
            composeRule.awaitUntil("the no-answer note shows") { nodeExists("steer-note") }

            composeRule.onNodeWithTag("steer-note").assertTextEquals(NO_ANSWER)
            assertFalse(enabled("steer-row-$LIVE_TMUX"))
            assertFalse(enabled("steer-new"))
            release.countDown()
            composeRule.awaitUntil("the re-read's steerer is marked") {
                nodeExists("steer-current") && rowText("steer-current") == "live tmux steers it now"
            }

            assertTrue(enabled("steer-new"))
            assertTrue(enabled("steer-row-$LIVE_HEADLESS"))
            assertFalse(nodeExists("steer-row-$LIVE_TMUX"))
            assertFalse(nodeExists("steer-note"))
            assertEquals(1, server.requests.count { it.rawPath == MOVE_PATH })
            assertEquals(1, server.requests.count { it.rawPath == GET_PATH })
            assertTrue(steered.isEmpty())
        }
    }
}
