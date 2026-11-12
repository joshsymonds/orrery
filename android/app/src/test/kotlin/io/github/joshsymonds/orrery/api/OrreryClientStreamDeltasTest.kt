package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import com.connectrpc.ConnectException
import com.connectrpc.ServerOnlyStreamInterface
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ReceiveChannel
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.IOException
import java.net.ConnectException as TransportConnectException

class OrreryClientStreamDeltasTest {
    @Test
    fun sendOnAStreamClosedBeforeItWritesThrowsTheFailureTheStreamClosedWith() {
        val transport = TransportConnectException("Connection refused")
        val stream = FakeStream(
            sendFailure = IllegalStateException("cannot send. underlying stream is closed"),
            closeCause = ConnectException(Code.UNAVAILABLE, "Connection refused", transport, emptyMap()),
        )

        val error = assertThrows(IOException::class.java) { streamDeltas(stream) }

        assertSame(transport, error)
    }

    @Test
    fun sendThatFailsClosedThrowsAnIOExceptionWhenTheStreamClosesWithNoCause() {
        val stream = FakeStream(sendFailure = IllegalStateException("closed"), closeCause = null)

        val error = assertThrows(IOException::class.java) { streamDeltas(stream) }

        assertFalse(error is OrreryCallException)
    }

    @Test
    fun otherSendFailuresMapAsACallsFailureDoes() {
        val transport = TransportConnectException("Connection refused")
        val stream = FakeStream(
            sendFailure = ConnectException(Code.UNAVAILABLE, "Connection refused", transport, emptyMap()),
            closeCause = null,
        )

        val error = assertThrows(IOException::class.java) { streamDeltas(stream) }

        assertSame(transport, error)
    }

    private fun streamDeltas(stream: FakeStream) {
        runBlocking {
            OrreryClient("http://localhost").streamDeltas(
                stream,
                WatchConversationRequest.getDefaultInstance(),
                { error("no answer was expected") },
                { },
            )
        }
    }

    /** A stream whose send fails with [sendFailure] and whose answer channel closes with [closeCause]. */
    private class FakeStream(
        private val sendFailure: Throwable,
        closeCause: Throwable?,
    ) : ServerOnlyStreamInterface<WatchConversationRequest, WatchConversationResponse> {
        private val responses = Channel<WatchConversationResponse>(Channel.UNLIMITED).apply { close(closeCause) }

        override fun responseChannel(): ReceiveChannel<WatchConversationResponse> = responses

        override fun responseHeaders(): Deferred<Map<String, List<String>>> =
            CompletableDeferred(emptyMap())

        override fun responseTrailers(): Deferred<Map<String, List<String>>> =
            CompletableDeferred(emptyMap())

        override suspend fun sendAndClose(input: WatchConversationRequest): Result<Unit> =
            Result.failure(sendFailure)

        override suspend fun receiveClose() {}

        override fun isReceiveClosed(): Boolean = true

        override fun isClosed(): Boolean = true
    }
}
