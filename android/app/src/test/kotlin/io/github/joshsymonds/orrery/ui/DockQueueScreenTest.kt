package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.QueuedMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val PROMPT_ID = "synthetic-prompt-1"
private const val QUEUED_TEXT = "synthetic queued text"
private const val REPLY_TEXT = "synthetic reply"
private const val ROW_TAG = "dock-tray-row"
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L

/** A headless session's screen over a scripted watch stream: the queued owner message and the transcript it may appear in. */
@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockQueueScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client) }
        }
    }

    private fun serveStream(server: FakeOrreryServer, vararg messages: MessageLite) {
        server.route(WATCH_PATH) { _ -> Reply.Stream(messages.toList(), StreamEnd.Open) }
    }

    private fun state(): ConversationState =
        ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/project")
                    .setLabel("example-label")
                    .setState("working")
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
            )
            .build()

    private fun delta(
        seq: Long,
        items: List<ConversationItemProto> = emptyList(),
        queued: List<QueuedMessage> = emptyList(),
        withState: Boolean = false,
    ): SessionsWatchConversationResponse {
        val builder = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
            .addAllQueued(queued)
        if (withState) builder.setState(state())
        return builder.build()
    }

    private fun promptItem(text: String): ConversationItemProto =
        ConversationItemProto.newBuilder()
            .setKind("prompt")
            .setText(text)
            .setPromptId(PROMPT_ID)
            .build()

    private fun replyItem(text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("reply").setText(text).build()

    private fun queued(state: String): QueuedMessage =
        QueuedMessage.newBuilder()
            .setPromptId(PROMPT_ID)
            .setText(QUEUED_TEXT)
            .setState(state)
            .setWaits("turn")
            .build()

    @Test
    fun queuedOwnerMessageIsDrawnInTheTrayAndNotInTheTranscript() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                delta(seq = 1L, items = listOf(promptItem(QUEUED_TEXT)), queued = listOf(queued("queued")), withState = true),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(ROW_TAG))
            composeRule.onAllNodesWithText(QUEUED_TEXT).assertCountEquals(1)
        }
    }

    @Test
    fun canceledOwnerMessageStaysOutOfTheTranscript() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                delta(seq = 1L, items = listOf(promptItem(QUEUED_TEXT)), queued = listOf(queued("queued")), withState = true),
                delta(seq = 2L, items = listOf(replyItem(REPLY_TEXT)), queued = listOf(queued("canceled"))),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))
            composeRule.onAllNodesWithText(QUEUED_TEXT).assertCountEquals(0)
        }
    }

    @Test
    fun discardedOwnerMessageStaysOutOfTheTranscriptAndStaysInTheTray() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                delta(seq = 1L, items = listOf(promptItem(QUEUED_TEXT)), queued = listOf(queued("queued")), withState = true),
                delta(seq = 2L, items = listOf(replyItem(REPLY_TEXT)), queued = listOf(queued("discarded"))),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))
            composeRule.awaitExactlyOne(hasTestTag(ROW_TAG))
            composeRule.onAllNodesWithText(QUEUED_TEXT).assertCountEquals(1)
        }
    }

    @Test
    fun takenOwnerMessageIsDrawnInTheTranscriptOnce() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                delta(seq = 1L, items = listOf(promptItem(QUEUED_TEXT)), queued = listOf(queued("queued")), withState = true),
                delta(seq = 2L, items = listOf(replyItem(REPLY_TEXT))),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))
            composeRule.onAllNodesWithText(QUEUED_TEXT).assertCountEquals(1)
            composeRule.onAllNodesWithTag(ROW_TAG).assertCountEquals(0)
        }
    }
}
