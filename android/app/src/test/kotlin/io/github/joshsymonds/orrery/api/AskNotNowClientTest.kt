package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.BringBackRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.BringBackResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NotNowRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NotNowResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class AskNotNowClientTest {
    @Test
    fun notNowSendsAPanesQuestionFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_NOT_NOW, NotNowResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).notNow(
                kind = "pane",
                id = "example-pane",
                requestId = "",
                questionHash = "example-hash",
                changed = 1700000000L,
            )

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(CONTROL_NOT_NOW, request)
            val sent = NotNowRequest.parseFrom(request.body)
            assertEquals("pane", sent.kind)
            assertEquals("example-pane", sent.id)
            assertEquals("", sent.requestId)
            assertEquals("example-hash", sent.questionHash)
            assertEquals(1700000000L, sent.changed)
        }
    }

    @Test
    fun notNowSendsAHeadlessSessionsRequestFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_NOT_NOW, NotNowResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).notNow(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
                changed = 0L,
            )

            assertEquals(StepCallResult.Landed, result)
            val sent = NotNowRequest.parseFrom(server.requests.single().body)
            assertEquals("headless", sent.kind)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("example-request", sent.requestId)
            assertEquals("", sent.questionHash)
            assertEquals(0L, sent.changed)
        }
    }

    @Test
    fun aRefusedNotNowAnswersRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(CONTROL_NOT_NOW, Code.FAILED_PRECONDITION, "question: example-request is not open")

            val result = OrreryClient(server.baseUrl).notNow(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
                changed = 0L,
            )

            assertEquals(StepCallResult.Refused("question: example-request is not open"), result)
        }
    }

    @Test
    fun bringBackSendsAPanesQuestionFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_BRING_BACK, BringBackResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).bringBack(
                kind = "pane",
                id = "example-pane",
                requestId = "",
                questionHash = "example-hash",
                changed = 1700000000L,
            )

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(CONTROL_BRING_BACK, request)
            val sent = BringBackRequest.parseFrom(request.body)
            assertEquals("pane", sent.kind)
            assertEquals("example-pane", sent.id)
            assertEquals("", sent.requestId)
            assertEquals("example-hash", sent.questionHash)
            assertEquals(1700000000L, sent.changed)
        }
    }

    @Test
    fun bringBackSendsAHeadlessSessionsRequestFieldsAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_BRING_BACK, BringBackResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).bringBack(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
                changed = 0L,
            )

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(CONTROL_BRING_BACK, request)
            val sent = BringBackRequest.parseFrom(request.body)
            assertEquals("headless", sent.kind)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("example-request", sent.requestId)
            assertEquals("", sent.questionHash)
            assertEquals(0L, sent.changed)
        }
    }

    @Test
    fun aRefusedBringBackAnswersRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(CONTROL_BRING_BACK, Code.FAILED_PRECONDITION, "question: example-request is not put off")

            val result = OrreryClient(server.baseUrl).bringBack(
                kind = "headless",
                id = SESSION_ID,
                requestId = "example-request",
                questionHash = "",
                changed = 0L,
            )

            assertEquals(StepCallResult.Refused("question: example-request is not put off"), result)
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
        const val CONTROL_NOT_NOW = "/orrery.v1.Control/NotNow"
        const val CONTROL_BRING_BACK = "/orrery.v1.Control/BringBack"
    }
}
