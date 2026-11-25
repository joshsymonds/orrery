package io.github.joshsymonds.orrery

import android.content.Context
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.gen.orrery.v1.Account
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageReadingsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSpendResponse
import io.github.joshsymonds.orrery.ui.MOVE_TOAST_LINE_TAG
import io.github.joshsymonds.orrery.ui.MOVE_TOAST_TAG
import io.github.joshsymonds.orrery.ui.MOVE_TOAST_UNDO_TAG
import io.github.joshsymonds.orrery.ui.PaneListScreen
import io.github.joshsymonds.orrery.ui.USAGE_BAND_TAG
import io.github.joshsymonds.orrery.ui.USAGE_CONFIRM_CANCEL_TAG
import io.github.joshsymonds.orrery.ui.USAGE_CONFIRM_MOVE_TAG
import io.github.joshsymonds.orrery.ui.USAGE_CONFIRM_REFUSAL_TAG
import io.github.joshsymonds.orrery.ui.USAGE_CONFIRM_TAG
import io.github.joshsymonds.orrery.ui.USAGE_SHEET_TAG
import io.github.joshsymonds.orrery.ui.usageCardTag
import io.github.joshsymonds.orrery.ui.usageUseTag
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.ConcurrentHashMap

/** The fake clock the inbox reads at, in unix seconds. */
private const val NOW = 1_700_000_000L

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val LIST_HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val HOST_STATUS_PATH = "/orrery.v1.Host/Status"
private const val ACCOUNTS_LIST_PATH = "/orrery.v1.Accounts/List"
private const val ROUTES_GET_PATH = "/orrery.v1.Routes/Get"
private const val ROUTES_SET_PATH = "/orrery.v1.Routes/SetContextAccount"
private const val USAGE_READINGS_PATH = "/orrery.v1.Usage/Readings"
private const val USAGE_SPEND_PATH = "/orrery.v1.Usage/Spend"
private const val HOST = "example-host"

/** The invented accounts' ids, as the server holds them. The tests name them max-1, pro-1, max-2 and max-3. */
private const val MAX_1 = "example-account-1"
private const val PRO_1 = "example-account-2"
private const val MAX_2 = "example-account-3"
private const val MAX_3 = "example-account-4"

/**
 * Where the four invented contexts start: personal and sandbox on max-1, tiltyard on pro-1, and work on max-3. Use on max-2
 * moves all four, from three different accounts.
 */
private val STARTING = mapOf("personal" to MAX_1, "tiltyard" to PRO_1, "sandbox" to MAX_1, "work" to MAX_3)

/**
 * The contexts with max-1 in use: personal, tiltyard and sandbox all send max-1, and work uses the session's own login.
 * Every named context names max-1, so max-1 reads "in use" and the other accounts offer Use.
 */
private val ALL_ON_MAX_1 = mapOf("personal" to MAX_1, "tiltyard" to MAX_1, "sandbox" to MAX_1, "work" to "")

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class UsageDroidUseTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    /** Each context's account id as the fake server holds it: a Set changes it, and a Get or the inbox's read reports it. */
    private val accountOf = ConcurrentHashMap<String, String>()

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun account(id: String, name: String, plan: String, fingerprint: String): Account = Account.newBuilder()
        .setId(id)
        .setName(name)
        .setProvider("example-provider")
        .setPlan(plan)
        .setFingerprint(fingerprint)
        .build()

    /** Four invented accounts: max-1, pro-1, max-2 and max-3, the last three named by no context at the start. */
    private fun accountsProto(): AccountsListResponse = AccountsListResponse.newBuilder()
        .addAccounts(account(MAX_1, "max-1", "Claude Max", "3f9a1c0d7e55"))
        .addAccounts(account(PRO_1, "pro-1", "Claude Pro", "e81f05aa9c10"))
        .addAccounts(account(MAX_2, "max-2", "Claude Max", "b0217e6d4f32"))
        .addAccounts(account(MAX_3, "max-3", "Claude Max", "7c3d0e9a1b64"))
        .build()

    private fun seat(id: String, authMode: String): RouteSeat = RouteSeat.newBuilder()
        .setId(id)
        .setAuthMode(authMode)
        .build()

    private fun routeContext(id: String, defaultSeat: String): RouteContext = RouteContext.newBuilder()
        .setId(id)
        .setDefaultSeat(defaultSeat)
        .setAccount(accountOf[id].orEmpty())
        .build()

    /** The route registry as the fake holds it now: four contexts, each on the account [accountOf] names it. */
    private fun registry(): RouteRegistry = RouteRegistry.newBuilder()
        .setDefaultContext("personal")
        .addSeats(seat("anthropic", "forward"))
        .addSeats(seat("relay", "inject"))
        .addSeats(seat("work-bedrock", "sigv4"))
        .addContexts(routeContext("personal", "anthropic"))
        .addContexts(routeContext("tiltyard", "relay"))
        .addContexts(routeContext("sandbox", "anthropic"))
        .addContexts(routeContext("work", "work-bedrock"))
        .build()

    /**
     * Routes Get answers with the registry as the fake holds it. SetContextAccount changes its context's account and
     * answers the registry, except that [refusedAfterApplying] is refused after it has changed its account: a call that
     * errors after applying.
     */
    private fun serveRoutes(refusedAfterApplying: String?) {
        server.route(ROUTES_GET_PATH) { Reply.Message(RoutesGetResponse.newBuilder().setRegistry(registry()).build(), 0L) }
        server.route(ROUTES_SET_PATH) { request ->
            val set = RoutesSetContextAccountRequest.parseFrom(request.body)
            accountOf[set.context] = set.account
            if (set.context == refusedAfterApplying) {
                Reply.Failure(Code.FAILED_PRECONDITION, "synthetic refusal", 0L)
            } else {
                Reply.Message(RoutesSetContextAccountResponse.newBuilder().setRegistry(registry()).build(), 0L)
            }
        }
    }

    /** The inbox's reads: nothing running, the accounts, and the usage reads, which hold no readings. */
    private fun serveInbox() {
        server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS_PATH, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(HOST_STATUS_PATH, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto())
        server.respond(USAGE_READINGS_PATH, UsageReadingsResponse.getDefaultInstance())
        server.respond(USAGE_SPEND_PATH, UsageSpendResponse.getDefaultInstance())
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

    /** The inbox with [contexts] as the fake holds them, and the band shown once its accounts have answered. */
    private fun arrangeInbox(contexts: Map<String, String> = STARTING, refusedAfterApplying: String? = null) {
        accountOf.clear()
        accountOf.putAll(contexts)
        serveInbox()
        serveRoutes(refusedAfterApplying)
        setInbox()
        awaitNode(hasTestTag(USAGE_BAND_TAG))
    }

    /** Opens the accounts sheet from the band, and taps Use on [account]'s card, which opens the confirm sheet. */
    private fun useOn(account: String) {
        composeRule.onNodeWithTag(USAGE_BAND_TAG).performClick()
        awaitNode(hasTestTag(usageUseTag(account)))
        composeRule.onNodeWithTag(usageUseTag(account)).performClick()
        awaitNode(hasTestTag(USAGE_CONFIRM_TAG))
    }

    private fun tapMove() {
        composeRule.onNodeWithTag(USAGE_CONFIRM_MOVE_TAG).performClick()
    }

    /** The SetContextAccount calls the fake server has taken, in order, as (context, account id) pairs. */
    private fun setCalls(): List<Pair<String, String>> = server.requests
        .filter { it.rawPath == ROUTES_SET_PATH }
        .map { request -> RoutesSetContextAccountRequest.parseFrom(request.body).let { it.context to it.account } }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil("a node matching ${matcher.description}") {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun countOf(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun inCard(account: String, text: String): SemanticsMatcher =
        hasText(text) and hasAnyAncestor(hasTestTag(usageCardTag(account)))

    @Test
    fun theUsageDroidUseConfirmSheetNamesTheMovingContextsAndTheAccountEachMovesFrom() {
        arrangeInbox()
        useOn("max-2")

        assertEquals(1, countOf(hasText("Move to max-2?")))
        assertEquals(
            1,
            countOf(
                hasText(
                    "personal and sandbox move from max-1. tiltyard moves from pro-1. work moves from max-3.",
                ),
            ),
        )
        assertEquals(1, countOf(hasText("Their sessions send their next request from max-2.")))
        assertEquals(1, countOf(hasTestTag(USAGE_CONFIRM_CANCEL_TAG)))
        assertEquals(1, countOf(hasTestTag(USAGE_CONFIRM_MOVE_TAG)))
        assertEquals(1, countOf(hasText("Move to max-2")))
        assertEquals(0, countOf(hasTestTag(USAGE_SHEET_TAG)))
    }

    @Test
    fun theUsageDroidUseOffersInUseOnTheAccountEveryNamedContextUsesAndUseOnTheRest() {
        arrangeInbox(contexts = ALL_ON_MAX_1)
        composeRule.onNodeWithTag(USAGE_BAND_TAG).performClick()
        awaitNode(hasTestTag(usageCardTag("max-2")))

        assertEquals(1, countOf(inCard("max-1", "in use")))
        assertEquals(0, countOf(hasTestTag(usageUseTag("max-1"))))
        assertEquals(1, countOf(hasTestTag(usageUseTag("max-2"))))
    }

    @Test
    fun theUsageDroidUseMovesEachPlannedContextInPlanOrder() {
        arrangeInbox()
        useOn("max-2")
        tapMove()
        composeRule.awaitUntil("all four moves are sent") { setCalls().size == 4 }

        assertEquals(
            listOf("personal" to MAX_2, "tiltyard" to MAX_2, "sandbox" to MAX_2, "work" to MAX_2),
            setCalls(),
        )
    }

    @Test
    fun theUsageDroidUseRefusalShowsWhatMovedAndWhatDidNotFromRoutesGet() {
        arrangeInbox(refusedAfterApplying = "tiltyard")
        useOn("max-2")
        tapMove()
        awaitNode(hasTestTag(USAGE_CONFIRM_REFUSAL_TAG))

        // tiltyard's call was refused after it applied, so the routes read counts it as moved; the calls stop there.
        assertEquals(
            1,
            countOf(
                hasTestTag(USAGE_CONFIRM_REFUSAL_TAG) and hasText(
                    "Moved: personal and tiltyard. Did not move: sandbox and work. The server refused it: synthetic refusal",
                ),
            ),
        )
        assertEquals(1, countOf(hasTestTag(USAGE_CONFIRM_TAG)))
        assertEquals(listOf("personal" to MAX_2, "tiltyard" to MAX_2), setCalls())
    }

    @Test
    fun theUsageDroidUseDoneToastNamesTheMoveAndOffersUndo() {
        arrangeInbox()
        useOn("max-2")
        tapMove()
        awaitNode(hasTestTag(MOVE_TOAST_TAG))

        assertEquals(
            1,
            countOf(hasTestTag(MOVE_TOAST_LINE_TAG) and hasText("Moved personal, tiltyard, sandbox and work to max-2.")),
        )
        assertEquals(1, countOf(hasTestTag(MOVE_TOAST_UNDO_TAG)))
        assertEquals(0, countOf(hasTestTag(USAGE_CONFIRM_TAG)))
    }

    @Test
    fun theUsageDroidUseUndoSetsEachContextBackToItsOwnAccount() {
        arrangeInbox()
        useOn("max-2")
        tapMove()
        awaitNode(hasTestTag(MOVE_TOAST_UNDO_TAG))
        composeRule.onNodeWithTag(MOVE_TOAST_UNDO_TAG).performClick()
        composeRule.awaitUntil("all four undo calls are sent") { setCalls().size == 8 }

        assertEquals(
            listOf("personal" to MAX_1, "tiltyard" to PRO_1, "sandbox" to MAX_1, "work" to MAX_3),
            setCalls().drop(4),
        )
        composeRule.awaitNone(hasTestTag(MOVE_TOAST_TAG))
    }
}
