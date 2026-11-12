package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.net.SocketTimeoutException
import java.nio.ByteBuffer

class OrreryClientStreamTest {
    private val examplePane = PaneProto.newBuilder()
        .setId("%3")
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(2)
        .setCwd("/home/user/project")
        .setCommand("claude")
        .setState("waiting")
        .setSummary("Example summary.")
        .setLabel("example-label")
        .setChanged(4102444800L)
        .setSeen(4102444900L)
        .setQuestion("Example question?")
        .setQuestionHash("abc123")
        .setNewestReply(4102444950L)
        .build()

    private val expectedPane = Pane(
        id = "%3",
        host = "example-host",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = "/home/user/project",
        command = "claude",
        state = "waiting",
        summary = "Example summary.",
        label = "example-label",
        changed = 4102444800L,
        seen = 4102444900L,
        question = "Example question?",
        questionHash = "abc123",
        newestReply = 4102444950L,
    )

    private val firstDeltaProto = WatchConversationResponse.newBuilder()
        .setTranscript("example-transcript")
        .setSeq(10L)
        .setReset(true)
        .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000000L).setText("Example prompt."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("compaction")
                .setTime(1700000050L)
                .setText("Example summary.")
                .setTrigger("auto")
                .setTokens(611000L),
        )
        .setState(
            ConversationStateProto.newBuilder()
                .setOrigin("Example opening prompt.")
                .setGoal("Example goal.")
                .addWaiting(WaitingMessage.newBuilder().setText("Example queued.").setTime(1700000300L).setCanceled(false))
                .addWaiting(WaitingMessage.newBuilder().setText("Example cancelled.").setTime(1700000310L).setCanceled(true))
                .setContext(ConversationContextProto.newBuilder().setTokens(1200L).setWindow(200000L))
                .setPane(examplePane),
        )
        .build()

    private val expectedFirstDelta = ConversationDelta(
        transcript = "example-transcript",
        seq = 10L,
        reset = true,
        items = listOf(
            ConversationItem(kind = "prompt", time = 1700000000L, text = "Example prompt.", tool = "", input = "", id = "", error = false),
            ConversationItem(
                kind = "compaction",
                time = 1700000050L,
                text = "Example summary.",
                tool = "",
                input = "",
                id = "",
                error = false,
                trigger = "auto",
                tokens = 611000L,
            ),
        ),
        state = ConversationState(
            origin = "Example opening prompt.",
            goal = "Example goal.",
            waiting = listOf(
                WaitingItem(text = "Example queued.", time = 1700000300L, canceled = false),
                WaitingItem(text = "Example cancelled.", time = 1700000310L, canceled = true),
            ),
            context = ConversationContext(tokens = 1200L, window = 200000L),
            pane = expectedPane,
        ),
    )

    private val secondDeltaProto = WatchConversationResponse.newBuilder()
        .setTranscript("example-transcript")
        .setSeq(11L)
        .addItems(ConversationItemProto.newBuilder().setKind("reply").setTime(1700000100L).setText("Example reply."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("result")
                .setTime(1700000210L)
                .setText("Example failure.")
                .setId("example-tool-use")
                .setError(true),
        )
        .build()

    private val expectedSecondDelta = ConversationDelta(
        transcript = "example-transcript",
        seq = 11L,
        reset = false,
        items = listOf(
            ConversationItem(kind = "reply", time = 1700000100L, text = "Example reply.", tool = "", input = "", id = "", error = false),
            ConversationItem(
                kind = "result",
                time = 1700000210L,
                text = "Example failure.",
                tool = "",
                input = "",
                id = "example-tool-use",
                error = true,
            ),
        ),
        state = null,
    )

    private val thirdDeltaProto = WatchConversationResponse.newBuilder()
        .setTranscript("example-transcript")
        .setSeq(12L)
        .setState(ConversationStateProto.newBuilder().setOrigin("Example opening prompt."))
        .build()

    private val expectedThirdDelta = ConversationDelta(
        transcript = "example-transcript",
        seq = 12L,
        reset = false,
        items = emptyList(),
        state = ConversationState(
            origin = "Example opening prompt.",
            goal = "",
            waiting = emptyList(),
            context = null,
            pane = null,
        ),
    )

    @Test
    fun watchConversationSendsTheProcedureHeadersAndTheRequestFields() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Clean) }

            runBlocking {
                OrreryClient(server.baseUrl).watchConversation("%3", "example-transcript", 42L) { }
            }

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals(WATCH_CONVERSATION, request.rawPath)
            assertEquals("application/connect+proto", request.headers["content-type"])
            assertEquals("orrery-android", request.headers["x-orrery-client"])

            val sent = WatchConversationRequest.parseFrom(decodeEnvelope(request.body))
            assertEquals("%3", sent.pane)
            assertEquals(50, sent.limit)
            assertEquals("example-transcript", sent.transcript)
            assertEquals(42L, sent.after)
        }
    }

    @Test
    fun watchConversationDeliversEveryDeltaInOrderWithEveryFieldMapped() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) {
                Reply.Stream(listOf(firstDeltaProto, secondDeltaProto, thirdDeltaProto), StreamEnd.Clean)
            }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchConversation("%3", "example-transcript", 0L) { received.add(it) }
            }

            assertEquals(listOf(expectedFirstDelta, expectedSecondDelta, expectedThirdDelta), received)
        }
    }

    @Test
    fun watchConversationCleanEndWithNoMessagesReturnsWithoutDeltas() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Clean) }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchConversation("%3", "example-transcript", 0L) { received.add(it) }
            }

            assertEquals(emptyList<ConversationDelta>(), received)
        }
    }

    @Test
    fun watchConversationErrorEndThrowsCallExceptionAfterTheDeltasBeforeIt() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) {
                Reply.Stream(listOf(firstDeltaProto), StreamEnd.Error(Code.NOT_FOUND, "example pane is gone"))
            }
            val received = mutableListOf<ConversationDelta>()

            val error = assertThrows(OrreryCallException::class.java) {
                runBlocking {
                    OrreryClient(server.baseUrl).watchConversation("%3", "example-transcript", 0L) { received.add(it) }
                }
            }

            assertEquals(Code.NOT_FOUND, error.code)
            assertEquals(listOf(expectedFirstDelta), received)
        }
    }

    @Test
    fun watchConversationSilenceLongerThanTheStreamReadTimeoutThrowsIOException() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) { Reply.Stream(listOf(firstDeltaProto), StreamEnd.Silent) }
            val client = OrreryClient(server.baseUrl, streamReadTimeoutMillis = 300)
            val received = mutableListOf<ConversationDelta>()

            assertThrows(SocketTimeoutException::class.java) {
                runBlocking {
                    client.watchConversation("%3", "example-transcript", 0L) { received.add(it) }
                }
            }

            assertEquals(listOf(expectedFirstDelta), received)
        }
    }

    @Test
    fun cancellingTheCallerClosesTheStreamWithinTwoSeconds() {
        FakeOrreryServer().use { server ->
            server.route(WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Held) }
            val client = OrreryClient(server.baseUrl)

            runBlocking {
                val firstDelta = CompletableDeferred<Unit>()
                val job = launch(Dispatchers.IO) {
                    client.watchConversation("%3", "example-transcript", 0L) { firstDelta.complete(Unit) }
                }
                withTimeout(5_000) { firstDelta.await() }

                job.cancelAndJoin()
            }

            assertTrue(server.awaitStreamClosedByClient(2_000))
        }
    }

    @Test
    fun watchConversationRefusedConnectionThrowsIOExceptionThatIsNotCallException() {
        val server = FakeOrreryServer()
        val baseUrl = server.baseUrl
        server.close()

        val error = assertThrows(IOException::class.java) {
            runBlocking {
                OrreryClient(baseUrl).watchConversation("%3", "example-transcript", 0L) { }
            }
        }

        assertFalse(error is OrreryCallException)
    }

    /** The payload of one Connect envelope: a zero flag byte, the big-endian length, then the message. */
    private fun decodeEnvelope(body: ByteArray): ByteArray {
        assertEquals(0, body[0].toInt())
        val length = ByteBuffer.wrap(body, 1, 4).int
        assertEquals(5 + length, body.size)
        return body.copyOfRange(5, body.size)
    }

    private companion object {
        const val WATCH_CONVERSATION = "/orrery.v1.Panes/WatchConversation"
    }
}
