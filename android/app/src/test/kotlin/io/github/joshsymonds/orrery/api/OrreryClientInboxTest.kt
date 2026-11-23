package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract as ContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientInboxTest {
    private val epicProto = Epic.newBuilder()
        .setId("example-repo/example-epic")
        .setRepository("example-repo")
        .setSlug("example-epic")
        .setOutcome("in progress")
        .setChanged(4102444800L)
        .setLive(true)
        .setEndedSteps(3)
        .setProblem("efforts/1/state.json: does not parse")
        .setSeen(1700000500L)
        .build()

    private val epicRow = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = "in progress",
        changed = 4102444800L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 3,
        seen = 1700000500L,
    )

    @Test
    fun epicsMapTheInboxFieldsOfEachEpic() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder()
                    .addEpics(
                        epicProto.toBuilder()
                            .setArchived(true)
                            .setSteering("example-session")
                            .setPath("/example/repo")
                            .setPaused(true)
                            .setPauseSource("owner")
                            .setParked("example park")
                            .setOpenQuestions(2),
                    )
                    .build(),
            )

            assertEquals(
                listOf(
                    epicRow.copy(
                        archived = true,
                        steering = "example-session",
                        path = "/example/repo",
                        paused = true,
                        pauseSource = "owner",
                        parked = "example park",
                        openQuestions = 2,
                    ),
                ),
                OrreryClient(server.baseUrl).epics(),
            )
        }
    }

    @Test
    fun panesCarryTheirOrreryId() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId("%3").setOrreryId("example-session"))
                    .addPanes(PaneProto.newBuilder().setId("%4"))
                    .build(),
            )

            assertEquals(
                listOf("example-session", ""),
                OrreryClient(server.baseUrl).panes().map { it.orreryId },
            )
        }
    }

    @Test
    fun headlessSessionsCarryTheirArchivedFlag() {
        FakeOrreryServer().use { server ->
            server.respond(
                HEADLESS_LIST,
                ListHeadlessResponse.newBuilder()
                    .addSessions(HeadlessSessionProto.newBuilder().setId("example-headless").setArchived(true))
                    .addSessions(HeadlessSessionProto.newBuilder().setId("example-other"))
                    .build(),
            )

            assertEquals(
                listOf(true, false),
                OrreryClient(server.baseUrl).headless().map { it.archived },
            )
        }
    }

    @Test
    fun archivedSessionsReturnsOnlyTheIdsOfArchivedSessions() {
        FakeOrreryServer().use { server ->
            server.respond(
                SESSIONS_LIST,
                ListResponse.newBuilder()
                    .addSessions(SessionProto.newBuilder().setId("example-a").setArchived(true))
                    .addSessions(SessionProto.newBuilder().setId("example-b").setArchived(false))
                    .addSessions(SessionProto.newBuilder().setId("example-c").setArchived(true))
                    .addSessions(SessionProto.newBuilder().setId("example-d"))
                    .build(),
            )

            assertEquals(setOf("example-a", "example-c"), OrreryClient(server.baseUrl).archivedSessions())
            assertConnectRequest(SESSIONS_LIST, server.requests.single())
        }
    }

    @Test
    fun contractsMapEveryFieldInListOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(
                        ContractProto.newBuilder()
                            .setId("example-contract")
                            .setRepository("example-repo")
                            .setTitle("Example contract title")
                            .setCare("serious")
                            .setState("proposed")
                            .setVersion(3L)
                            .setOpenDecisions(2L)
                            .setConversation("example-session")
                            .setCreated(4102444600L)
                            .setChanged(4102444700L),
                    )
                    .build(),
            )

            assertEquals(
                listOf(
                    ContractRow(
                        id = "example-contract",
                        repository = "example-repo",
                        title = "Example contract title",
                        care = "serious",
                        state = "proposed",
                        version = 3L,
                        openDecisions = 2L,
                        conversation = "example-session",
                        created = 4102444600L,
                        changed = 4102444700L,
                    ),
                ),
                OrreryClient(server.baseUrl).contracts(),
            )
            assertConnectRequest(CONTRACTS_LIST, server.requests.single())
        }
    }

    @Test
    fun contractsAbsentOnAnOldServerIsEmpty() {
        for (code in listOf(Code.UNIMPLEMENTED, Code.NOT_FOUND)) {
            FakeOrreryServer().use { server ->
                server.fail(CONTRACTS_LIST, code)

                assertEquals(emptyList<ContractRow>(), OrreryClient(server.baseUrl).contracts())
            }
        }
    }

    @Test
    fun archivedSessionsAbsentOnAnOldServerIsEmpty() {
        for (code in listOf(Code.UNAVAILABLE, Code.UNIMPLEMENTED, Code.NOT_FOUND)) {
            FakeOrreryServer().use { server ->
                server.fail(SESSIONS_LIST, code)

                assertEquals(emptySet<String>(), OrreryClient(server.baseUrl).archivedSessions())
            }
        }
    }

    @Test
    fun contractsAndArchivedSessionsThrowAnyOtherError() {
        FakeOrreryServer().use { server ->
            server.fail(CONTRACTS_LIST, Code.INTERNAL_ERROR)
            server.fail(SESSIONS_LIST, Code.INTERNAL_ERROR)
            val client = OrreryClient(server.baseUrl)

            assertThrows(OrreryCallException::class.java) { client.contracts() }
            assertThrows(OrreryCallException::class.java) { client.archivedSessions() }
        }
    }

    @Test
    fun archiveSendsTheKindAndIdAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_ARCHIVE, EpicsArchiveResponse.getDefaultInstance())

            assertEquals(
                StepCallResult.Landed,
                OrreryClient(server.baseUrl).archive("headless", "example-headless"),
            )

            val request = server.requests.single()
            assertConnectRequest(EPICS_ARCHIVE, request)
            val sent = EpicsArchiveRequest.parseFrom(request.body)
            assertEquals("headless", sent.kind)
            assertEquals("example-headless", sent.id)
        }
    }

    @Test
    fun unarchiveSendsTheKindAndIdAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_UNARCHIVE, EpicsUnarchiveResponse.getDefaultInstance())

            assertEquals(
                StepCallResult.Landed,
                OrreryClient(server.baseUrl).unarchive("epic", "example-repo/example-epic"),
            )

            val request = server.requests.single()
            assertConnectRequest(EPICS_UNARCHIVE, request)
            val sent = EpicsUnarchiveRequest.parseFrom(request.body)
            assertEquals("epic", sent.kind)
            assertEquals("example-repo/example-epic", sent.id)
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
        const val EPICS_ARCHIVE = "/orrery.v1.Epics/Archive"
        const val EPICS_UNARCHIVE = "/orrery.v1.Epics/Unarchive"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val HEADLESS_LIST = "/orrery.v1.Sessions/ListHeadless"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
    }
}
