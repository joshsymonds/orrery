package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.test.assertContentDescriptionEquals
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
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
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// The close of the round 8 phone sheet: the X at the right of its head closes the sheet and its bracket, unselects the
// work link it lit and keeps the conversation, and System Back does the same. Read through the semantics tree alone, with
// the conversation's records invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class SheetCloseTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val REPOSITORY = "/home/user/lighthouse"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val DESIGN_STATE = "0 of 1 accepted"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
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

    /** Each of the band, the spine and the foot is drawn [count] times. */
    private fun assertBracketCount(count: Int) {
        for (tag in listOf(SHEET_BAND_TAG, SHEET_SPINE_TAG, SHEET_FOOT_TAG)) {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(count)
        }
    }

    @Test
    fun sheetCloseIsTheXAtTheRightOfTheHead() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()

            composeRule.onNode(
                hasTestTag(SHEET_HEAD_TAG) and hasAnyDescendant(hasTestTag(SHEET_CLOSE_TAG)),
                useUnmergedTree = true,
            ).assertExists()
            composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).assertContentDescriptionEquals("Close")
            val name = node(SHEET_NAME_TAG)
            val close = node(SHEET_CLOSE_TAG)
            assertTrue("the X lies right of the name", close.boundsInRoot.left > name.boundsInRoot.right)
        }
    }

    @Test
    fun sheetCloseClosesTheSheetAndItsBracketAndUnselectsTheLink() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()
            assertBracketCount(1)

            composeRule.onNodeWithTag(SHEET_CLOSE_TAG).performClick()

            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(0)
            assertBracketCount(0)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsNotSelected()
            composeRule.onNodeWithTag("head-title", useUnmergedTree = true).assertExists()
        }
    }

    @Test
    fun sheetCloseBackClosesTheSheetAndKeepsTheConversation() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)))
            setSession(server)
            openDesignFromItsLink()
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()
            assertBracketCount(1)

            composeRule.activity.onBackPressedDispatcher.onBackPressed()

            composeRule.awaitUntil("the sheet to close") {
                composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
            }
            assertBracketCount(0)
            composeRule.onNodeWithTag("head-title", useUnmergedTree = true).assertExists()
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsNotSelected()
        }
    }
}
