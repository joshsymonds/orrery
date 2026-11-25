package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageEpicSession as UsageEpicSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageEpicTotalsRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageEpicTotalsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotal as UsageSessionTotalProto
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotalsRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotalsResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientUsageTest {
    @Test
    fun sessionUsageMapsEachTotalAndReadsAnUnsetKindCostAsNull() {
        FakeOrreryServer().use { server ->
            server.respond(
                SESSION_TOTALS,
                UsageSessionTotalsResponse.newBuilder()
                    .addTotals(SUMMED_PROTO)
                    .addTotals(PARTIAL_PROTO)
                    .build(),
            )

            val totals = OrreryClient(server.baseUrl).sessionUsage("sess-example-1")

            assertEquals(listOf(SUMMED_TOTAL, PARTIAL_TOTAL), totals)
            val request = server.requests.single()
            assertRequest(SESSION_TOTALS, request)
            assertEquals("sess-example-1", UsageSessionTotalsRequest.parseFrom(request.body).session)
        }
    }

    @Test
    fun epicUsageMapsEachSessionWithItsSharedFlagAndItsTotals() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPIC_TOTALS,
                UsageEpicTotalsResponse.newBuilder()
                    .addSessions(
                        UsageEpicSessionProto.newBuilder()
                            .setSession("sess-example-step")
                            .setShared(false)
                            .addTotals(SUMMED_PROTO),
                    )
                    .addSessions(
                        UsageEpicSessionProto.newBuilder()
                            .setSession("sess-example-steer")
                            .setShared(true)
                            .addTotals(PARTIAL_PROTO),
                    )
                    .build(),
            )

            val sessions = OrreryClient(server.baseUrl).epicUsage("repo-example/epic-example")

            assertEquals(
                listOf(
                    EpicUsageSession(session = "sess-example-step", shared = false, totals = listOf(SUMMED_TOTAL)),
                    EpicUsageSession(session = "sess-example-steer", shared = true, totals = listOf(PARTIAL_TOTAL)),
                ),
                sessions,
            )
            val request = server.requests.single()
            assertRequest(EPIC_TOTALS, request)
            assertEquals("repo-example/epic-example", UsageEpicTotalsRequest.parseFrom(request.body).epic)
        }
    }

    @Test
    fun sessionUsageThrowsWhenTheReadFails() {
        FakeOrreryServer().use { server ->
            server.fail(SESSION_TOTALS, Code.UNAVAILABLE, "usage is unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).sessionUsage("sess-example-1")
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals(listOf(SESSION_TOTALS), server.requests.map { it.rawPath })
        }
    }

    @Test
    fun epicUsageThrowsWhenTheReadFails() {
        FakeOrreryServer().use { server ->
            server.fail(EPIC_TOTALS, Code.UNAVAILABLE, "usage is unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).epicUsage("repo-example/epic-example")
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals(listOf(EPIC_TOTALS), server.requests.map { it.rawPath })
        }
    }

    private fun assertRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val SESSION_TOTALS = "/orrery.v1.Usage/SessionTotals"
        const val EPIC_TOTALS = "/orrery.v1.Usage/EpicTotals"

        /** A total with every kind's list cost set. */
        val SUMMED_PROTO: UsageSessionTotalProto = UsageSessionTotalProto.newBuilder()
            .setModel("example-model-a")
            .setAgentType("example-agent")
            .setRequests(30L)
            .setInputTokens(100_000L)
            .setOutputTokens(10_000L)
            .setCacheReadTokens(4_000_000L)
            .setCacheWriteTokens(100_000L)
            .setBilledCostNanoUsd(200_000_000L)
            .setListCostNanoUsd(220_000_000L)
            .setListInputNanoUsd(30_000_000L)
            .setListOutputNanoUsd(30_000_000L)
            .setListCacheReadNanoUsd(100_000_000L)
            .setListCacheWriteNanoUsd(60_000_000L)
            .build()

        /**
         * A total whose input kind cost is set to zero, which reads as 0 and not null, and whose output and cache write
         * kind costs are unset, which read as null.
         */
        val PARTIAL_PROTO: UsageSessionTotalProto = UsageSessionTotalProto.newBuilder()
            .setModel("example-model-b")
            .setAgentType("")
            .setRequests(4L)
            .setInputTokens(2_000L)
            .setOutputTokens(500L)
            .setCacheReadTokens(9_000L)
            .setCacheWriteTokens(1_000L)
            .setBilledCostNanoUsd(0L)
            .setListCostNanoUsd(7_000_000L)
            .setListInputNanoUsd(0L)
            .setListCacheReadNanoUsd(7_000_000L)
            .build()

        val SUMMED_TOTAL = UsageTotal(
            model = "example-model-a",
            agentType = "example-agent",
            requests = 30L,
            inputTokens = 100_000L,
            outputTokens = 10_000L,
            cacheReadTokens = 4_000_000L,
            cacheWriteTokens = 100_000L,
            billedNanoUsd = 200_000_000L,
            listNanoUsd = 220_000_000L,
            listInputNanoUsd = 30_000_000L,
            listOutputNanoUsd = 30_000_000L,
            listCacheReadNanoUsd = 100_000_000L,
            listCacheWriteNanoUsd = 60_000_000L,
        )

        val PARTIAL_TOTAL = UsageTotal(
            model = "example-model-b",
            agentType = "",
            requests = 4L,
            inputTokens = 2_000L,
            outputTokens = 500L,
            cacheReadTokens = 9_000L,
            cacheWriteTokens = 1_000L,
            billedNanoUsd = 0L,
            listNanoUsd = 7_000_000L,
            listInputNanoUsd = 0L,
            listOutputNanoUsd = null,
            listCacheReadNanoUsd = 7_000_000L,
            listCacheWriteNanoUsd = null,
        )
    }
}
