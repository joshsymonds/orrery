package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.api.LlmContext
import io.github.joshsymonds.orrery.api.LlmRoutes
import io.github.joshsymonds.orrery.api.LlmSeat
import io.github.joshsymonds.orrery.api.SeatSpendRow
import io.github.joshsymonds.orrery.api.UsageReadingRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UsageModelTest {
    private val now = 1_800_000_000L
    private val hour = 3600L
    private val day = 86400L

    @Test
    fun aDroppedReadingAndOneWhoseResetHasPassedAreUndrawn() {
        val readings = listOf(
            reading("max-1", "5h", utilization = 0.3, dropped = true),
            reading("max-1", "7d", utilization = 0.4, reset = now),
            reading("max-1", "7d_oi", utilization = 0.5, reset = null),
        )
        val row = usageRows(listOf(account("max-1")), routes(), readings, now).single()
        assertNull(row.windows[0])
        assertNull(row.windows[1])
        assertEquals(50, row.windows[2]?.percent)
        assertFalse(row.none)
    }

    @Test
    fun aPercentIsTheRoundedUtilizationAndItsLevelIsWarnFromSixtyAndCritFromEighty() {
        val readings = listOf(
            reading("max-1", "5h", utilization = 0.594),
            reading("max-1", "7d", utilization = 0.604),
            reading("max-1", "7d_oi", utilization = 0.796),
        )
        val windows = usageRows(listOf(account("max-1")), routes(), readings, now).single().windows
        assertEquals(59, windows[0]?.percent)
        assertEquals(UsageLevel.OK, windows[0]?.level)
        assertEquals(60, windows[1]?.percent)
        assertEquals(UsageLevel.WARN, windows[1]?.level)
        assertEquals(80, windows[2]?.percent)
        assertEquals(UsageLevel.CRIT, windows[2]?.level)
    }

    @Test
    fun aLimitedAccountIsCritAndLiftsAtItsLatestRejectedReset() {
        val readings = listOf(
            reading("max-3", "5h", utilization = 1.0, reset = now + hour, status = "rejected"),
            reading("max-3", "7d", utilization = 0.96, reset = now + day, status = "rejected"),
            reading("max-3", "7d_oi", utilization = 0.3, reset = now + 3 * day, status = "allowed"),
        )
        val row = usageRows(listOf(account("max-3")), routes(), readings, now).single()
        assertTrue(row.limited)
        assertEquals(now + day, row.liftsAt)
        assertEquals(listOf(UsageLevel.CRIT, UsageLevel.CRIT, UsageLevel.CRIT), row.windows.map { it?.level })
        assertEquals(LastColumn.Lift(now + day), row.last)
    }

    @Test
    fun anOverageOnlyRejectionDoesNotLimitAnAccount() {
        val readings = listOf(
            reading("pro-1", "overage", utilization = null, reset = null, status = "rejected"),
            reading("pro-1", "5h", utilization = 0.5, reset = now + hour, status = "allowed"),
        )
        val row = usageRows(listOf(account("pro-1")), routes(), readings, now).single()
        assertFalse(row.limited)
        assertNull(row.liftsAt)
        assertEquals(UsageLevel.OK, row.windows[0]?.level)
    }

    @Test
    fun aRejectedWindowWhoseResetHasPassedDoesNotLimitTheAccount() {
        val readings = listOf(
            reading("max-2", "5h", utilization = 1.0, reset = now - 1, status = "rejected"),
            reading("max-2", "7d", utilization = 0.2, reset = now + day, status = "allowed"),
        )
        val row = usageRows(listOf(account("max-2")), routes(), readings, now).single()
        assertFalse(row.limited)
        assertNull(row.windows[0])
        assertEquals(LastColumn.Reset(now + day), row.last)
    }

    @Test
    fun aStaleRowShowsTheAgeOfItsOldestDrawnReadingAndAnHourOldIsNotStale() {
        val readings = listOf(
            reading("max-2", "5h", observed = now - 60),
            reading("max-2", "7d_oi", observed = now - 2 * hour),
            reading("pro-1", "5h", observed = now - hour),
        )
        val rows = usageRows(listOf(account("max-2"), account("pro-1")), routes(), readings, now)
        assertTrue(rows[0].stale)
        assertEquals(now - 2 * hour, rows[0].readAt)
        assertEquals(LastColumn.Age(2 * hour), rows[0].last)
        assertFalse(rows[1].stale)
        assertEquals(now - hour, rows[1].readAt)
    }

    @Test
    fun anAccountWithNoCurrentReadingDrawsNoWindowAndHasNoLastColumn() {
        val readings = listOf(reading("pro-1", "7d", dropped = true))
        val row = usageRows(listOf(account("pro-1")), routes(), readings, now).single()
        assertTrue(row.none)
        assertTrue(row.windows.all { it == null })
        assertNull(row.readAt)
        assertFalse(row.stale)
        assertNull(row.last)
    }

    @Test
    fun inUseAndUsedByNameTheContextsOfTheAccountInRegistryOrder() {
        val contexts = listOf(
            context("personal", "max-1"),
            context("lab", "pro-1"),
            context("side", "max-1"),
            context("own", ""),
        )
        val rows = usageRows(
            listOf(account("max-1"), account("pro-1"), account("max-9")),
            routes(contexts),
            emptyList(),
            now,
        )
        assertTrue(rows[0].inUse)
        assertEquals(listOf("personal", "side"), rows[0].usedBy)
        assertEquals(listOf("lab"), rows[1].usedBy)
        assertFalse(rows[2].inUse)
        assertEquals(emptyList<String>(), rows[2].usedBy)
    }

    @Test
    fun useOfferIsInUseWhenEveryContextNamingAnAccountNamesIt() {
        val contexts = listOf(context("personal", "max-1"), context("side", "max-1"), context("own", ""))
        val rows = usageRows(listOf(account("max-1"), account("max-2")), routes(contexts), emptyList(), now)
        assertEquals(UseOffer.IN_USE, rows[0].useOffer)
        assertEquals(UseOffer.USE, rows[1].useOffer)
        assertEquals(emptyList<MovingContext>(), rows[0].movingContexts)
        assertEquals(
            listOf(MovingContext("personal", "max-1"), MovingContext("side", "max-1")),
            rows[1].movingContexts,
        )
    }

    @Test
    fun useOfferIsNullWhenNoContextNamesAnAccount() {
        val contexts = listOf(context("own", ""), context("other", ""))
        val rows = usageRows(listOf(account("max-1"), account("pro-1")), routes(contexts), emptyList(), now)
        assertTrue(rows.all { it.useOffer == null })
        assertTrue(rows.all { it.movingContexts.isEmpty() })
    }

    @Test
    fun movingContextsListTheContextsOnOtherAccountsInRegistryOrder() {
        val contexts = listOf(
            context("personal", "max-1"),
            context("lab", "pro-1"),
            context("side", "max-1"),
            context("own", ""),
        )
        val rows = usageRows(
            listOf(account("max-1"), account("pro-1"), account("max-2")),
            routes(contexts),
            emptyList(),
            now,
        )
        assertEquals(UseOffer.USE, rows[0].useOffer)
        assertEquals(listOf(MovingContext("lab", "pro-1")), rows[0].movingContexts)
        assertEquals(
            listOf(MovingContext("personal", "max-1"), MovingContext("side", "max-1")),
            rows[1].movingContexts,
        )
        assertEquals(
            listOf(
                MovingContext("personal", "max-1"),
                MovingContext("lab", "pro-1"),
                MovingContext("side", "max-1"),
            ),
            rows[2].movingContexts,
        )
    }

    @Test
    fun spendTotalsCountForwardAndSubscriptionSeatsAtListPrice() {
        val registry = routes(
            seats = listOf(
                seat("api", authMode = "inject", billing = "metered"),
                seat("fwd", authMode = "forward", billing = ""),
                seat("sub", authMode = "inject", billing = "subscription"),
                seat("lab", authMode = "sigv4", billing = "metered"),
            ),
        )
        val spend = listOf(
            SeatSpendRow("api", billedNanoUsd = 4_000L, listNanoUsd = 9_000L, unknownCostRequests = 0L),
            SeatSpendRow("fwd", billedNanoUsd = 0L, listNanoUsd = 5_000L, unknownCostRequests = 0L),
            SeatSpendRow("sub", billedNanoUsd = 0L, listNanoUsd = 7_000L, unknownCostRequests = 0L),
            SeatSpendRow("lab", billedNanoUsd = 1_000L, listNanoUsd = 2_000L, unknownCostRequests = 1L),
            SeatSpendRow("gone", billedNanoUsd = 0L, listNanoUsd = 100L, unknownCostRequests = 0L),
        )
        val totals = spendTotals(spend, registry)
        assertEquals(5_000L, totals.billed)
        assertEquals(23_100L, totals.list)
        assertEquals(listOf("api", "lab"), totals.billedSeats.map { it.seat })
        assertEquals(12_000L, totals.subscriptionList)
    }

    private fun account(id: String): LlmAccount =
        LlmAccount(
            id = id,
            name = "$id name",
            provider = "anthropic",
            plan = "max",
            fingerprint = "fp-$id",
            created = 0L,
        )

    private fun context(id: String, account: String): LlmContext =
        LlmContext(id = id, defaultSeat = "api", models = emptyMap(), account = account)

    private fun seat(id: String, authMode: String, billing: String): LlmSeat =
        LlmSeat(
            id = id,
            authMode = authMode,
            upstream = "https://upstream.invalid",
            model = "",
            billing = billing,
            speed = "",
            awsProfile = "",
            awsRegion = "",
            modelMapEnvFile = "",
        )

    private fun routes(contexts: List<LlmContext> = emptyList(), seats: List<LlmSeat> = emptyList()): LlmRoutes =
        LlmRoutes(defaultContext = "own", seats = seats, contexts = contexts)

    private fun reading(
        account: String,
        claim: String,
        utilization: Double? = 0.5,
        reset: Long? = now + hour,
        status: String = "allowed",
        observed: Long = now - 60,
        dropped: Boolean = false,
    ): UsageReadingRow =
        UsageReadingRow(
            account = account,
            claim = claim,
            utilization = utilization,
            reset = reset,
            status = status,
            observed = observed,
            dropped = dropped,
        )
}
