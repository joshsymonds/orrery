package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineText
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import java.nio.ByteBuffer
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
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val ANSWER_PATH = "/orrery.v1.Control/Answer"
private const val SEEN_PATH = "/orrery.v1.Control/Seen"
private const val TIMEOUT_MILLIS = 5_000L
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val REPLY_TIME = 1_700_000_000L
private const val SYNTHETIC_ORIGIN = "synthetic origin"
private const val SYNTHETIC_DRAFT = "synthetic draft"
private const val PART_ONE = "synthetic part one"
private const val PART_TWO = "synthetic part one, then more"
private const val REQUEST_ID = "synthetic-request"
private const val QUESTION = "Pick a synthetic option?"
private const val OPTION_ONE = "Synthetic one"
private const val OPTION_TWO = "Synthetic two"
private const val REFRESH_FAILED = "could not refresh"
private const val NOT_SENT = "not sent"
private const val INTERRUPT_TAG = "session-interrupt"
private const val STOP_TAG = "session-stop"
private const val REQUEST_TAG = "session-request"

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SessionScreenTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client) }
        }
    }

    /**
     * Scripts the watch stream: the first request gets streams[0], the second streams[1], and so on. A
     * request past the script gets a stream that sends nothing and ends cleanly after a few seconds.
     */
    private fun serveStreams(server: FakeOrreryServer, vararg streams: Reply) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            val index = requests.getAndIncrement()
            if (index < streams.size) streams[index] else Reply.Stream(emptyList(), StreamEnd.Silent)
        }
    }

    private fun stateOf(state: String, request: OpenRequest? = null): ConversationState {
        val builder = ConversationState.newBuilder()
            .setOrigin(SYNTHETIC_ORIGIN)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/project")
                    .setLabel("example-label")
                    .setState(state)
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
            )
        request?.let { builder.setRequest(it) }
        return builder.build()
    }

    private fun delta(
        seq: Long,
        items: List<ConversationItemProto> = emptyList(),
        state: ConversationState? = null,
        unfinished: List<EngineText> = emptyList(),
    ): SessionsWatchConversationResponse {
        val builder = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
            .addAllUnfinished(unfinished)
        state?.let { builder.setState(it) }
        return builder.build()
    }

    private fun item(kind: String, text: String, time: Long = 0L): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(time).setText(text).build()

    private fun unfinishedText(text: String, kind: String = "text"): EngineText =
        EngineText.newBuilder()
            .setMessageId("synthetic-message")
            .setIndex(0)
            .setParentToolUseId("")
            .setKind(kind)
            .setText(text)
            .build()

    private fun streamOf(vararg messages: MessageLite, end: StreamEnd = StreamEnd.Silent): Reply.Stream =
        Reply.Stream(messages.toList(), end)

    private fun countPosts(server: FakeOrreryServer, path: String): Int =
        server.requests.count { it.method == "POST" && it.rawPath == path }

    private fun watchRequests(server: FakeOrreryServer): List<SessionsWatchConversationRequest> =
        server.requests
            .filter { it.method == "POST" && it.rawPath == WATCH_PATH }
            .map { request -> watchRequest(request) }

    /** The stream request's message: its enveloped body, one flag byte, a big-endian length, then the payload. */
    private fun watchRequest(request: RecordedRequest): SessionsWatchConversationRequest {
        val body = request.body
        assertEquals(0, body[0].toInt())
        val length = ByteBuffer.wrap(body, 1, 4).int
        return SessionsWatchConversationRequest.parseFrom(body.copyOfRange(5, 5 + length))
    }

    private fun setDraft(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.waitForIdle()
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(composeRule.activity.window.decorView)) {
            "SessionScreen must host the draft's EditText"
        }

    private fun findEditText(view: View): EditText? {
        if (view is EditText) {
            return view
        }
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                findEditText(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }

    @Test
    fun streamedItemsShowInTheConversation() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(item("prompt", "synthetic prompt"), item("reply", "synthetic reply")),
                        state = stateOf("done"),
                    ),
                ),
            )
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic prompt").assertIsDisplayed()
        }
    }

    @Test
    fun unfinishedMainTextShowsAsAReplyThenTheFinishedBlockShowsOnce() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        state = stateOf("working"),
                        unfinished = listOf(
                            unfinishedText(PART_ONE),
                            unfinishedText("synthetic thinking", kind = "thinking"),
                        ),
                    ),
                    end = StreamEnd.Error(Code.UNAVAILABLE, "synthetic outage"),
                ),
                streamOf(
                    delta(seq = 2L, unfinished = listOf(unfinishedText(PART_TWO))),
                    delta(seq = 3L, items = listOf(item("reply", PART_TWO))),
                ),
            )
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText(PART_ONE), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithText("synthetic thinking").assertCountEquals(0)

            composeRule.waitUntil(TIMEOUT_MILLIS) {
                composeRule.onAllNodesWithText(PART_TWO).fetchSemanticsNodes().size == 1 &&
                    composeRule.onAllNodesWithText(PART_ONE).fetchSemanticsNodes().isEmpty()
            }
            composeRule.onAllNodesWithText(PART_TWO).assertCountEquals(1)
        }
    }

    @Test
    fun sendPostsTheDraftAsAPromptToTheSession() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag("composer"), TIMEOUT_MILLIS)

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { countPosts(server, SAY_PATH) == 1 }

            val request = server.requests.single { it.rawPath == SAY_PATH }
            val say = SayRequest.parseFrom(request.body)
            assertEquals(SESSION_ID, say.id)
            assertEquals(SYNTHETIC_DRAFT, say.text)
            assertTrue(say.promptId.isNotEmpty())
        }
    }

    @Test
    fun resourceExhaustedRefusalKeepsTheDraft() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            server.fail(SAY_PATH, Code.RESOURCE_EXHAUSTED, "synthetic cap reached")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag("composer"), TIMEOUT_MILLIS)

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.waitUntilExactlyOneExists(hasText(NOT_SENT), TIMEOUT_MILLIS)

            assertEquals(SYNTHETIC_DRAFT, editField().text.toString())
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun waitingRequestCardAnswersThroughAnswer() {
        FakeOrreryServer().use { server ->
            val request = OpenRequest.newBuilder()
                .setId(REQUEST_ID)
                .setKind("question")
                .setQuestion(QUESTION)
                .addAllOptions(listOf(OPTION_ONE, OPTION_TWO))
                .build()
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("waiting", request))))
            server.respond(ANSWER_PATH, AnswerResponse.getDefaultInstance())
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(REQUEST_TAG), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag("session-option:$OPTION_TWO").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { countPosts(server, ANSWER_PATH) == 1 }

            val sent = server.requests.single { it.rawPath == ANSWER_PATH }
            assertEquals(
                AnswerRequest.newBuilder()
                    .setId(SESSION_ID)
                    .setRequestId(REQUEST_ID)
                    .setAnswer(OPTION_TWO)
                    .build(),
                AnswerRequest.parseFrom(sent.body),
            )
        }
    }

    @Test
    fun workingSessionShowsInterruptAndStop() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(INTERRUPT_TAG), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(INTERRUPT_TAG).assertIsDisplayed()
            composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
        }
    }

    @Test
    fun readingMarksTheSessionSeen() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(item("reply", "synthetic read reply", time = REPLY_TIME)),
                        state = stateOf("done"),
                    ),
                ),
            )
            server.respond(SEEN_PATH, SeenResponse.getDefaultInstance())
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { countPosts(server, SEEN_PATH) >= 1 }

            val seen = SeenRequest.parseFrom(server.requests.first { it.rawPath == SEEN_PATH }.body)
            assertEquals(SESSION_ID, seen.id)
        }
    }

    @Test
    fun failedStreamReconnectsWithTheHeldSeqAsAfter() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 7L,
                        items = listOf(item("prompt", "synthetic prompt")),
                        state = stateOf("working"),
                    ),
                    end = StreamEnd.Error(Code.INTERNAL_ERROR, "synthetic failure"),
                ),
            )
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText(REFRESH_FAILED), TIMEOUT_MILLIS)
            composeRule.waitUntil(TIMEOUT_MILLIS) { watchRequests(server).size >= 2 }

            val reconnect = watchRequests(server)[1]
            assertEquals(SESSION_ID, reconnect.id)
            assertEquals(7L, reconnect.after)
        }
    }
}
