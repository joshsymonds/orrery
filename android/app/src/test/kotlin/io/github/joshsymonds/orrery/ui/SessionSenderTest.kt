package io.github.joshsymonds.orrery.ui

import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

private const val SESSION_ID = "session-1"
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val TEXT = "echo hi"
private const val OTHER_TEXT = "echo bye"

class SessionSenderTest {
    @Test
    fun retryAfterResourceExhaustedRefusalReusesPromptId() {
        FakeOrreryServer().use { server ->
            server.fail(SAY_PATH, Code.RESOURCE_EXHAUSTED, "cap reached")
            val sender = sender(server)

            val error = assertThrows(OrreryCallException::class.java) { sender.send(TEXT) }
            assertEquals(Code.RESOURCE_EXHAUSTED, error.code)

            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            sender.send(TEXT)

            assertEquals(
                listOf(say("prompt-1", TEXT), say("prompt-1", TEXT)),
                said(server),
            )
        }
    }

    @Test
    fun retryAfterNoAnswerReusesPromptId() {
        FakeOrreryServer().use { server ->
            server.drop(SAY_PATH)
            val sender = sender(server)

            assertThrows(NoAnswerException::class.java) { sender.send(TEXT) }

            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            sender.send(TEXT)

            assertEquals(
                listOf(say("prompt-1", TEXT), say("prompt-1", TEXT)),
                said(server),
            )
        }
    }

    @Test
    fun sendAfterSuccessTakesFreshPromptId() {
        FakeOrreryServer().use { server ->
            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            val sender = sender(server)

            sender.send(TEXT)
            sender.send(TEXT)

            assertEquals(
                listOf(say("prompt-1", TEXT), say("prompt-2", TEXT)),
                said(server),
            )
        }
    }

    @Test
    fun changedTextAfterFailureTakesFreshPromptId() {
        FakeOrreryServer().use { server ->
            server.fail(SAY_PATH, Code.RESOURCE_EXHAUSTED, "cap reached")
            val sender = sender(server)

            assertThrows(OrreryCallException::class.java) { sender.send(TEXT) }

            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            sender.send(OTHER_TEXT)

            assertEquals(
                listOf(say("prompt-1", TEXT), say("prompt-2", OTHER_TEXT)),
                said(server),
            )
        }
    }

    private fun sender(server: FakeOrreryServer): SessionSender {
        var next = 0
        return SessionSender(OrreryClient(server.baseUrl), SESSION_ID) { "prompt-${++next}" }
    }

    private fun say(promptId: String, text: String): SayRequest =
        SayRequest.newBuilder().setId(SESSION_ID).setText(text).setPromptId(promptId).build()

    private fun said(server: FakeOrreryServer): List<SayRequest> =
        server.requests.map { SayRequest.parseFrom(it.body) }
}
