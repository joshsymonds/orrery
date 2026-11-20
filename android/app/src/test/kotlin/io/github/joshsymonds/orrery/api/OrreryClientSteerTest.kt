package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class OrreryClientSteerTest {
    @Test
    fun moveEpicSendsOneMoveWithTheEpicIdAndSessionAndAnswersSteered() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_MOVE, EpicsMoveResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).moveEpic("example-repo/example-epic", "example-session")

            assertEquals(SteerResult.Steered("example-session"), result)
            val moves = server.requests.filter { it.rawPath == EPICS_MOVE }
            assertEquals(1, moves.size)
            assertConnectRequest(EPICS_MOVE, moves.single())
            val request = EpicsMoveRequest.parseFrom(moves.single().body)
            assertEquals("example-repo/example-epic", request.id)
            assertEquals("example-session", request.session)
        }
    }

    @Test
    fun continueEpicSendsOneContinueAndAnswersTheAnswersSessionEvenWhenEmpty() {
        FakeOrreryServer().use { server ->
            val client = OrreryClient(server.baseUrl)

            server.respond(EPICS_CONTINUE, EpicsContinueResponse.newBuilder().setSession("example-started").build())
            assertEquals(SteerResult.Steered("example-started"), client.continueEpic("example-repo/example-epic"))

            server.respond(EPICS_CONTINUE, EpicsContinueResponse.getDefaultInstance())
            assertEquals(SteerResult.Steered(""), client.continueEpic("example-repo/example-epic"))

            val continues = server.requests.filter { it.rawPath == EPICS_CONTINUE }
            assertEquals(2, continues.size)
            assertConnectRequest(EPICS_CONTINUE, continues.first())
            assertEquals("example-repo/example-epic", EpicsContinueRequest.parseFrom(continues.first().body).id)
        }
    }

    @Test
    fun aRefusedMoveOrContinueAnswersRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_MOVE, Code.FAILED_PRECONDITION, "example move refusal")
            server.fail(EPICS_CONTINUE, Code.FAILED_PRECONDITION, "example continue refusal")
            val client = OrreryClient(server.baseUrl)

            assertEquals(
                SteerResult.Refused("example move refusal"),
                client.moveEpic("example-repo/example-epic", "example-session"),
            )
            assertEquals(
                SteerResult.Refused("example continue refusal"),
                client.continueEpic("example-repo/example-epic"),
            )
        }
    }

    @Test
    fun aMoveOrContinueThatGetsNoAnswerFromTheServerIsThrownAsItIs() {
        FakeOrreryServer().use { server ->
            val client = OrreryClient(server.baseUrl)

            for (code in listOf(Code.UNAVAILABLE, Code.DEADLINE_EXCEEDED, Code.UNKNOWN)) {
                server.fail(EPICS_MOVE, code, "example move ${code.codeName}")
                server.fail(EPICS_CONTINUE, code, "example continue ${code.codeName}")

                val move = assertThrows(OrreryCallException::class.java) {
                    client.moveEpic("example-repo/example-epic", "example-session")
                }
                assertEquals(code, move.code)
                val continued = assertThrows(OrreryCallException::class.java) {
                    client.continueEpic("example-repo/example-epic")
                }
                assertEquals(code, continued.code)
            }
        }
    }

    @Test
    fun aDroppedMoveIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(EPICS_MOVE) { it.moveEpic("example-repo/example-epic", "example-session") }
    }

    @Test
    fun aDroppedContinueIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(EPICS_CONTINUE) { it.continueEpic("example-repo/example-epic") }
    }

    @Test
    fun archivedSessionsAnswersTheArchivedIds() {
        FakeOrreryServer().use { server ->
            server.respond(
                SESSIONS_LIST,
                ListResponse.newBuilder()
                    .addSessions(SessionProto.newBuilder().setId("example-archived").setArchived(true))
                    .addSessions(SessionProto.newBuilder().setId("example-live"))
                    .build(),
            )

            assertEquals(setOf("example-archived"), OrreryClient(server.baseUrl).archivedSessions())
        }
    }

    @Test
    fun archivedSessionsIsEmptyWhenTheServerDoesNotListSessions() {
        FakeOrreryServer().use { server ->
            server.fail(SESSIONS_LIST, Code.UNIMPLEMENTED, "unimplemented")

            assertEquals(emptySet<String>(), OrreryClient(server.baseUrl).archivedSessions())
        }
    }

    @Test
    fun aHeadlessSessionsArchivedFlagReachesItsPane() {
        FakeOrreryServer().use { server ->
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder()
                    .addSessions(HeadlessSessionProto.newBuilder().setId("example-headless").setArchived(true))
                    .build(),
            )

            val session = OrreryClient(server.baseUrl).headless().single()

            assertTrue(session.archived)
        }
    }

    /**
     * Runs [call] after a read has left a keep-alive connection in the pool, so the write travels on
     * that pooled connection, the one a retry would resend on. The server reads the write and closes
     * the connection unanswered.
     */
    private fun assertDroppedWriteIsNoAnswerAndSentOnce(procedure: String, call: (OrreryClient) -> Unit) {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            server.drop(procedure)
            val client = OrreryClient(server.baseUrl)
            client.epics()

            assertThrows(NoAnswerException::class.java) {
                call(client)
            }

            assertEquals(1, server.requests.count { it.rawPath == procedure })
        }
    }

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_MOVE = "/orrery.v1.Epics/Move"
        const val EPICS_CONTINUE = "/orrery.v1.Epics/Continue"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
    }
}
