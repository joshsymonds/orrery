package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextReplacement
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Account as AccountProto
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRemoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRemoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRenameRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRenameResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext as RouteContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry as RouteRegistryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat as RouteSeatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AccountsDroidActsTest {
    private companion object {
        const val ACCOUNTS_LIST = "/orrery.v1.Accounts/List"
        const val ACCOUNTS_RENAME = "/orrery.v1.Accounts/Rename"
        const val ACCOUNTS_REMOVE = "/orrery.v1.Accounts/Remove"
        const val ROUTES_GET = "/orrery.v1.Routes/Get"
        const val ROUTES_SET_CONTEXT_ACCOUNT = "/orrery.v1.Routes/SetContextAccount"

        const val FIRST = "acct-example-1"
        const val SECOND = "acct-example-2"
        const val FIRST_NAME = "max-1"
        const val SECOND_NAME = "max-2"
        const val RENAMED = "max-main"

        const val NO_ANSWER = "Could not tell whether that landed: no answer came. This is what stands now."
        const val IN_USE_NOTE =
            "max-1 is in use by personal and tiltyard. Move them to another account first, then remove it. " +
                "Nothing changed."

        val FIRST_ACCOUNT: AccountProto = account(FIRST, FIRST_NAME, "max", "fp-example-1")
        val SECOND_ACCOUNT: AccountProto = account(SECOND, SECOND_NAME, "pro", "fp-example-2")

        val SEAT_FORWARD: RouteSeatProto = RouteSeatProto.newBuilder()
            .setId("seat-forward")
            .setAuthMode("forward")
            .setUpstream("https://api.example.test")
            .setModel("example-model-a")
            .build()

        val SEAT_INJECT: RouteSeatProto = RouteSeatProto.newBuilder()
            .setId("seat-inject")
            .setAuthMode("inject")
            .setUpstream("https://inject.example.test")
            .setModel("example-model-b")
            .build()

        val SEAT_SIGV4: RouteSeatProto = RouteSeatProto.newBuilder()
            .setId("seat-sigv4")
            .setAuthMode("sigv4")
            .setUpstream("https://bedrock.example.test")
            .setModel("example-model-c")
            .setAwsProfile("example-profile")
            .setAwsRegion("example-region-1")
            .build()

        fun account(id: String, name: String, plan: String, fingerprint: String): AccountProto =
            AccountProto.newBuilder()
                .setId(id)
                .setName(name)
                .setProvider("anthropic")
                .setPlan(plan)
                .setFingerprint(fingerprint)
                .setCreated(1_700_000_000L)
                .build()

        /** The registry: personal sends with [personal], tiltyard with [tiltyard]; "" is the session's own login. */
        fun registryOf(personal: String, tiltyard: String): RouteRegistryProto =
            RouteRegistryProto.newBuilder()
                .setDefaultContext("personal")
                .addSeats(SEAT_FORWARD)
                .addSeats(SEAT_INJECT)
                .addSeats(SEAT_SIGV4)
                .addContexts(contextOf("personal", "seat-forward", personal))
                .addContexts(contextOf("tiltyard", "seat-inject", tiltyard))
                .build()

        fun contextOf(id: String, seat: String, account: String): RouteContextProto =
            RouteContextProto.newBuilder().setId(id).setDefaultSeat(seat).setAccount(account).build()
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun serveAccounts(server: FakeOrreryServer) {
        server.respond(
            ACCOUNTS_LIST,
            AccountsListResponse.newBuilder().addAccounts(FIRST_ACCOUNT).addAccounts(SECOND_ACCOUNT).build(),
        )
    }

    private fun serveRoutes(server: FakeOrreryServer, registry: RouteRegistryProto) {
        server.respond(ROUTES_GET, RoutesGetResponse.newBuilder().setRegistry(registry).build())
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent { AccountsScreen(client = client, onBack = {}) }
    }

    private fun awaitAccounts() {
        composeRule.awaitUntil("the accounts load") { nodeExists("account-$FIRST") }
    }

    private fun nodeExists(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    /** Taps [tag] through its click action, as a tap inside a sheet runs under Robolectric. */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** The text a merged row shows, its title and its second line, joined with a space. */
    private fun rowText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    /** The requests the server has had for [path], in order. */
    private fun sent(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.rawPath == path }

    /** Opens the ⋯ of [FIRST] and then its Rename, with the rename field waiting. */
    private fun openRenameOfFirst() {
        tap("account-more-$FIRST")
        tap("account-rename")
        composeRule.awaitExactlyOne(hasTestTag("rename-field"))
    }

    @Test
    fun theMenuRenamesAnAccountAndTheListShowsTheNameTheServerAnswers() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            server.respond(
                ACCOUNTS_RENAME,
                AccountsRenameResponse.newBuilder().setAccount(FIRST_ACCOUNT.toBuilder().setName(RENAMED)).build(),
            )
            setScreen(server)
            awaitAccounts()

            openRenameOfFirst()
            composeRule.onNodeWithTag("rename-field").performTextReplacement(RENAMED)
            tap("rename-confirm")
            composeRule.awaitNone(hasTestTag("act-rename"))

            composeRule.onAllNodesWithText(RENAMED).assertCountEquals(1)
            assertEquals(
                listOf(AccountsRenameRequest.newBuilder().setId(FIRST).setName(RENAMED).build()),
                sent(server, ACCOUNTS_RENAME).map { AccountsRenameRequest.parseFrom(it.body) },
            )
        }
    }

    @Test
    fun aRenameTheServerRefusesKeepsTheNameItHadAndSaysSo() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            server.fail(ACCOUNTS_RENAME, Code.INVALID_ARGUMENT, "example refusal from orreryd")
            setScreen(server)
            awaitAccounts()

            openRenameOfFirst()
            composeRule.onNodeWithTag("rename-field").performTextReplacement(RENAMED)
            tap("rename-confirm")
            composeRule.awaitUntil("the refusal's note") { nodeExists("act-note") }

            composeRule.onNodeWithTag("act-note")
                .assertTextEquals("Not renamed: example refusal from orreryd Nothing changed.")
            composeRule.onAllNodesWithTag("act-rename").assertCountEquals(1)
            assertEquals(1, composeRule.onAllNodesWithText(FIRST_NAME).fetchSemanticsNodes().size)
        }
    }

    @Test
    fun theMenuRemovesAnAccountOnlyAfterItsAskIsConfirmed() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            server.respond(ACCOUNTS_REMOVE, AccountsRemoveResponse.getDefaultInstance())
            setScreen(server)
            awaitAccounts()

            tap("account-more-$FIRST")
            tap("account-remove")
            composeRule.awaitExactlyOne(hasTestTag("remove-title"))
            composeRule.onNodeWithTag("remove-title").assertTextEquals("Remove $FIRST_NAME?")
            assertTrue(sent(server, ACCOUNTS_REMOVE).isEmpty())

            tap("remove-confirm")
            composeRule.awaitNone(hasTestTag("account-$FIRST"))

            composeRule.awaitExactlyOne(hasTestTag("account-$SECOND"))
            assertEquals(
                listOf(AccountsRemoveRequest.newBuilder().setId(FIRST).build()),
                sent(server, ACCOUNTS_REMOVE).map { AccountsRemoveRequest.parseFrom(it.body) },
            )
        }
    }

    @Test
    fun keepSendsNothingAndTheAccountStays() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            setScreen(server)
            awaitAccounts()

            tap("account-more-$FIRST")
            tap("account-remove")
            composeRule.awaitExactlyOne(hasTestTag("remove-title"))
            tap("remove-keep")
            composeRule.awaitNone(hasTestTag("act-remove"))

            Quiescence.awaitUntil("the work settles", step = { composeRule.waitForIdle() }) {
                Quiescence.inFlight() == null
            }
            assertTrue(sent(server, ACCOUNTS_REMOVE).isEmpty())
            assertTrue(nodeExists("account-$FIRST"))
        }
    }

    @Test
    fun aRemovalRefusedAsInUseNamesEveryContextThatHoldsIt() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            // The page reads the routes once, with only personal on the account; the refusal's fresh read holds both.
            val reads = AtomicInteger()
            server.route(ROUTES_GET) {
                val registry = if (reads.incrementAndGet() == 1) registryOf(FIRST, "") else registryOf(FIRST, FIRST)
                Reply.Message(RoutesGetResponse.newBuilder().setRegistry(registry).build(), 0L)
            }
            server.fail(ACCOUNTS_REMOVE, Code.FAILED_PRECONDITION, "account is in use")
            setScreen(server)
            awaitAccounts()

            tap("account-more-$FIRST")
            tap("account-remove")
            composeRule.awaitExactlyOne(hasTestTag("remove-title"))
            tap("remove-confirm")
            composeRule.awaitUntil("the in-use note") { nodeExists("act-note") }

            composeRule.onNodeWithTag("act-note").assertTextEquals(IN_USE_NOTE)
            composeRule.onAllNodesWithTag("remove-confirm").assertCountEquals(1)
            assertTrue(nodeExists("account-$FIRST"))
            assertEquals(2, sent(server, ROUTES_GET).size)
        }
    }

    @Test
    fun aWriteWithNoAnswerReadsTheAccountsAgainAndSaysWhatStands() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            server.drop(ACCOUNTS_RENAME)
            setScreen(server)
            awaitAccounts()

            openRenameOfFirst()
            composeRule.onNodeWithTag("rename-field").performTextReplacement(RENAMED)
            tap("rename-confirm")
            composeRule.awaitUntil("the no-answer note") {
                composeRule.onAllNodesWithText(NO_ANSWER).fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.awaitNone(hasTestTag("act-rename"))
            composeRule.awaitUntil("the accounts read again") {
                sent(server, ACCOUNTS_LIST).size == 2 && Quiescence.inFlight() == null
            }
            composeRule.waitForIdle()
            awaitAccounts()

            composeRule.onAllNodesWithText(NO_ANSWER).assertCountEquals(1)
            assertEquals(1, composeRule.onAllNodesWithText(FIRST_NAME).fetchSemanticsNodes().size)
        }
    }

    @Test
    fun aContextsChevronSetsOnlyThatContextsAccount() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            server.respond(
                ROUTES_SET_CONTEXT_ACCOUNT,
                RoutesSetContextAccountResponse.newBuilder().setRegistry(registryOf(SECOND, "")).build(),
            )
            setScreen(server)
            awaitAccounts()
            val tiltyardBefore = rowText("context-tiltyard")

            tap("context-personal")
            composeRule.awaitExactlyOne(hasTestTag("act-pick"))
            tap("pick-$SECOND")
            composeRule.awaitNone(hasTestTag("act-pick"))

            composeRule.awaitUntil("the context shows its new account") {
                rowText("context-personal").contains(SECOND_NAME)
            }
            assertFalse(rowText("context-personal").contains(FIRST_NAME))
            assertEquals(tiltyardBefore, rowText("context-tiltyard"))
            assertEquals(
                listOf(RoutesSetContextAccountRequest.newBuilder().setContext("personal").setAccount(SECOND).build()),
                sent(server, ROUTES_SET_CONTEXT_ACCOUNT).map { RoutesSetContextAccountRequest.parseFrom(it.body) },
            )
        }
    }

    @Test
    fun theSeatsChevronListsEverySeat() {
        FakeOrreryServer().use { server ->
            serveAccounts(server)
            serveRoutes(server, registryOf(FIRST, ""))
            setScreen(server)
            awaitAccounts()

            tap("seats-row")
            composeRule.awaitExactlyOne(hasTestTag("act-seats"))

            composeRule.onAllNodesWithTag("seat-seat-forward").assertCountEquals(1)
            composeRule.onAllNodesWithText("forward · https://api.example.test · example-model-a").assertCountEquals(1)
            composeRule.onAllNodesWithTag("seat-seat-inject").assertCountEquals(1)
            composeRule.onAllNodesWithText("inject · https://inject.example.test · example-model-b")
                .assertCountEquals(1)
            composeRule.onAllNodesWithTag("seat-seat-sigv4").assertCountEquals(1)
            composeRule.onAllNodesWithText("sigv4 · region example-region-1 · example-model-c").assertCountEquals(1)
        }
    }
}
