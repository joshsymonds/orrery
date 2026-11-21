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
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val REPLY_COUNT = 40
private const val SYNTHETIC_GOAL = "synthetic goal text"
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
private const val COMPOSER_TAG = "composer"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val SERVING_HOST = "example-host"

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1000dp")
class PhoneHeadSessionScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun stateOf(state: String, goal: String = "", putOff: Boolean = false): ConversationState =
        ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(goal)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/tidepool")
                    .setLabel("Tide table cache warm-up")
                    .setState(state)
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN)
                    .setPutOff(putOff),
            )
            .build()

    /** The session's first delta: [REPLY_COUNT] replies in [state] with [goal]. */
    private fun fullDelta(state: String = "done", goal: String = "", putOff: Boolean = false): SessionsWatchConversationResponse =
        SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .addAllItems((1..REPLY_COUNT).map { ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply $it").build() })
            .setState(stateOf(state, goal, putOff))
            .build()

    private fun streamOf(vararg messages: MessageLite): Reply.Stream = Reply.Stream(messages.toList(), StreamEnd.Open)

    /**
     * Scripts the watch stream as a session's streams come: the first request gets the first stream, each later request
     * the next, and a request past the script gets an empty stream that stays open. A fresh screen starts a new stream.
     */
    private fun serveStreams(server: FakeOrreryServer, vararg streams: Reply) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) {
            val index = requests.getAndIncrement()
            if (index < streams.size) streams[index] else Reply.Stream(emptyList(), StreamEnd.Open)
        }
    }

    /** One steered epic, rota-export, whose one open question asks; the session id steers it. */
    private fun serveAskingEpic(server: FakeOrreryServer) {
        val epic = Epic.newBuilder()
            .setId("example-repo/rota-export")
            .setRepository("example-repo")
            .setSlug("rota-export")
            .setLive(true)
            .setSteering(SESSION_ID)
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder()
                .setEpic(epic)
                .addQuestions(EpicQuestionProto.newBuilder().setId("Q1").setText("Bump the embedded tz database?"))
                .build(),
        )
    }

    /** The screen's instance, which a restart replaces; the device's hidden choice outlives it. */
    private val screenInstance = mutableIntStateOf(0)

    /** The epics the screen opened, in the order its band lines were tapped. */
    private val opened = mutableListOf<String>()

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                key(screenInstance.intValue) { SessionScreen(SESSION_ID, client, onOpenEpic = { opened += it }) }
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
    fun theServingHostsMetersDrawInTheSharedMachineBand() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(fullDelta()))
            server.respond(
                HOST_PATH,
                HostStatusResponse.newBuilder()
                    .setHost(SERVING_HOST)
                    .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
                    .build(),
            )
            setScreen(server)
            awaitNewestReply()

            composeRule.awaitExactlyOne(hasTestTag("machine-band"))
            composeRule.onNode(hasTestTag("machine-host") and hasAnyAncestor(hasTestTag("machine-band")))
                .assertTextEquals(SERVING_HOST)
            composeRule.onNode(hasTestTag("meter:cpu") and hasAnyAncestor(hasTestTag("machine-band"))).assertIsDisplayed()
        }
    }

    @Test
    fun headIsOpenAtTheNewestLine() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(fullDelta()))
            setScreen(server)
            awaitNewestReply()

            composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
            composeRule.onAllNodesWithTag(COMPACT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun readingOlderLinesFoldsTheHeadToItsCompactLine() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(fullDelta(goal = SYNTHETIC_GOAL)))
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
            serveStreams(server, streamOf(fullDelta()))
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
            serveStreams(server, streamOf(fullDelta(goal = SYNTHETIC_GOAL)))
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
            serveStreams(server, streamOf(fullDelta()), streamOf(fullDelta()))
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
            serveStreams(server, streamOf(fullDelta(goal = SYNTHETIC_GOAL)))
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
            serveStreams(server, streamOf(fullDelta()))
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
    fun aFoldedWaitingSessionNamesItsOwnAskWithTheAttentionColour() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(fullDelta(state = "waiting")))
            setScreen(server)
            awaitNewestReply()
            scrollToOldest()

            composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
            composeRule.onNodeWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).assertTextEquals("tidepool asks")
            assertEquals(listOf(OrreryTokens.Role.attention), askingColours())
        }
    }

    @Test
    fun aFoldedWaitingSessionThatIsPutOffDoesNotAskItsOwnName() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(fullDelta(state = "waiting", putOff = true)))
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
            serveStreams(server, streamOf(fullDelta()))
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
            serveStreams(server, streamOf(fullDelta(state = "working", goal = SYNTHETIC_GOAL)))
            setScreen(server)
            awaitNewestReply()

            composeRule.onNodeWithTag(BAND_GOAL_TAG).assertIsDisplayed()
            val goal = composeRule.onNodeWithText(SYNTHETIC_GOAL, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val band = composeRule.onNodeWithTag(BAND_GOAL_TAG).fetchSemanticsNode().boundsInRoot
            val composer = composeRule.onNodeWithTag(COMPOSER_TAG).fetchSemanticsNode().boundsInRoot
            assertTrue("the goal sits inside the band", goal.top >= band.top && goal.bottom <= band.bottom)
            assertTrue("the band sits above the composer", band.bottom <= composer.top)
        }
    }
}
