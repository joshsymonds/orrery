package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performScrollToIndex
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicSession as EpicSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
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
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val FIXTURE_SEEN = 4102444900L
private const val REPLY_COUNT = 40
private const val BAND_GOAL_TAG = "band-goal"
private const val SYNTHETIC_GOAL = "synthetic goal text"
private const val STATUS_LINE_TAG = "status-line"
private const val COMPOSER_TAG = "composer"
private const val CONVERSATION_LIST_TAG = "conversation-list"
private const val COMPACT_ASKING_TAG = "compact-asking"
private const val EPIC_REPOSITORY = "example-repo"
private const val CREATED_EPIC = "example-repo/rota-export"
private const val STEERED_EPIC = "example-repo/tide-bake"
private const val OTHER_CONVERSATION = "synthetic-other-conversation"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1000dp")
class BandGoalScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun paneList(state: String): PanesListResponse =
        PanesListResponse.newBuilder()
            .addPanes(
                PaneProto.newBuilder()
                    .setId(PANE_ID)
                    .setHost(PANE_HOST)
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
                    .setOrreryId(ORRERY_ID),
            )
            .build()

    private fun conversationOf(goal: String): ConversationResponse =
        ConversationResponse.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(goal)
            .addAllItems((1..REPLY_COUNT).map { ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply $it").build() })
            .build()

    /** Scripts a pane in [state] with [conversation]. The stream is refused, so the screen polls, as a pane without a stream does. */
    private fun servePane(server: FakeOrreryServer, state: String, conversation: ConversationResponse) {
        server.route(STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        server.respond(PANES_PATH, paneList(state))
        server.respond(CONVERSATION_PATH, conversation)
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                PaneScreen(PANE_ID, client, clock = { FIXED_NOW_SECONDS })
            }
        }
    }

    private fun epic(id: String, steering: String): Epic =
        Epic.newBuilder()
            .setId(id)
            .setRepository(EPIC_REPOSITORY)
            .setSlug(id.substringAfter('/'))
            .setLive(true)
            .setSteering(steering)
            .build()

    /**
     * Lists two live epics: one another conversation steers, which this pane created, and one this pane steers. Each Get
     * answers its own detail, and the created epic's detail names this pane as the session that created it.
     */
    private fun serveEpics(server: FakeOrreryServer) {
        val created = epic(CREATED_EPIC, steering = OTHER_CONVERSATION)
        val steered = epic(STEERED_EPIC, steering = ORRERY_ID)
        val details = mapOf(
            CREATED_EPIC to EpicsGetResponse.newBuilder()
                .setEpic(created)
                .addQuestions(EpicQuestionProto.newBuilder().setId("Q1").setText("Bump the embedded tz database?"))
                .addSessions(EpicSessionProto.newBuilder().setId(ORRERY_ID).setCall("epic_create").setCreator(true))
                .build(),
            STEERED_EPIC to EpicsGetResponse.newBuilder()
                .setEpic(steered)
                .addQuestions(EpicQuestionProto.newBuilder().setId("Q2").setText("Pin the tz release?"))
                .build(),
        )
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(created).addEpics(steered).build())
        server.route(EPICS_GET_PATH) { request ->
            Reply.Message(details.getValue(EpicsGetRequest.parseFrom(request.body).id), 0L)
        }
    }

    private fun awaitNewestReply() {
        composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
    }

    private fun scrollToOldest() {
        composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
        composeRule.waitForIdle()
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
    fun anEpicThisPaneCreatedButAnotherConversationSteersNeverShows() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "working", conversation = conversationOf(goal = SYNTHETIC_GOAL))
            serveEpics(server)
            setScreen(server)
            awaitNewestReply()
            // The steered epic's line appears only once the refresh has read the listing, so the created epic's
            // absence is decided by then.
            composeRule.awaitExactlyOne(hasTestTag("band-line:$STEERED_EPIC"))
            composeRule.onAllNodesWithTag("band-line:$CREATED_EPIC").assertCountEquals(0)

            scrollToOldest()

            composeRule.onNodeWithTag(COMPACT_ASKING_TAG, useUnmergedTree = true).assertTextEquals("tide-bake asks")
        }
    }
}
