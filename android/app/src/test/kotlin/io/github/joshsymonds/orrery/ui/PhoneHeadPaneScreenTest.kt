package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val PANE_HOST = "example-host"
private const val ORRERY_ID = "synthetic-orrery-id"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val FIXTURE_SEEN = 4102444900L
private const val REPLY_COUNT = 40
private const val CONVERSATION_LIST_TAG = "conversation-list"
private const val HEAD_TAG = "head"
private const val HEAD_MORE_TAG = "head-more"
private const val MENU_HIDE_TAG = "head-menu-hide"
private const val BAND_TAG = "band"
private const val BAND_GOAL_TAG = "band-goal"
private const val BAND_HIDE_TAG = "band-hide"
private const val COMPACT_TAG = "compact-head"
private const val COMPACT_ASKING_TAG = "compact-asking"
private const val COMPACT_BADGE_TAG = "compact-badge:example-repo/rota-export"
private const val BAND_LINE_TAG = "band-line:example-repo/rota-export"
private const val METERS_TAG = "head-meters"
private const val OTHER_HOST_TAG = "head-other-host"
private const val SYNTHETIC_GOAL = "synthetic goal text"
private const val OTHER_HOST = "other-host"
private const val STATUS_LINE_TAG = "status-line"
private const val COMPOSER_TAG = "composer"

private val HOST_BODY: HostStatusResponse = HostStatusResponse.newBuilder()
    .setHost(PANE_HOST)
    .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
    .build()

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1000dp")
class PhoneHeadPaneScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun paneList(state: String = "done", host: String = PANE_HOST, putOff: Boolean = false): PanesListResponse =
        PanesListResponse.newBuilder()
            .addPanes(
                PaneProto.newBuilder()
                    .setId(PANE_ID)
                    .setHost(host)
                    .setSession("example-session")
                    .setWindow(1)
                    .setPane(2)
                    .setCwd("/home/user/tidepool")
                    .setCommand("claude")
                    .setState(state)
                    .setSummary("Example summary.")
                    .setLabel("Tide table cache warm-up")
                    .setChanged(4102444800L)
                    .setSeen(FIXTURE_SEEN)
                    .setQuestion("")
                    .setQuestionHash("abc123")
                    .setOrreryId(ORRERY_ID)
                    .setPutOff(putOff),
            )
            .build()

    private fun conversationOf(goal: String = "", replies: Int = REPLY_COUNT): ConversationResponse =
        ConversationResponse.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(goal)
            .addAllItems((1..replies).map { ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply $it").build() })
            .build()

    /** Scripts a pane in [state] with [conversation]. The stream is refused, so the screen polls, as a pane without a stream does. */
    private fun servePane(
        server: FakeOrreryServer,
        state: String = "done",
        host: String = PANE_HOST,
        putOff: Boolean = false,
        conversation: ConversationResponse = conversationOf(),
    ) {
        server.route(STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        server.respond(PANES_PATH, paneList(state, host, putOff))
        server.respond(CONVERSATION_PATH, conversation)
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    /** The screen's instance, which a restart replaces; the device's hidden choice outlives it. */
    private val screenInstance = mutableIntStateOf(0)

    /** The epics the screen opened, in the order its band lines were tapped. */
    private val opened = mutableListOf<String>()

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                key(screenInstance.intValue) {
                    PaneScreen(PANE_ID, client, clock = { FIXED_NOW_SECONDS }, onOpenEpic = { opened += it })
                }
            }
        }
    }

    /** The colours the compact line's asking words draw in. */
    private fun askingColours(): List<Color> =
        composeRule.onNodeWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).fetchSemanticsNode()
            .config[SemanticsProperties.Text].single().spanStyles.map { it.item.color }

    /** Starts a fresh screen instance, as a restart does, over the same device store. */
    private fun restartScreen() {
        composeRule.runOnUiThread { screenInstance.intValue++ }
        composeRule.waitForIdle()
    }

    /** One steered epic, rota-export, whose one open question asks; its id is what the pane's orreryId steers. */
    private fun serveAskingEpic(server: FakeOrreryServer) {
        val epic = Epic.newBuilder()
            .setId("example-repo/rota-export")
            .setRepository("example-repo")
            .setSlug("rota-export")
            .setLive(true)
            .setSteering(ORRERY_ID)
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder()
                .setEpic(epic)
                .addQuestions(EpicQuestionProto.newBuilder().setId("Q1").setText("Bump the embedded tz database?"))
                .build(),
        )
    }

    private fun stateIs(state: String) = SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state)

    private fun awaitNewestReply() {
        composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
    }

    private fun scrollToOldest() {
        composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
        composeRule.waitForIdle()
    }

    private fun scrollToNewest() {
        composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(REPLY_COUNT - 1)
        composeRule.waitForIdle()
    }

    @Test
    fun headIsOpenAtTheNewestLine() {
        FakeOrreryServer().use { server ->
            servePane(server)
            setScreen(server)
            awaitNewestReply()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
            composeRule.onAllNodesWithTag(COMPACT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun readingOlderLinesFoldsTheHeadToItsCompactLine() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationOf(goal = SYNTHETIC_GOAL))
            setScreen(server)
            awaitNewestReply()

            scrollToOldest()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("reading"))
            composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
            composeRule.onAllNodesWithTag(BAND_GOAL_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun scrollingBackToTheNewestLineOpensTheFoldedHead() {
        FakeOrreryServer().use { server ->
            servePane(server)
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()
            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("reading"))

            scrollToNewest()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
        }
    }

    @Test
    fun tappingTheFoldedLineOpensTheHeadWhileOlderLinesAreStillRead() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationOf(goal = SYNTHETIC_GOAL))
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()
            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("reading"))

            composeRule.onNodeWithTag(COMPACT_TAG).performClick()
            composeRule.waitForIdle()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
            composeRule.onNodeWithTag(BAND_GOAL_TAG).assertIsDisplayed()
        }
    }

    @Test
    fun hideFromMoreFoldsTheHeadAndAFreshScreenStaysFoldedUntilTheLineIsTapped() {
        FakeOrreryServer().use { server ->
            servePane(server)
            setScreen(server)
            awaitNewestReply()
            composeRule.onNodeWithTag(HEAD_MORE_TAG).performClick()
            composeRule.onNodeWithTag(MENU_HIDE_TAG).performClick()
            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("hidden"))

            restartScreen()
            awaitNewestReply()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("hidden"))
            composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()

            composeRule.onNodeWithTag(COMPACT_TAG).performClick()
            composeRule.waitForIdle()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
        }
    }

    @Test
    fun hideFromTheBandFoldsTheHead() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationOf(goal = SYNTHETIC_GOAL))
            setScreen(server)
            awaitNewestReply()

            composeRule.onNodeWithTag(BAND_HIDE_TAG).performClick()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("hidden"))
            composeRule.onAllNodesWithTag(BAND_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun aSteeredEpicAskingFoldsToItsAskingLine() {
        FakeOrreryServer().use { server ->
            servePane(server)
            serveAskingEpic(server)
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()

            composeRule.awaitUntil { composeRule.onAllNodesWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).fetchSemanticsNodes().size == 1 }
            composeRule.onNodeWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).assertTextEquals("rota-export asks")
            composeRule.onNodeWithTag(COMPACT_BADGE_TAG, useUnmergedTree = true).assertTextEquals("rota?")
        }
    }

    @Test
    fun aFoldedWaitingPaneNamesItsOwnAskWithTheAttentionColour() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "waiting")
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()

            composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
            composeRule.onNodeWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).assertTextEquals("tidepool asks")
            assertEquals(listOf(OrreryTokens.Role.attention), askingColours())
        }
    }

    @Test
    fun aFoldedWaitingPaneThatIsPutOffDoesNotAskItsOwnName() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "waiting", putOff = true)
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()

            composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
            composeRule.onAllNodesWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun tappingASteeredEpicsBandLineOpensThatEpic() {
        FakeOrreryServer().use { server ->
            servePane(server)
            serveAskingEpic(server)
            setScreen(server)
            awaitNewestReply()
            // The asks land while the reader is at the newest line: the head stays open and draws them in its band.
            composeRule.awaitExactlyOne(hasTestTag(BAND_LINE_TAG))
            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))

            composeRule.onNodeWithTag(BAND_LINE_TAG).performClick()
            composeRule.waitForIdle()

            assertEquals(listOf("example-repo/rota-export"), opened)
        }
    }

    @Test
    fun theGoalIsInTheBandAboveTheComposerAndNotInTheDock() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "working", conversation = conversationOf(goal = SYNTHETIC_GOAL))
            setScreen(server)
            awaitNewestReply()

            composeRule.onNodeWithTag(BAND_GOAL_TAG).assertIsDisplayed()
            val goal = composeRule.onNodeWithText(SYNTHETIC_GOAL, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val band = composeRule.onNodeWithTag(BAND_GOAL_TAG).fetchSemanticsNode().boundsInRoot
            val composer = composeRule.onNodeWithTag(COMPOSER_TAG).fetchSemanticsNode().boundsInRoot
            composeRule.onNodeWithTag(STATUS_LINE_TAG).assertIsDisplayed()
            assertTrue("the goal sits inside the band", goal.top >= band.top && goal.bottom <= band.bottom)
            assertTrue("the band sits above the composer", band.bottom <= composer.top)
        }
    }

    @Test
    fun anotherHostShowsItsNameAndNoMeters() {
        FakeOrreryServer().use { server ->
            servePane(server, host = OTHER_HOST)
            server.respond(HOST_PATH, HOST_BODY)
            setScreen(server)
            awaitNewestReply()

            composeRule.awaitExactlyOne(hasTestTag(OTHER_HOST_TAG))
            composeRule.onNodeWithText(OTHER_HOST).assertIsDisplayed()
            composeRule.onNodeWithText("on another host: no meters").assertIsDisplayed()
            composeRule.onAllNodesWithTag(METERS_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun theLocalHostShowsItsMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            setScreen(server)
            awaitNewestReply()

            composeRule.awaitExactlyOne(hasTestTag(METERS_TAG))
            composeRule.onAllNodesWithTag(OTHER_HOST_TAG).assertCountEquals(0)
        }
    }
}
