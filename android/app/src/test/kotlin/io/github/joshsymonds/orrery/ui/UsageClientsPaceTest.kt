package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.api.LlmAccounts
import io.github.joshsymonds.orrery.api.LlmContext
import io.github.joshsymonds.orrery.api.LlmRoutes
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.SeatSpendRow
import io.github.joshsymonds.orrery.api.UsageReadingRow
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Account as AccountProto
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReading
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReadingsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSeatSpend
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class UsageClientsPaceTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun readReadsTheAccountsTheReadingsAndTheSevenDaysOfSpendBeforeNow() {
        FakeOrreryServer().use { server ->
            serveAll(server)
            val reads = UsageReads(OrreryClient(server.baseUrl)) { NOW }

            runBlocking { reads.read() }

            assertEquals(LLM_ACCOUNTS, reads.accounts)
            assertEquals(listOf(FIVE_HOUR_ROW), reads.readings)
            assertEquals(listOf(SEAT_ROW), reads.spend)
            assertFalse(reads.failed)
            assertEquals(listOf(ACCOUNTS_LIST, ROUTES_GET, USAGE_READINGS, USAGE_SPEND), server.requests.map { it.rawPath })
            assertEquals(
                UsageSpendRequest.newBuilder().setFrom(NOW - SEVEN_DAYS).setTo(NOW).build(),
                UsageSpendRequest.parseFrom(server.requests.last().body),
            )
        }
    }

    @Test
    fun spendIsReadAtMostOnceEverySixtySecondsOfTheClockWhileTheAccountsAreReadEveryTime() {
        FakeOrreryServer().use { server ->
            serveAll(server)
            var clock = NOW
            val reads = UsageReads(OrreryClient(server.baseUrl)) { clock }

            runBlocking {
                repeat(10) {
                    reads.read()
                    clock += 20
                }
            }

            // Reads at NOW and every 20 s after, to NOW + 180: the Spend reads are those at least 60 s apart.
            assertEquals(
                listOf(NOW, NOW + 60, NOW + 120, NOW + 180),
                requestsTo(server, USAGE_SPEND).map { UsageSpendRequest.parseFrom(it.body).to },
            )
            assertEquals(10, requestsTo(server, ACCOUNTS_LIST).size)
            assertEquals(10, requestsTo(server, USAGE_READINGS).size)
        }
    }

    @Test
    fun aFailedReadKeepsTheDataLastReadAndMarksItFailed() {
        FakeOrreryServer().use { server ->
            serveAll(server)
            var clock = NOW
            val reads = UsageReads(OrreryClient(server.baseUrl)) { clock }
            runBlocking { reads.read() }

            server.fail(ACCOUNTS_LIST, Code.UNAVAILABLE, "accounts are unavailable")
            server.fail(USAGE_READINGS, Code.UNAVAILABLE, "usage is unavailable")
            server.fail(USAGE_SPEND, Code.UNAVAILABLE, "spend is unavailable")
            clock += 60
            runBlocking { reads.read() }

            assertTrue(reads.failed)
            assertEquals(LLM_ACCOUNTS, reads.accounts)
            assertEquals(listOf(FIVE_HOUR_ROW), reads.readings)
            assertEquals(listOf(SEAT_ROW), reads.spend)

            serveAll(server)
            clock += 60
            runBlocking { reads.read() }

            assertFalse(reads.failed)
            assertEquals(LLM_ACCOUNTS, reads.accounts)
            assertEquals(listOf(FIVE_HOUR_ROW), reads.readings)
            assertEquals(listOf(SEAT_ROW), reads.spend)
        }
    }

    @Test
    fun nothingIsReadWhileTheLifecycleIsBelowStarted() {
        val owner = HandLifecycleOwner()
        owner.lifecycle.currentState = Lifecycle.State.CREATED
        FakeOrreryServer().use { server ->
            serveAll(server)
            composeRule.mainClock.autoAdvance = false
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                CompositionLocalProvider(LocalLifecycleOwner provides owner) {
                    rememberUsageReads(client)
                }
            }

            settle()
            composeRule.mainClock.advanceTimeBy(THREE_MINUTES_MILLIS, ignoreFrameDuration = true)
            settle()
            assertEquals(0, requestsTo(server, ACCOUNTS_LIST).size)

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.STARTED }
            composeRule.awaitUntil("the accounts are read once started") { requestsTo(server, ACCOUNTS_LIST).size == 1 }
            settle()
            composeRule.mainClock.advanceTimeBy(ONE_MINUTE_MILLIS, ignoreFrameDuration = true)
            composeRule.awaitUntil("the accounts are read again a minute later") { requestsTo(server, ACCOUNTS_LIST).size == 2 }
            settle()

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.CREATED }
            composeRule.mainClock.advanceTimeBy(THREE_MINUTES_MILLIS, ignoreFrameDuration = true)
            settle()
            assertEquals(2, requestsTo(server, ACCOUNTS_LIST).size)
        }
    }

    /** Lets the composition and every request it started finish, so a read it made is on the server. */
    private fun settle() {
        Quiescence.awaitUntil("the reads settle", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    private fun requestsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.rawPath == path }

    /** Answers every procedure a read makes with the invented data the tests assert on. */
    private fun serveAll(server: FakeOrreryServer) {
        server.respond(ACCOUNTS_LIST, AccountsListResponse.newBuilder().addAccounts(ACCOUNT).build())
        server.respond(ROUTES_GET, RoutesGetResponse.newBuilder().setRegistry(REGISTRY).build())
        server.respond(USAGE_READINGS, UsageReadingsResponse.newBuilder().addReadings(FIVE_HOUR_READING).build())
        server.respond(USAGE_SPEND, UsageSpendResponse.newBuilder().addSeats(SEAT_SPEND).build())
    }

    private companion object {
        const val ACCOUNTS_LIST = "/orrery.v1.Accounts/List"
        const val ROUTES_GET = "/orrery.v1.Routes/Get"
        const val USAGE_READINGS = "/orrery.v1.Usage/Readings"
        const val USAGE_SPEND = "/orrery.v1.Usage/Spend"

        /** An invented time, in unix seconds. */
        const val NOW = 1_700_100_000L
        const val SEVEN_DAYS = 7L * 24 * 60 * 60
        const val ONE_MINUTE_MILLIS = 60_000L
        const val THREE_MINUTES_MILLIS = 180_000L

        val ACCOUNT: AccountProto = AccountProto.newBuilder()
            .setId("acct-example-1")
            .setName("Example Work")
            .setProvider("anthropic")
            .setPlan("max")
            .setFingerprint("fp-example-1")
            .setCreated(1_700_000_000L)
            .build()

        val REGISTRY: RouteRegistry = RouteRegistry.newBuilder()
            .setDefaultContext("ctx-main")
            .addContexts(
                RouteContext.newBuilder()
                    .setId("ctx-main")
                    .setDefaultSeat("seat-forward")
                    .setAccount("acct-example-1"),
            )
            .build()

        val FIVE_HOUR_READING: UsageReading = UsageReading.newBuilder()
            .setAccount("acct-example-1")
            .setClaim("five_hour")
            .setUtilization(0.42)
            .setReset(1_700_018_000L)
            .setStatus("allowed")
            .setObserved(1_700_000_100L)
            .setDropped(false)
            .build()

        val SEAT_SPEND: UsageSeatSpend = UsageSeatSpend.newBuilder()
            .setSeat("seat-example-alpha")
            .setBilledCostNanoUsd(4_200_000_000L)
            .setListCostNanoUsd(5_100_000_000L)
            .setUnknownCostRequests(0L)
            .build()

        val LLM_ACCOUNTS = LlmAccounts(
            accounts = listOf(
                LlmAccount(
                    id = "acct-example-1",
                    name = "Example Work",
                    provider = "anthropic",
                    plan = "max",
                    fingerprint = "fp-example-1",
                    created = 1_700_000_000L,
                ),
            ),
            routes = LlmRoutes(
                defaultContext = "ctx-main",
                seats = emptyList(),
                contexts = listOf(
                    LlmContext(id = "ctx-main", defaultSeat = "seat-forward", models = emptyMap(), account = "acct-example-1"),
                ),
            ),
        )

        val FIVE_HOUR_ROW = UsageReadingRow(
            account = "acct-example-1",
            claim = "five_hour",
            utilization = 0.42,
            reset = 1_700_018_000L,
            status = "allowed",
            observed = 1_700_000_100L,
            dropped = false,
        )

        val SEAT_ROW = SeatSpendRow(
            seat = "seat-example-alpha",
            billedNanoUsd = 4_200_000_000L,
            listNanoUsd = 5_100_000_000L,
            unknownCostRequests = 0L,
        )
    }
}
