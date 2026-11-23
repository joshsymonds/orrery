package io.github.joshsymonds.orrery

import android.content.Context
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.gen.orrery.v1.Account
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val ACCOUNTS_LIST_PATH = "/orrery.v1.Accounts/List"
private const val ROUTES_GET_PATH = "/orrery.v1.Routes/Get"
private const val PANE_TAG = "convo:pane:example-host:%1"
private const val OPEN_ACCOUNTS_TAG = "open-accounts"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AccountsDroidPageTest {
    @get:Rule
    val composeRule = mainThreadEmptyComposeRule()

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
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(paneProto("%1", "alpha-build")).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto())
        server.respond(ROUTES_GET_PATH, routesProto())
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    private fun paneProto(id: String, label: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd("/home/user/alpha")
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(0)
        .setSeen(0)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    /** Three invented accounts: two on the same plan as each other, one on another; only max-1 is used by contexts. */
    private fun accountsProto(): AccountsListResponse = AccountsListResponse.newBuilder()
        .addAccounts(account("example-account-1", "max-1", "Claude Max", "3f9a1c0d7e55"))
        .addAccounts(account("example-account-2", "pro-1", "Claude Pro", "e81f05aa9c10"))
        .addAccounts(account("example-account-3", "max-2", "Claude Max", "b0217e6d4f32"))
        .build()

    private fun account(id: String, name: String, plan: String, fingerprint: String): Account = Account.newBuilder()
        .setId(id)
        .setName(name)
        .setProvider("example-provider")
        .setPlan(plan)
        .setFingerprint(fingerprint)
        .build()

    /**
     * Five invented seats (two forward, two inject, one sigv4) and three contexts: two that send max-1's token and one
     * that uses the session's own login (an empty account).
     */
    private fun routesProto(): RoutesGetResponse = RoutesGetResponse.newBuilder()
        .setRegistry(
            RouteRegistry.newBuilder()
                .setDefaultContext("personal")
                .addSeats(seat("anthropic", "forward"))
                .addSeats(seat("anthropic-alt", "forward"))
                .addSeats(seat("relay", "inject"))
                .addSeats(seat("relay-alt", "inject"))
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

    private fun launch(): ActivityController<MainActivity> =
        Robolectric.buildActivity(MainActivity::class.java).setup().also { launched += it }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** Opens Settings from the inbox, then taps its top row, and waits until Settings has left the screen. */
    private fun openAccounts() {
        awaitNode(hasTestTag(PANE_TAG))
        composeRule.tapInboxAction("open-settings")
        awaitNode(hasTestTag(OPEN_ACCOUNTS_TAG))
        composeRule.onNodeWithTag(OPEN_ACCOUNTS_TAG).performClick()
        composeRule.awaitNone(hasTestTag(OPEN_ACCOUNTS_TAG))
    }

    @Test
    fun tappingAccountsAndRoutesInSettingsShowsAccountsContextsAndSeats() {
        launch()

        openAccounts()

        composeRule.awaitExactlyOne(hasText("max-1"))
        composeRule.awaitExactlyOne(hasText("Claude Max · #3f9a1c · personal, tiltyard"))
        composeRule.awaitExactlyOne(hasText("pro-1"))
        composeRule.awaitExactlyOne(hasText("Claude Pro · #e81f05"))
        composeRule.awaitExactlyOne(hasText("max-2"))
        composeRule.awaitExactlyOne(hasText("Claude Max · #b0217e"))
        composeRule.awaitExactlyOne(hasText("personal"))
        composeRule.awaitExactlyOne(hasText("max-1 · default seat anthropic"))
        composeRule.awaitExactlyOne(hasText("tiltyard"))
        composeRule.awaitExactlyOne(hasText("max-1 · default seat relay"))
        composeRule.awaitExactlyOne(hasText("work"))
        composeRule.awaitExactlyOne(hasText("the session's own login · default seat work-bedrock"))
        composeRule.awaitExactlyOne(hasText("5 seats"))
        composeRule.awaitExactlyOne(hasText("forward 2 · inject 2 · sigv4 1"))
    }

    @Test
    fun systemBackReturnsFromAccountsToSettings() {
        val controller = launch()
        openAccounts()
        composeRule.awaitExactlyOne(hasText("max-1"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(OPEN_ACCOUNTS_TAG))
        composeRule.awaitNone(hasText("max-1"))
    }

    @Test
    fun accountsTitleBackReturnsToSettings() {
        launch()
        openAccounts()
        composeRule.awaitExactlyOne(hasText("max-1"))

        composeRule.onNodeWithTag("title-back").performClick()

        awaitNode(hasTestTag(OPEN_ACCOUNTS_TAG))
        composeRule.awaitNone(hasText("max-1"))
    }

    @Test
    fun aFailedReadShowsANoteAndRetryReadsTheAccountsAgain() {
        server.fail(ACCOUNTS_LIST_PATH, Code.UNAVAILABLE, "synthetic failure")
        launch()

        openAccounts()

        composeRule.awaitExactlyOne(hasText("The server did not answer."))
        server.respond(ACCOUNTS_LIST_PATH, accountsProto())
        composeRule.onNodeWithText("Retry").performClick()
        composeRule.awaitExactlyOne(hasText("max-1"))
    }
}
