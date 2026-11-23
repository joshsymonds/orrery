package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.BackgroundTask as BackgroundTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A conversation's work links on its screens: the links of a design it works, the activity lines above its dock, and the
// design's sheet, drawn from invented rows at a fixed clock and read through the semantics tree alone.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class WorkLinkScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val PANE_ID = "%7"
        const val ORRERY_ID = "synthetic-orrery-id"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val PANE_WATCH_PATH = "/orrery.v1.Panes/WatchConversation"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
        }
    }

    private fun setPane(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { NOW }) }
        }
    }

    /** Streams the headless session [SESSION_ID], with [background] as its background tasks. */
    private fun serveSession(server: FakeOrreryServer, background: List<BackgroundTaskProto>) {
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/lighthouse")
            .setLabel("synthetic session")
            .setState("working")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(session)
            .addAllBackground(background)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(state)
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    /** Serves the pane [PANE_ID] with the orrery id [ORRERY_ID], and no stream, so its screen polls. */
    private fun servePane(server: FakeOrreryServer) {
        server.route(PANE_WATCH_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        val pane = PaneProto.newBuilder()
            .setId(PANE_ID)
            .setOrreryId(ORRERY_ID)
            .setHost("example-host")
            .setCwd("/home/user/lighthouse")
            .setCommand("claude")
            .setState("waiting")
            .setLabel("synthetic pane")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(pane).build())
    }

    /** No epics, and one proposed contract that [conversation] proposed. */
    private fun serveHeadLists(server: FakeOrreryServer, conversation: String) {
        val contract = Contract.newBuilder()
            .setId(CONTRACT_ID)
            .setTitle("Synthetic contract")
            .setCare("limited")
            .setState("proposed")
            .setVersion(1L)
            .setConversation(conversation)
            .build()
        server.respond(EPICS_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.newBuilder().addContracts(contract).build())
    }

    /** One design that [conversation] works, with its one contract, [CONTRACT_ID]. */
    private fun serveDesign(server: FakeOrreryServer, conversation: String) {
        val contract = DesignContract.newBuilder()
            .setId(CONTRACT_ID)
            .setTitle("Synthetic contract")
            .setRepository("synthetic-owner")
            .setState("proposed")
            .setPosition(1L)
            .build()
        val design = Design.newBuilder()
            .setId(DESIGN_ID)
            .setTitle(DESIGN_TITLE)
            .setConversation(conversation)
            .setCreated(NOW - 600)
            .setOfOne(false)
            .setState("0 of 1 accepted")
            .addContracts(contract)
            .build()
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addDesigns(design).build())
    }

    private fun backgroundTask(id: String, type: String, status: String): BackgroundTaskProto =
        BackgroundTaskProto.newBuilder()
            .setId(id)
            .setType(type)
            .setStatus(status)
            .setDescription("synthetic $type task")
            .setStarted(NOW - 90)
            .build()

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** A node whose test tag starts with [prefix]. */
    private fun tagStartingWith(prefix: String): SemanticsMatcher =
        SemanticsMatcher("test tag starts with $prefix") {
            it.config.contains(SemanticsProperties.TestTag) && it.config[SemanticsProperties.TestTag].startsWith(prefix)
        }

    /** No tile strip and no tile is drawn: the work links replace the rail the strip carried. */
    private fun assertNoTileStrip() {
        composeRule.onAllNodes(hasTestTag("tile-strip"), useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodes(tagStartingWith("tile:"), useUnmergedTree = true).assertCountEquals(0)
    }

    /** The node tagged [tag] is drawn inside the head's title row. */
    private fun assertInHeadTitle(tag: String) {
        composeRule.onNode(
            hasTestTag(tag) and hasAnyAncestor(hasTestTag("head-title")),
            useUnmergedTree = true,
        ).assertExists()
    }

    /** The activity lines and the dock share a parent, and the lines come before the dock among its children. */
    private fun assertLinesAboveDock() {
        val lines = composeRule.onNodeWithTag("activity-lines", useUnmergedTree = true).fetchSemanticsNode()
        val dock = composeRule.onNodeWithTag("dock", useUnmergedTree = true).fetchSemanticsNode()
        val parent = checkNotNull(lines.parent) { "the activity lines must have a parent" }
        assertEquals("the dock shares the lines' parent", parent.id, checkNotNull(dock.parent).id)
        val siblings = parent.children.map { it.id }
        assertTrue("the lines come before the dock", siblings.indexOf(lines.id) in 0 until siblings.indexOf(dock.id))
    }

    @Test
    fun theHeadNamesTheDesignAsAWorkLinkInItsTitleAndDrawsNoTileStrip() {
        FakeOrreryServer().use { server ->
            serveSession(server, background = listOf(backgroundTask("synthetic-agent", "agent", "running")))
            serveHeadLists(server, SESSION_ID)
            serveDesign(server, SESSION_ID)
            setSession(server)

            awaitNode(hasTestTag(DESIGN_LINK_TAG))
            assertInHeadTitle(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertTextEquals(DESIGN_TITLE)
            assertNoTileStrip()
        }
    }

    @Test
    fun runningTasksDrawTheirLinesAboveTheDockInTheSameColumn() {
        FakeOrreryServer().use { server ->
            serveSession(
                server,
                background = listOf(
                    backgroundTask("synthetic-agent", "agent", "running"),
                    backgroundTask("synthetic-shell", "shell", "running"),
                ),
            )
            serveHeadLists(server, SESSION_ID)
            setSession(server)

            awaitNode(hasTestTag("activity-lines"))
            awaitNode(hasTestTag("dock"))
            assertLinesAboveDock()
        }
    }

    @Test
    fun aConversationWithNothingRunningDrawsNoActivityLines() {
        FakeOrreryServer().use { server ->
            serveSession(server, background = listOf(backgroundTask("synthetic-done", "agent", "done")))
            serveHeadLists(server, SESSION_ID)
            setSession(server)

            awaitNode(hasTestTag("head-text"))
            composeRule.onAllNodes(hasTestTag("activity-lines"), useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun tappingTheDesignLinkOpensItsSheetWithItsTabAndLightsTheLink() {
        FakeOrreryServer().use { server ->
            serveSession(server, background = emptyList())
            serveHeadLists(server, SESSION_ID)
            serveDesign(server, SESSION_ID)
            setSession(server)

            awaitNode(hasTestTag(DESIGN_LINK_TAG))
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()

            awaitNode(hasTestTag("design-tab:$DESIGN_ID"))
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()
        }
    }

    @Test
    fun aPaneNamesItsDesignAsAWorkLinkInItsTitleAndDrawsNoTileStrip() {
        FakeOrreryServer().use { server ->
            servePane(server)
            serveHeadLists(server, ORRERY_ID)
            serveDesign(server, ORRERY_ID)
            setPane(server)

            awaitNode(hasTestTag(DESIGN_LINK_TAG))
            assertInHeadTitle(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertTextEquals(DESIGN_TITLE)
            assertNoTileStrip()
        }
    }
}
