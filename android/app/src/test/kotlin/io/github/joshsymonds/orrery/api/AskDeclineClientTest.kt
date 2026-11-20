package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.DeclineRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DeclineResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class AskDeclineClientTest {
    @Test
    fun declineSendsAPanesQuestionFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_DECLINE, DeclineResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).decline(
                kind = "pane",
                id = "example-pane",
                requestId = "",
                questionHash = "example-hash",
            )

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(CONTROL_DECLINE, request)
            val sent = DeclineRequest.parseFrom(request.body)
            assertEquals("pane", sent.kind)
            assertEquals("example-pane", sent.id)
            assertEquals("", sent.requestId)
            assertEquals("example-hash", sent.questionHash)
        }
    }

    @Test
    fun declineSendsAHeadlessSessionsRequestFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_DECLINE, DeclineResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).decline(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
            )

            assertEquals(StepCallResult.Landed, result)
            val sent = DeclineRequest.parseFrom(server.requests.single().body)
            assertEquals("headless", sent.kind)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("example-request", sent.requestId)
            assertEquals("", sent.questionHash)
        }
    }

    @Test
    fun aRefusedDeclineAnswersRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(CONTROL_DECLINE, Code.FAILED_PRECONDITION, "question: example-request is not open")

            val result = OrreryClient(server.baseUrl).decline(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
            )

            assertEquals(StepCallResult.Refused("question: example-request is not open"), result)
        }
    }

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val CONTROL_DECLINE = "/orrery.v1.Control/Decline"
    }
}
