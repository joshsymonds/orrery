package io.github.joshsymonds.orrery.ui

import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class BandDataTest {
    private companion object {
        const val CONVERSATION = "conversation-1"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
    }

    private fun epic(id: String, steering: String, live: Boolean = true): Epic =
        Epic.newBuilder()
            .setId(id)
            .setRepository("example-repo")
            .setSlug(id.substringAfter('/'))
            .setLive(live)
            .setSteering(steering)
            .build()

    private fun listing(vararg epics: Epic): EpicsListResponse =
        EpicsListResponse.newBuilder().addAllEpics(epics.toList()).build()

    /** Answers each Get with the epic it asks for, so a detail can be told from the others. */
    private fun answerGets(server: FakeOrreryServer) {
        server.route(EPICS_GET) { request ->
            val id = EpicsGetRequest.parseFrom(request.body).id
            Reply.Message(EpicsGetResponse.newBuilder().setEpic(epic(id, steering = CONVERSATION)).build(), 0L)
        }
    }

    @Test
    fun headEpicsReturnsTheSteeredEpicsInListingOrderWithTheirDetails() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                listing(
                    epic("example-repo/zeta-epic", steering = CONVERSATION),
                    epic("example-repo/other-epic", steering = "conversation-2"),
                    epic("example-repo/alpha-epic", steering = CONVERSATION),
                    epic("example-repo/archived-epic", steering = CONVERSATION, live = false),
                ),
            )
            answerGets(server)

            val result = runBlocking { fetchHeadEpics(OrreryClient(server.baseUrl), CONVERSATION) }

            assertEquals(
                listOf("example-repo/zeta-epic", "example-repo/alpha-epic"),
                result?.steered?.map { it.id },
            )
            assertEquals(
                setOf("example-repo/zeta-epic", "example-repo/alpha-epic"),
                result?.details?.keys,
            )
            assertEquals("zeta-epic", result?.details?.get("example-repo/zeta-epic")?.epic?.slug)
            assertEquals("alpha-epic", result?.details?.get("example-repo/alpha-epic")?.epic?.slug)
            val fetched = server.requests.filter { it.rawPath == EPICS_GET }
                .map { EpicsGetRequest.parseFrom(it.body).id }
            assertEquals(setOf("example-repo/zeta-epic", "example-repo/alpha-epic"), fetched.toSet())
            assertEquals(2, fetched.size)
        }
    }

    @Test
    fun headEpicsOfAnEmptyIdReadsNothing() {
        FakeOrreryServer().use { server ->
            val result = runBlocking { fetchHeadEpics(OrreryClient(server.baseUrl), "") }

            assertEquals(HeadEpics(emptyList(), emptyMap()), result)
            assertTrue(server.requests.isEmpty())
        }
    }

    @Test
    fun headEpicsOfAFailedListIsNull() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_LIST, Code.UNAVAILABLE)

            val result = runBlocking { fetchHeadEpics(OrreryClient(server.baseUrl), CONVERSATION) }

            assertNull(result)
        }
    }

    @Test
    fun headEpicsOfAFailedGetIsNull() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_LIST, listing(epic("example-repo/alpha-epic", steering = CONVERSATION)))
            server.fail(EPICS_GET, Code.UNAVAILABLE)

            val result = runBlocking { fetchHeadEpics(OrreryClient(server.baseUrl), CONVERSATION) }

            assertNull(result)
        }
    }
}
