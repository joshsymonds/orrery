package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// The round 8 bracket a phone sheet opened from a work link sits in: the band under the head, the spine down the sheet's
// left side and the foot under it, drawn while the sheet is open and not while it is closed. Read through the semantics
// tree alone, with the conversation's records invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class SheetBracketTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val PANE_ID = "%7"
        const val ORRERY_ID = "synthetic-orrery-id"
        const val REPOSITORY = "/home/user/lighthouse"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val DESIGN_STATE = "0 of 1 accepted"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val PANE_WATCH_PATH = "/orrery.v1.Panes/WatchConversation"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

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

    /** Streams the headless session [SESSION_ID], which has nothing running. */
    private fun serveSession(server: FakeOrreryServer) {
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir(REPOSITORY)
            .setLabel("synthetic session")
            .setState("working")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(session)
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
            .setCwd(REPOSITORY)
            .setCommand("claude")
            .setState("waiting")
            .setLabel("synthetic pane")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(pane).build())
    }

    /** The designs the conversation's head reads, with no epics and no contracts. */
    private fun serveWork(server: FakeOrreryServer, designs: List<Design>) {
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addAllDesigns(designs).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    private fun design(id: String, title: String, conversation: String): Design = Design.newBuilder()
        .setId(id)
        .setTitle(title)
        .setConversation(conversation)
        .setCreated(NOW - 600)
        .setState(DESIGN_STATE)
        .build()

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** Opens the design's sheet from its work link, which the head draws once the conversation's work has loaded. */
    private fun openDesignFromItsLink() {
        awaitTag(DESIGN_LINK_TAG)
        composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
        awaitTag(PHONE_SHEET_TAG)
    }

    /** The node tagged [tag] in the unmerged tree, which must be drawn. */
    private fun node(tag: String): SemanticsNode =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).assertExists().fetchSemanticsNode()

    /** Each of the band, the spine and the foot is drawn [count] times, once while the sheet is open and never closed. */
    private fun assertBracketCount(count: Int) {
        for (tag in listOf(SHEET_BAND_TAG, SHEET_SPINE_TAG, SHEET_FOOT_TAG)) {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(count)
        }
    }

    @Test
    fun sheetBracketLightsTheLinkAndDrawsTheBandUnderTheHead() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()
            val head = node("head-title")
            val band = node(SHEET_BAND_TAG)
            val sheet = node(PHONE_SHEET_TAG)
            assertTrue("the band begins under the head's title row", band.boundsInRoot.top >= head.boundsInRoot.bottom)
            assertTrue("the band lies above the sheet", band.boundsInRoot.top <= sheet.boundsInRoot.top)
        }
    }

    @Test
    fun sheetBracketDrawsTheSpineDownTheSheetsLeftSide() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()

            val sheet = node(PHONE_SHEET_TAG)
            val spine = node(SHEET_SPINE_TAG)
            assertTrue("the spine lies at or left of the sheet", spine.boundsInRoot.left <= sheet.boundsInRoot.left)
            assertTrue(
                "the spine spans the sheet's height",
                spine.boundsInRoot.top <= sheet.boundsInRoot.top && spine.boundsInRoot.bottom >= sheet.boundsInRoot.bottom,
            )
        }
    }

    @Test
    fun sheetBracketDrawsTheFootUnderTheSheet() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()

            val sheet = node(PHONE_SHEET_TAG)
            val foot = node(SHEET_FOOT_TAG)
            assertTrue("the foot lies under the sheet", foot.boundsInRoot.top >= sheet.boundsInRoot.bottom)
        }
    }

    @Test
    fun sheetBracketIsDrawnAroundTheSheetInAPane() {
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)))
            setPane(server)
            openDesignFromItsLink()

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()
            val head = node("head-title")
            val sheet = node(PHONE_SHEET_TAG)
            val band = node(SHEET_BAND_TAG)
            val spine = node(SHEET_SPINE_TAG)
            val foot = node(SHEET_FOOT_TAG)
            assertTrue("the band begins under the head's title row", band.boundsInRoot.top >= head.boundsInRoot.bottom)
            assertTrue("the band lies above the sheet", band.boundsInRoot.top <= sheet.boundsInRoot.top)
            assertTrue("the spine lies at or left of the sheet", spine.boundsInRoot.left <= sheet.boundsInRoot.left)
            assertTrue(
                "the spine spans the sheet's height",
                spine.boundsInRoot.top <= sheet.boundsInRoot.top && spine.boundsInRoot.bottom >= sheet.boundsInRoot.bottom,
            )
            assertTrue("the foot lies under the sheet", foot.boundsInRoot.top >= sheet.boundsInRoot.bottom)
        }
    }

    @Test
    fun sheetBracketIsNotDrawnWhileTheSheetIsClosed() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()
            assertBracketCount(1)

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()

            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(0)
            assertBracketCount(0)
        }
    }
}
