package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReading
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReadingsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSeatSpend
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class UsageClientsDroidClientTest {
    @Test
    fun usageReadingsMapsEachReadingAndMapsAnUnsetUtilizationAndResetToNull() {
        FakeOrreryServer().use { server ->
            server.respond(
                USAGE_READINGS,
                UsageReadingsResponse.newBuilder().addReadings(FIVE_HOUR_READING).addReadings(WEEKLY_READING_UNSET).build(),
            )

            val rows = OrreryClient(server.baseUrl).usageReadings()

            assertEquals(listOf(FIVE_HOUR_ROW, WEEKLY_ROW_UNSET), rows)
            assertEquals(listOf(USAGE_READINGS), server.requests.map { it.rawPath })
            assertRequest(USAGE_READINGS, server.requests.single())
        }
    }

    @Test
    fun usageSpendSendsTheSpanAndMapsEachSeat() {
        FakeOrreryServer().use { server ->
            server.respond(
                USAGE_SPEND,
                UsageSpendResponse.newBuilder().addSeats(SEAT_ALPHA_SPEND).addSeats(SEAT_BETA_SPEND).build(),
            )

            val rows = OrreryClient(server.baseUrl).usageSpend(from = SPAN_FROM, to = SPAN_TO)

            assertEquals(listOf(SEAT_ALPHA_ROW, SEAT_BETA_ROW), rows)
            val request = server.requests.single()
            assertRequest(USAGE_SPEND, request)
            assertEquals(
                UsageSpendRequest.newBuilder().setFrom(SPAN_FROM).setTo(SPAN_TO).build(),
                UsageSpendRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun usageReadingsThrowsWhenTheReadFails() {
        FakeOrreryServer().use { server ->
            server.fail(USAGE_READINGS, Code.UNAVAILABLE, "usage is unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).usageReadings()
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals(listOf(USAGE_READINGS), server.requests.map { it.rawPath })
        }
    }

    @Test
    fun usageSpendThrowsWhenTheReadFails() {
        FakeOrreryServer().use { server ->
            server.fail(USAGE_SPEND, Code.UNAVAILABLE, "spend is unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).usageSpend(from = SPAN_FROM, to = SPAN_TO)
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals(listOf(USAGE_SPEND), server.requests.map { it.rawPath })
        }
    }

    private fun assertRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val USAGE_READINGS = "/orrery.v1.Usage/Readings"
        const val USAGE_SPEND = "/orrery.v1.Usage/Spend"

        const val SPAN_FROM = 1_700_000_000L
        const val SPAN_TO = 1_700_086_400L

        val FIVE_HOUR_READING: UsageReading = UsageReading.newBuilder()
            .setAccount("acct-example-1")
            .setClaim("five_hour")
            .setUtilization(0.42)
            .setReset(1_700_018_000L)
            .setStatus("allowed")
            .setObserved(1_700_000_100L)
            .setDropped(false)
            .build()

        /** A reading that gives neither a utilization nor a reset, and that orrery dropped. */
        val WEEKLY_READING_UNSET: UsageReading = UsageReading.newBuilder()
            .setAccount("acct-example-2")
            .setClaim("seven_day")
            .setStatus("unknown")
            .setObserved(1_700_000_200L)
            .setDropped(true)
            .build()

        val FIVE_HOUR_ROW = UsageReadingRow(
            account = "acct-example-1",
            claim = "five_hour",
            utilization = 0.42,
            reset = 1_700_018_000L,
            status = "allowed",
            observed = 1_700_000_100L,
            dropped = false,
        )

        val WEEKLY_ROW_UNSET = UsageReadingRow(
            account = "acct-example-2",
            claim = "seven_day",
            utilization = null,
            reset = null,
            status = "unknown",
            observed = 1_700_000_200L,
            dropped = true,
        )

        val SEAT_ALPHA_SPEND: UsageSeatSpend = UsageSeatSpend.newBuilder()
            .setSeat("seat-example-alpha")
            .setBilledCostNanoUsd(4_200_000_000L)
            .setListCostNanoUsd(5_100_000_000L)
            .setUnknownCostRequests(0L)
            .build()

        val SEAT_BETA_SPEND: UsageSeatSpend = UsageSeatSpend.newBuilder()
            .setSeat("seat-example-beta")
            .setBilledCostNanoUsd(0L)
            .setListCostNanoUsd(900_000_000L)
            .setUnknownCostRequests(3L)
            .build()

        val SEAT_ALPHA_ROW = SeatSpendRow(
            seat = "seat-example-alpha",
            billedNanoUsd = 4_200_000_000L,
            listNanoUsd = 5_100_000_000L,
            unknownCostRequests = 0L,
        )

        val SEAT_BETA_ROW = SeatSpendRow(
            seat = "seat-example-beta",
            billedNanoUsd = 0L,
            listNanoUsd = 900_000_000L,
            unknownCostRequests = 3L,
        )
    }
}
