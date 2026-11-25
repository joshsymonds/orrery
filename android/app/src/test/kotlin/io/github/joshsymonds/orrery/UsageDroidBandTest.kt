package io.github.joshsymonds.orrery

import android.content.Context
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.Account
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReading
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReadingsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSeatSpend
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendResponse
import io.github.joshsymonds.orrery.ui.PaneListScreen
import io.github.joshsymonds.orrery.ui.USAGE_BAND_TAG
import io.github.joshsymonds.orrery.ui.USAGE_SHEET_ACCOUNTS_TAG
import io.github.joshsymonds.orrery.ui.USAGE_SHEET_TAG
import io.github.joshsymonds.orrery.ui.usageCardTag
import io.github.joshsymonds.orrery.ui.usageRowTag
import io.github.joshsymonds.orrery.ui.usageSheetWindowTag
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlin.math.abs
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

/** The fake clock the inbox reads at, in unix seconds: every reading and reset in these tests is set against it. */
private const val NOW = 1_700_000_000L

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val LIST_HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val HOST_STATUS_PATH = "/orrery.v1.Host/Status"
private const val ACCOUNTS_LIST_PATH = "/orrery.v1.Accounts/List"
private const val ROUTES_GET_PATH = "/orrery.v1.Routes/Get"
private const val USAGE_READINGS_PATH = "/orrery.v1.Usage/Readings"
private const val USAGE_SPEND_PATH = "/orrery.v1.Usage/Spend"
private const val HOST = "example-host"
private const val PANE_TAG = "convo:pane:example-host:%1"
private const val OPEN_NOTICES_TAG = "open-notices"
private const val SPEND_LINE = "Billed 7d \$47.43 list \$1,287.00"

/** The window claims a card draws, in column order: 5h, 7d and Fable. */
private val CLAIMS = listOf("5h", "7d", "7d_oi")

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class UsageDroidBandTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    /** Every activity a test launched, destroyed after it so no screen keeps composing into the next test. */
    private val launched = mutableListOf<ActivityController<MainActivity>>()

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    private fun paneProto(id: String, label: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd("/home/user/alpha")
        .setCommand("claude")
        .setClaudeSession("synthetic-claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(0)
        .setSeen(0)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    /** Four invented accounts: max-1 is used by two contexts, pro-1 is read long ago, max-2 is unused, max-3 is limited. */
    private fun accountsProto(): AccountsListResponse = AccountsListResponse.newBuilder()
        .addAccounts(account("example-account-1", "max-1", "Claude Max", "3f9a1c0d7e55"))
        .addAccounts(account("example-account-2", "pro-1", "Claude Pro", "e81f05aa9c10"))
        .addAccounts(account("example-account-3", "max-2", "Claude Max", "b0217e6d4f32"))
        .addAccounts(account("example-account-4", "max-3", "Claude Max", "7c3d0e9a1b64"))
        .build()

    private fun account(id: String, name: String, plan: String, fingerprint: String): Account = Account.newBuilder()
        .setId(id)
        .setName(name)
        .setProvider("example-provider")
        .setPlan(plan)
        .setFingerprint(fingerprint)
        .build()

    /** Two contexts send max-1's token, and one uses the session's own login. */
    private fun routesProto(): RoutesGetResponse = RoutesGetResponse.newBuilder()
        .setRegistry(
            RouteRegistry.newBuilder()
                .setDefaultContext("personal")
                .addSeats(seat("anthropic", "forward"))
                .addSeats(seat("relay", "inject"))
                .addSeats(seat("work-bedrock", "sigv4"))
                .addContexts(routeContext("personal", "anthropic", "example-account-1"))
                .addContexts(routeContext("tiltyard", "relay", "example-account-1"))
                .addContexts(routeContext("work", "work-bedrock", "")),
        )
        .build()

    private fun seat(id: String, authMode: String): RouteSeat = RouteSeat.newBuilder()
        .setId(id)
        .setAuthMode(authMode)
        .build()

    private fun routeContext(id: String, defaultSeat: String, account: String): RouteContext = RouteContext.newBuilder()
        .setId(id)
        .setDefaultSeat(defaultSeat)
        .setAccount(account)
        .build()

    /**
     * The readings, all observed against [NOW]: max-1 is current and read a minute ago; pro-1 was read two hours ago, so
     * it is stale; max-2 has only a 7d window; max-3 is rejected until an hour from now, so it is limited.
     */
    private fun readingsProto(): UsageReadingsResponse = UsageReadingsResponse.newBuilder()
        .addReadings(reading("example-account-1", "5h", 0.24, NOW + 8_400, "allowed", NOW - 60))
        .addReadings(reading("example-account-1", "7d", 0.54, NOW + 259_200, "allowed", NOW - 60))
        .addReadings(reading("example-account-1", "7d_oi", 0.31, NOW + 259_200, "allowed", NOW - 60))
        .addReadings(reading("example-account-2", "5h", 0.03, NOW + 10_000, "allowed", NOW - 7_200))
        .addReadings(reading("example-account-2", "7d", 0.18, NOW + 400_000, "allowed", NOW - 7_200))
        .addReadings(reading("example-account-3", "7d", 0.72, NOW + 172_800, "allowed", NOW - 720))
        .addReadings(reading("example-account-4", "5h", 1.0, NOW + 3_600, "rejected", NOW - 240))
        .addReadings(reading("example-account-4", "7d", 0.96, NOW + 86_400, "allowed", NOW - 240))
        .addReadings(reading("example-account-4", "7d_oi", 0.80, NOW + 86_400, "allowed", NOW - 240))
        .build()

    private fun reading(
        account: String,
        claim: String,
        utilization: Double,
        reset: Long,
        status: String,
        observed: Long,
    ): UsageReading = UsageReading.newBuilder()
        .setAccount(account)
        .setClaim(claim)
        .setUtilization(utilization)
        .setReset(reset)
        .setStatus(status)
        .setObserved(observed)
        .setDropped(false)
        .build()

    /** Invented seat spend over the week: 47.43 billed and 1,287 at list price, in nano US dollars. */
    private fun spendProto(): UsageSpendResponse = UsageSpendResponse.newBuilder()
        .addSeats(seatSpend("work-bedrock", billed = 44_330_000_000L, list = 46_000_000_000L))
        .addSeats(seatSpend("anthropic", billed = 0L, list = 1_240_000_000_000L))
        .addSeats(seatSpend("relay", billed = 3_100_000_000L, list = 1_000_000_000L))
        .build()

    private fun seatSpend(seat: String, billed: Long, list: Long): UsageSeatSpend = UsageSeatSpend.newBuilder()
        .setSeat(seat)
        .setBilledCostNanoUsd(billed)
        .setListCostNanoUsd(list)
        .build()

    /** The inbox's reads: one pane under the host, and the accounts with their routes. */
    private fun serveInbox() {
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(paneProto("%1", "alpha-build")).build())
        server.respond(LIST_HEADLESS_PATH, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(HOST_STATUS_PATH, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto())
        server.respond(ROUTES_GET_PATH, routesProto())
    }

    private fun serveUsage() {
        server.respond(USAGE_READINGS_PATH, readingsProto())
        server.respond(USAGE_SPEND_PATH, spendProto())
    }

    /** The inbox on its own, with the fake clock at [NOW]: the screen's client is made once, outside the composition. */
    private fun setInbox() {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
                onOpenPane = {},
                onOpenPaneId = {},
                onStartedSession = {},
                onOpenSettings = {},
                onOpenAccounts = {},
                now = { NOW },
            )
        }
    }

    private fun launch(): ActivityController<MainActivity> =
        Robolectric.buildActivity(MainActivity::class.java).setup().also { launched += it }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil("a node matching ${matcher.description}") {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun countOf(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun boundsOf(tag: String): Rect =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot

    /** A text under the band's row for [account], or under its card in the sheet. */
    private fun inRow(account: String, text: String): SemanticsMatcher =
        hasText(text) and hasAnyAncestor(hasTestTag(usageRowTag(account)))

    private fun inCard(account: String, text: String): SemanticsMatcher =
        hasText(text) and hasAnyAncestor(hasTestTag(usageCardTag(account)))

    private fun inWindow(account: String, claim: String, text: String): SemanticsMatcher =
        hasText(text) and hasAnyAncestor(hasTestTag(usageSheetWindowTag(account, claim)))

    /** The HH:mm an instant reads as on this device, as the sheet's limit line shows it. */
    private fun clockText(at: Long): String =
        Instant.ofEpochSecond(at).atZone(ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("HH:mm"))

    @Test
    fun theUsageBandSitsUnderTheInboxHeadAboveTheProjects() {
        serveInbox()
        serveUsage()
        setInbox()
        awaitNode(hasTestTag(PANE_TAG))
        // The spend is read last, after the readings, so its line means the whole band has its data.
        awaitNode(hasText(SPEND_LINE))

        val band = boundsOf(USAGE_BAND_TAG)
        assertTrue("the band sits under the inbox head", band.top >= boundsOf(OPEN_NOTICES_TAG).bottom)
        assertTrue("the band sits above the projects", band.bottom <= boundsOf(PANE_TAG).top)

        listOf("5h", "7d", "Fable", "resets").forEach { head -> assertEquals(1, countOf(hasText(head))) }
        assertEquals(1, countOf(hasText(SPEND_LINE)))

        assertEquals(1, countOf(inRow("max-1", "24%")))
        assertEquals(1, countOf(inRow("max-1", "54%")))
        assertEquals(1, countOf(inRow("max-1", "31%")))
        assertEquals(1, countOf(inRow("max-1", "3d")))
        assertEquals(1, countOf(hasContentDescription("in use") and hasAnyAncestor(hasTestTag(usageRowTag("max-1")))))

        assertEquals(1, countOf(inRow("pro-1", "3%")))
        assertEquals(1, countOf(inRow("pro-1", "18%")))
        assertEquals(1, countOf(inRow("pro-1", "2h ago")))
        assertEquals(1, countOf(inRow("pro-1", "—")))

        assertEquals(1, countOf(inRow("max-2", "72%")))
        assertEquals(1, countOf(inRow("max-2", "2d")))
        assertEquals(2, countOf(inRow("max-2", "—")))

        assertEquals(1, countOf(inRow("max-3", "100%")))
        assertEquals(1, countOf(inRow("max-3", "96%")))
        assertEquals(1, countOf(inRow("max-3", "80%")))
        assertEquals(1, countOf(inRow("max-3", "1h")))
        assertEquals(1, countOf(hasContentDescription("limited") and hasAnyAncestor(hasTestTag(usageRowTag("max-3")))))
    }

    @Test
    fun aFailedAccountsReadLeavesTheInboxAndNoBand() {
        serveInbox()
        serveUsage()
        server.fail(ACCOUNTS_LIST_PATH, Code.UNAVAILABLE, "synthetic failure")
        setInbox()
        awaitNode(hasTestTag(PANE_TAG))
        // The usage reads come after the accounts read, so the spend read having been made means it has failed too.
        composeRule.awaitUntil("the usage spend is read") {
            server.requests.any { it.rawPath == USAGE_SPEND_PATH }
        }
        composeRule.waitForIdle()

        assertEquals(1, countOf(hasTestTag(PANE_TAG)))
        assertEquals(0, countOf(hasTestTag(USAGE_BAND_TAG)))
    }

    @Test
    fun aFailedUsageReadKeepsTheInboxAndDrawsTheAccountsWithoutWindows() {
        serveInbox()
        server.fail(USAGE_READINGS_PATH, Code.UNAVAILABLE, "synthetic failure")
        server.fail(USAGE_SPEND_PATH, Code.UNAVAILABLE, "synthetic failure")
        setInbox()
        awaitNode(hasTestTag(PANE_TAG))
        awaitNode(hasTestTag(usageRowTag("max-1")))

        assertEquals(1, countOf(hasTestTag(PANE_TAG)))
        assertEquals(3, countOf(inRow("max-1", "—")))
    }

    @Test
    fun aTapOnTheBandOpensASheetWhoseCardsHaveThreeEqualWindowColumns() {
        serveInbox()
        serveUsage()
        setInbox()
        awaitNode(hasText(SPEND_LINE))

        composeRule.onNodeWithTag(USAGE_BAND_TAG).performClick()
        awaitNode(hasTestTag(USAGE_SHEET_TAG))

        val accounts = listOf("max-1", "pro-1", "max-2", "max-3")
        accounts.forEach { account -> awaitNode(hasTestTag(usageCardTag(account))) }
        accounts.forEach { account ->
            val windows = CLAIMS.map { claim -> boundsOf(usageSheetWindowTag(account, claim)) }
            assertTrue("$account's windows have width", windows.all { it.width > 0f })
            assertTrue("$account's first and second windows are equal", abs(windows[0].width - windows[1].width) <= 1f)
            assertTrue("$account's second and third windows are equal", abs(windows[1].width - windows[2].width) <= 1f)
            assertTrue("$account's windows run left to right", windows[0].left < windows[1].left && windows[1].left < windows[2].left)
        }

        awaitNode(inCard("max-1", "used by personal, tiltyard · read 1m ago"))
        awaitNode(inCard("max-2", "Claude Max · read 12m ago"))
        awaitNode(inCard("pro-1", "Claude Pro · read 2h ago"))
        awaitNode(inCard("max-3", "limited until ${clockText(NOW + 3_600)} · read 4m ago"))
        assertEquals(1, countOf(hasContentDescription("limited") and hasAnyAncestor(hasTestTag(usageCardTag("max-3")))))

        assertEquals(1, countOf(inWindow("max-1", "5h", "24%")))
        assertEquals(1, countOf(inWindow("max-1", "5h", "in 2h")))
        assertEquals(1, countOf(inWindow("max-1", "7d", "54%")))
        assertEquals(1, countOf(inWindow("max-1", "7d_oi", "31%")))
        assertEquals(1, countOf(inWindow("max-1", "7d_oi", "in 3d")))
        assertEquals(1, countOf(inWindow("max-3", "5h", "100%")))
    }

    @Test
    fun theSheetsAccountsAndRoutesLinkOpensTheAccountsPage() {
        serveInbox()
        serveUsage()
        launch()
        awaitNode(hasText(SPEND_LINE))

        composeRule.onNodeWithTag(USAGE_BAND_TAG).performClick()
        awaitNode(hasTestTag(USAGE_SHEET_ACCOUNTS_TAG))
        composeRule.onNodeWithTag(USAGE_SHEET_ACCOUNTS_TAG).performClick()

        composeRule.awaitNone(hasTestTag(USAGE_BAND_TAG))
        composeRule.awaitExactlyOne(hasText("Claude Max · #3f9a1c · personal, tiltyard"))
    }
}
