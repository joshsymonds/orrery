package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.Lifecycle
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicInteger

private const val PANE_ID = "%3"
private const val TRANSCRIPT = "synthetic-transcript"
private const val STREAMED_SEQ = 10L
private const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val NO_CONVERSATION_TAG = "no-conversation"
private const val STREAMED_REPLY = "synthetic streamed reply"
private const val POLLED_REPLY = "synthetic polled reply"
private const val TIMEOUT_MILLIS = 5_000L
// Long enough for the held stream's keepalives to keep it open, short enough that silence fails a stream quickly.
private const val SHORT_READ_TIMEOUT_MILLIS = 300
private const val QUIET_MILLIS = 5_000L
private const val STREAM_PREFIX_BYTES = 5
// Longer than STREAM_FAILURES_BEFORE_POLL, so a restart that ends this many streams UNAVAILABLE would poll if it counted.
private const val UNAVAILABLE_ENDS = 4
private const val VIRTUAL_STEP_MILLIS = 100L
private const val REAL_PAUSE_MILLIS = 10L
private const val NANOS_PER_MILLI = 1_000_000L

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PaneScreenStreamTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private fun paneProto(state: String): PaneProto = PaneProto.newBuilder()
        .setId(PANE_ID)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(2)
        .setCwd("/home/user/project")
        .setCommand("claude")
        .setState(state)
        .setSummary("Example summary.")
        .setLabel("example-label")
        .setChanged(4102444800L)
        .setSeen(4102444900L)
        .build()

    private fun paneList(state: String): PanesListResponse =
        PanesListResponse.newBuilder().addPanes(paneProto(state)).build()

    private fun replyItem(text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("reply").setTime(0).setText(text).build()

    /** The first delta of a stream: the reply, and the state that carries the pane. */
    private fun firstDelta(state: String, text: String): WatchConversationResponse =
        WatchConversationResponse.newBuilder()
            .setTranscript(TRANSCRIPT)
            .setSeq(STREAMED_SEQ)
            .addItems(replyItem(text))
            .setState(
                ConversationStateProto.newBuilder()
                    .setOrigin("synthetic origin")
                    .setPane(paneProto(state))
                    .build(),
            )
            .build()

    /** A keepalive: the transcript and seq the stream is at, with nothing else. */
    private fun keepalive(): WatchConversationResponse =
        WatchConversationResponse.newBuilder().setTranscript(TRANSCRIPT).setSeq(STREAMED_SEQ).build()

    /** The request a stream call sent: one Connect envelope holding a WatchConversationRequest. */
    private fun streamRequest(body: ByteArray): WatchConversationRequest {
        val length = ByteBuffer.wrap(body, 1, 4).int
        return WatchConversationRequest.parseFrom(body.copyOfRange(STREAM_PREFIX_BYTES, STREAM_PREFIX_BYTES + length))
    }

    private fun streamRequests(server: FakeOrreryServer): List<WatchConversationRequest> =
        server.requests.filter { it.rawPath == STREAM_PATH }.map { streamRequest(it.body) }

    private fun requestCount(server: FakeOrreryServer, path: String): Int =
        server.requests.count { it.method == "POST" && it.rawPath == path }

    private fun setScreen(server: FakeOrreryServer, client: OrreryClient = OrreryClient(server.baseUrl)) {
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    /**
     * Moves the virtual clock [VIRTUAL_STEP_MILLIS] at a time until [condition] holds. The screen's reconnect delays and
     * poll intervals run on that clock, so they pass by these steps rather than by the frames a waitUntil loop must
     * reach them in, and the steps needed do not depend on how fast frames run. The real pause between steps lets the
     * requests in flight finish; [TIMEOUT_MILLIS] bounds that real wait. Each step then idles the main looper, so the
     * deltas the screen hops to the main thread are applied before [condition] is checked.
     */
    private fun advanceUntil(condition: () -> Boolean) {
        val deadline = System.nanoTime() + TIMEOUT_MILLIS * NANOS_PER_MILLI
        while (!condition()) {
            check(System.nanoTime() < deadline) { "condition not met within $TIMEOUT_MILLIS ms" }
            composeRule.mainClock.advanceTimeBy(VIRTUAL_STEP_MILLIS)
            Thread.sleep(REAL_PAUSE_MILLIS)
            composeRule.waitForIdle()
        }
    }

    @Test
    fun healthyStreamShowsItsRepliesWithoutPaneOrConversationReads() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) {
                Reply.Stream(listOf(firstDelta("waiting", STREAMED_REPLY)), StreamEnd.HeldWith(keepalive()))
            }
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText(STREAMED_REPLY), TIMEOUT_MILLIS)
            composeRule.mainClock.advanceTimeBy(QUIET_MILLIS)
            advanceUntil { requestCount(server, HOST_PATH) >= 2 }

            assertEquals(0, requestCount(server, PANES_PATH))
            assertEquals(0, requestCount(server, CONVERSATION_PATH))
            assertEquals(1, streamRequests(server).size)
        }
    }

    @Test
    fun failedStreamReconnectsWithTheHeldTranscriptAndSeq() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) { request ->
                if (streamRequest(request.body).after == 0L) {
                    Reply.Stream(
                        listOf(firstDelta("waiting", STREAMED_REPLY)),
                        StreamEnd.Error(Code.UNAVAILABLE, "synthetic failure"),
                    )
                } else {
                    Reply.Stream(emptyList(), StreamEnd.HeldWith(keepalive()))
                }
            }
            setScreen(server)

            advanceUntil { streamRequests(server).size == 2 }
            val resumed = streamRequests(server)[1]
            assertEquals(TRANSCRIPT, resumed.transcript)
            assertEquals(STREAMED_SEQ, resumed.after)
            composeRule.waitUntilExactlyOneExists(hasText(STREAMED_REPLY), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun fourUnavailableEndsReconnectWithTheHeldTranscriptAndSeqWithoutPolling() {
        FakeOrreryServer().use { server ->
            val starts = AtomicInteger()
            server.route(STREAM_PATH) {
                val index = starts.getAndIncrement()
                if (index < UNAVAILABLE_ENDS) {
                    val delivered = if (index == 0) listOf(firstDelta("waiting", STREAMED_REPLY)) else emptyList()
                    Reply.Stream(delivered, StreamEnd.Error(Code.UNAVAILABLE, "synthetic outage"))
                } else {
                    Reply.Stream(emptyList(), StreamEnd.HeldWith(keepalive()))
                }
            }
            setScreen(server)

            advanceUntil {
                streamRequests(server).size == UNAVAILABLE_ENDS + 1 || requestCount(server, PANES_PATH) > 0
            }
            val requests = streamRequests(server)
            assertEquals(UNAVAILABLE_ENDS + 1, requests.size)
            assertEquals(0, requestCount(server, PANES_PATH))
            assertEquals(0, requestCount(server, CONVERSATION_PATH))
            assertEquals(0L, requests[0].after)
            requests.drop(1).forEach { resumed ->
                assertEquals(TRANSCRIPT, resumed.transcript)
                assertEquals(STREAMED_SEQ, resumed.after)
            }
            composeRule.waitUntilExactlyOneExists(hasText(STREAMED_REPLY), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun stopClosesTheStreamAndTheNextStartOpensANewOne() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) {
                Reply.Stream(listOf(firstDelta("waiting", STREAMED_REPLY)), StreamEnd.HeldWith(keepalive()))
            }
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText(STREAMED_REPLY), TIMEOUT_MILLIS)

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            assertTrue(server.awaitStreamClosedByClient(TIMEOUT_MILLIS))

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            composeRule.waitUntil(TIMEOUT_MILLIS) { streamRequests(server).size == 2 }
            assertEquals(STREAMED_SEQ, streamRequests(server)[1].after)
        }
    }

    @Test
    fun threeSilentStreamsFallBackToPollingUntilTheNextStart() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) { Reply.Stream(emptyList(), StreamEnd.Silent) }
            server.respond(PANES_PATH, paneList("waiting"))
            server.respond(
                CONVERSATION_PATH,
                ConversationResponse.newBuilder()
                    .setOrigin("synthetic origin")
                    .addItems(replyItem(POLLED_REPLY))
                    .build(),
            )
            val client = OrreryClient(server.baseUrl, streamReadTimeoutMillis = SHORT_READ_TIMEOUT_MILLIS)
            setScreen(server, client)

            advanceUntil { requestCount(server, CONVERSATION_PATH) >= 1 }
            composeRule.waitUntilExactlyOneExists(hasText(POLLED_REPLY), TIMEOUT_MILLIS)
            assertEquals(3, streamRequests(server).size)

            composeRule.mainClock.advanceTimeBy(QUIET_MILLIS)
            advanceUntil { requestCount(server, PANES_PATH) >= 2 }
            assertEquals(3, streamRequests(server).size)

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            advanceUntil { streamRequests(server).size == 4 }
        }
    }

    @Test
    fun threeFailedStreamsWithOtherErrorsFallBackToPollingUntilTheNextStart() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) { Reply.Stream(emptyList(), StreamEnd.Error(Code.INTERNAL_ERROR, "synthetic failure")) }
            server.respond(PANES_PATH, paneList("waiting"))
            server.respond(
                CONVERSATION_PATH,
                ConversationResponse.newBuilder()
                    .setOrigin("synthetic origin")
                    .addItems(replyItem(POLLED_REPLY))
                    .build(),
            )
            setScreen(server)

            advanceUntil { requestCount(server, CONVERSATION_PATH) >= 1 }
            composeRule.waitUntilExactlyOneExists(hasText(POLLED_REPLY), TIMEOUT_MILLIS)
            assertEquals(3, streamRequests(server).size)

            composeRule.mainClock.advanceTimeBy(QUIET_MILLIS)
            advanceUntil { requestCount(server, PANES_PATH) >= 2 }
            assertEquals(3, streamRequests(server).size)

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            advanceUntil { streamRequests(server).size == 4 }
        }
    }

    @Test
    fun notFoundStreamPollsAtOnceAndShowsNoConversationForAnEmptyPane() {
        FakeOrreryServer().use { server ->
            server.route(STREAM_PATH) { Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane is gone")) }
            server.respond(PANES_PATH, paneList(state = ""))
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(NO_CONVERSATION_TAG), TIMEOUT_MILLIS)
            assertEquals(1, streamRequests(server).size)

            composeRule.mainClock.advanceTimeBy(QUIET_MILLIS)
            advanceUntil { requestCount(server, PANES_PATH) >= 2 }
            assertEquals(1, streamRequests(server).size)
        }
    }
}
