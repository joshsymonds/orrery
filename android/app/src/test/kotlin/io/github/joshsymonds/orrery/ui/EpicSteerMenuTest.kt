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
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicSteerMenuTest {
    private companion object {
        const val REPO = "/home/user/projects/synthetic-repo"
        const val EPIC = "synthetic-repo/synthetic-epic"
        const val SLUG = "synthetic-epic"
        const val STEERER = "sess-steerer"
        const val LIVE_TMUX = "sess-live-tmux"
        const val LIVE_HEADLESS = "sess-live-headless"
        const val STOPPED_HEADLESS = "sess-stopped-headless"
        const val OTHER_PROJECT = "sess-other-project"
        const val NAMED_BY_READ = "sess-named-by-read"
        const val MOVE_PATH = "/orrery.v1.Epics/Move"
        const val GET_PATH = "/orrery.v1.Epics/Get"
        const val HISTORY_PATH = "/orrery.v1.Epics/History"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val MORE_ITEM = "Steer it from another conversation"
        val EXCLUDED_LABELS = listOf("shell pane", "stopped headless", "other project")
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The steerings the screen's More reported through onSteered, in order. */
    private val steered = CopyOnWriteArrayList<String>()

    private fun tmuxPane(id: String, orreryId: String, cwd: String, label: String, state: String): PaneProto =
        PaneProto.newBuilder()
            .setId(id)
            .setOrreryId(orreryId)
            .setCwd(cwd)
            .setLabel(label)
            .setState(state)
            .build()

    private fun headlessSession(id: String, dir: String, label: String, state: String) =
        HeadlessSessionProto.newBuilder()
            .setId(id)
            .setDir(dir)
            .setLabel(label)
            .setState(state)
            .build()

    /** The conversations of the project, and ones that must not be offered: a shell, stopped, another project. */
    private fun serveConversations(server: FakeOrreryServer) {
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane("%1", STEERER, "$REPO/steerer", "steerer label", "working"))
                .addPanes(tmuxPane("%2", LIVE_TMUX, "$REPO.worktrees/feature", "live tmux", "idle"))
                .addPanes(tmuxPane("%3", "", REPO, "shell pane", "idle"))
                .addPanes(tmuxPane("%5", OTHER_PROJECT, "/home/user/projects/other-repo", "other project", "idle"))
                .build(),
        )
        server.respond(
            LIST_HEADLESS,
            ListHeadlessResponse.newBuilder()
                .addSessions(headlessSession(LIVE_HEADLESS, "$REPO/other", "live headless", "running"))
                .addSessions(headlessSession(STOPPED_HEADLESS, REPO, "stopped headless", "stopped"))
                .build(),
        )
        server.respond(SESSIONS_LIST, ListResponse.getDefaultInstance())
    }

    /** The epic as the server reads it: its steering, and the repository the epic belongs to on the response. */
    private fun epicGet(steering: String): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                EpicProto.newBuilder()
                    .setId(EPIC)
                    .setSlug(SLUG)
                    .setSource("orreryd")
                    .setLive(true)
                    .setSteering(steering),
            )
            .setRepository(REPO)
            .build()

    /** The screen the record's More belongs to: the epic, with the More in its foot. */
    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC,
                onBack = {},
                onOpenStep = {},
                onOpenContract = {},
                onTalk = {},
                onSteered = { steered.add(it) },
            )
        }
    }

    /** Lets the main thread and every request it started finish, so a second call would be on the server before a count. */
    private fun settle() {
        Quiescence.awaitUntil("the work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    private fun nodeExists(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    /** The text a row shows, its title and its second line, joined with a space. */
    private fun rowText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    /** Taps the More's button, which the screen draws in its foot. */
    private fun openMore() {
        composeRule.awaitUntil("the More is drawn once the epic loads") { nodeExists("epic-more") }
        composeRule.onNodeWithTag("epic-more", useUnmergedTree = true).performClick()
    }

    /** Opens the steering sheet through the More's sheet, the way a viewer does. */
    private fun openSteerSheet() {
        openMore()
        composeRule.awaitUntil("the More sheet opens") { nodeExists("epic-steer") }
        tapPopup("epic-steer")
    }

    /**
     * Taps [tag] through its click action. A touch injected into a popup's or a sheet's window does not reach its content
     * under Robolectric, so a tap inside one is sent as the accessibility click that the same tap runs.
     */
    private fun tapPopup(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    @Test
    fun theMoreOpensASheetHoldingTheSteerCommand() {
        FakeOrreryServer().use { server ->
            server.respond(GET_PATH, epicGet(STEERER))
            server.respond(HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
            setScreen(server)

            openMore()
            composeRule.awaitUntil("the More sheet opens") { nodeExists("epic-steer") }
            composeRule.onNodeWithTag("epic-steer").assertTextEquals(MORE_ITEM)
        }
    }

    @Test
    fun theSteerCommandOpensTheSteerMenuSheetHeadedForTheEpicAndListingItsChoices() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            server.respond(GET_PATH, epicGet(STEERER))
            server.respond(HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
            setScreen(server)

            openSteerSheet()
            composeRule.awaitUntil("the conversations load") { nodeExists("steer-row-$LIVE_TMUX") }

            composeRule.onNodeWithText("Steer $SLUG from").assertTextEquals("Steer $SLUG from")
            composeRule.onNodeWithTag("steer-current").assertIsNotEnabled()
            assertEquals("steerer label steers it now", rowText("steer-current"))
            assertEquals("live tmux feature · idle", rowText("steer-row-$LIVE_TMUX"))
            assertEquals("live headless other · running", rowText("steer-row-$LIVE_HEADLESS"))
            composeRule.onNodeWithTag("steer-new").assertIsEnabled()
            for (label in EXCLUDED_LABELS) {
                assertEquals(0, composeRule.onAllNodesWithText(label, substring = true).fetchSemanticsNodes().size)
            }
        }
    }

    @Test
    fun aRowSendsOneMoveAndOneReadAndReachesOnSteeredWithTheSessionTheReadNamesAndTheSheetShut() {
        FakeOrreryServer().use { server ->
            serveConversations(server)
            val reads = AtomicInteger()
            server.route(GET_PATH) {
                // The first read loads the screen with the steerer; the read after the move names the steerer it reports,
                // which is not the row chosen, so the menu must report the read and not the move's answer.
                val steering = if (reads.incrementAndGet() == 1) STEERER else NAMED_BY_READ
                Reply.Message(epicGet(steering), 0L)
            }
            server.respond(MOVE_PATH, EpicsMoveResponse.getDefaultInstance())
            server.respond(HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
            setScreen(server)

            openSteerSheet()
            composeRule.awaitUntil("the conversations load") { nodeExists("steer-row-$LIVE_HEADLESS") }

            tapPopup("steer-row-$LIVE_HEADLESS")
            composeRule.awaitUntil("the move settles on a steering") { steered.isNotEmpty() }
            settle()

            val moves = server.requests.filter { it.rawPath == MOVE_PATH }
            assertEquals(1, moves.size)
            val request = EpicsMoveRequest.parseFrom(moves.single().body)
            assertEquals(EPIC, request.id)
            assertEquals(LIVE_HEADLESS, request.session)
            assertEquals(2, server.requests.count { it.rawPath == GET_PATH })
            assertEquals(listOf(NAMED_BY_READ), steered.toList())
            assertEquals(0, composeRule.onAllNodesWithTag("steer-menu", useUnmergedTree = true).fetchSemanticsNodes().size)
        }
    }
}
