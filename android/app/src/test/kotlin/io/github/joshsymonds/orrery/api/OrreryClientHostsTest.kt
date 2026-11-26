package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ProposeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ProposeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StartProposal as StartProposalProto
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OrreryClientHostsTest {
    @Test
    fun paneCarriesItsUnreachableFlagAsServed() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId("%14").setHost("example-remote").setUnreachable(true))
                    .addPanes(PaneProto.newBuilder().setId("%15").setHost("example-controller"))
                    .build(),
            )

            val panes = OrreryClient(server.baseUrl).panes().map { it.id to it.unreachable }

            assertEquals(listOf("%14" to true, "%15" to false), panes)
        }
    }

    @Test
    fun headlessSessionCarriesTheHostItRunsOnAsServed() {
        FakeOrreryServer().use { server ->
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder()
                    .addSessions(HeadlessSessionProto.newBuilder().setId(SESSION_ID).setHost("example-remote"))
                    .build(),
            )

            val session = OrreryClient(server.baseUrl).headless().single()

            assertEquals("example-remote", session.host)
        }
    }

    @Test
    fun epicAndItsStepsCarryTheHostTheyLiveOnAsServed() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId(EPIC_ID).setHost("example-remote"))
                    .addSteps(EpicStep.newBuilder().setId("example-step-1").setHost("example-controller"))
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic(EPIC_ID)

            assertEquals("example-remote", detail?.epic?.host)
            assertEquals("example-controller", detail?.steps?.single()?.host)
        }
    }

    @Test
    fun hostStatusCarriesEachHostsEntriesInServerOrderWithReachable() {
        FakeOrreryServer().use { server ->
            server.respond(
                HOST_STATUS,
                HostStatusResponse.newBuilder()
                    .setHost("example-controller")
                    .addHosts(hostEntry("example-controller", 12.5, 40.0, true))
                    .addHosts(hostEntry("example-remote", 0.0, 0.0, false))
                    .build(),
            )

            val hosts = OrreryClient(server.baseUrl).host()?.hosts

            assertEquals(
                listOf(
                    HostEntry("example-controller", 12.5, 40.0, true),
                    HostEntry("example-remote", 0.0, 0.0, false),
                ),
                hosts,
            )
        }
    }

    @Test
    fun proposeReturnsTheServersProposalsInItsOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                PROPOSE,
                ProposeResponse.newBuilder()
                    .addProposals(
                        StartProposalProto.newBuilder().setHost("example-remote").setDir("/example/projects/beta"),
                    )
                    .addProposals(
                        StartProposalProto.newBuilder().setHost("example-controller").setDir("/example/projects/alpha"),
                    )
                    .build(),
            )

            val proposals = OrreryClient(server.baseUrl).propose("example prompt")

            assertEquals("example prompt", ProposeRequest.parseFrom(server.requests.single().body).text)
            assertEquals(
                listOf(
                    StartProposal("example-remote", "/example/projects/beta"),
                    StartProposal("example-controller", "/example/projects/alpha"),
                ),
                proposals,
            )
        }
    }

    @Test
    fun startSendsTheHostItStartsOn() {
        FakeOrreryServer().use { server ->
            server.respond(START, StartResponse.newBuilder().setId(SESSION_ID).build())

            val client = OrreryClient(server.baseUrl)

            val id = client.start("/example/projects/beta", "Example prompt.", SESSION_ID, "example-remote")

            assertEquals(SESSION_ID, id)
            assertEquals("example-remote", StartRequest.parseFrom(server.requests.single().body).host)
        }
    }

    @Test
    fun reachableOfIsFalseOnlyForANamedHostThatIsNotReachable() {
        val hosts = listOf(
            HostEntry("example-controller", 1.0, 2.0, true),
            HostEntry("example-remote", 0.0, 0.0, false),
        )

        assertFalse(reachableOf(hosts, "example-remote"))
        assertTrue(reachableOf(hosts, "example-unlisted"))
        assertTrue(reachableOf(emptyList(), "example-remote"))
    }

    private fun hostEntry(name: String, load: Double, memory: Double, reachable: Boolean): HostEntryProto =
        HostEntryProto.newBuilder()
            .setName(name)
            .setLoadPercent(load)
            .setMemoryPercent(memory)
            .setReachable(reachable)
            .build()

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val EPIC_ID = "example-repo/example-epic"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PROPOSE = "/orrery.v1.Picker/Propose"
        const val START = "/orrery.v1.Control/Start"
    }
}
