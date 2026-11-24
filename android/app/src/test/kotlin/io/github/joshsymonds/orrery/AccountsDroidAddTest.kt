package io.github.joshsymonds.orrery

import android.content.Context
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.gen.orrery.v1.Account
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsAddRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsAddResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
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

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val ACCOUNTS_LIST_PATH = "/orrery.v1.Accounts/List"
private const val ACCOUNTS_ADD_PATH = "/orrery.v1.Accounts/Add"
private const val ROUTES_GET_PATH = "/orrery.v1.Routes/Get"
private const val PANE_TAG = "convo:pane:example-host:%1"
private const val OPEN_ACCOUNTS_TAG = "open-accounts"
private const val ADD_TAG = "title-add"
private const val SHEET_TAG = "add-sheet"
private const val NAME_TAG = "add-name"
private const val PLAN_TAG = "add-plan"
private const val TOKEN_TAG = "add-token"
private const val CANCEL_TAG = "add-cancel"
private const val SUBMIT_TAG = "add-submit"
private const val TOKEN_PLACEHOLDER = "paste the setup-token"
private const val NEW_NAME = "example-account-new"
private const val NEW_FINGERPRINT = "77ab09c2e1d4"
private const val TOKEN_STORED = "example-setup-token-stored"
private const val TOKEN_REFUSED = "example-setup-token-refused"
private const val TOKEN_DROPPED = "example-setup-token-dropped"

/** An invented setup-token that no field value, node text or reopened sheet may hold once its sheet is gone. */
private const val MARKER_TOKEN = "example-setup-token-marker-7c1e"

private const val UNANSWERED_NOTE = "Could not tell whether that landed: no answer came. This is what stands now."

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AccountsDroidAddTest {
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
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(paneProto()).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto())
        server.respond(ROUTES_GET_PATH, RoutesGetResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    private fun paneProto(): PaneProto = PaneProto.newBuilder()
        .setId("%1")
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd("/home/user/alpha")
        .setCommand("claude")
        .setClaudeSession("synthetic-claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel("alpha-build")
        .setChanged(0)
        .setSeen(0)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    /** Three invented accounts, then [added] when a test has the server store one. */
    private fun accountsProto(added: Account? = null): AccountsListResponse = AccountsListResponse.newBuilder()
        .addAllAccounts(
            listOfNotNull(
                account("example-account-1", "max-1", "Claude Max", "3f9a1c0d7e55"),
                account("example-account-2", "pro-1", "Claude Pro", "e81f05aa9c10"),
                account("example-account-3", "max-2", "Claude Max", "b0217e6d4f32"),
                added,
            ),
        )
        .build()

    private fun account(id: String, name: String, plan: String, fingerprint: String): Account = Account.newBuilder()
        .setId(id)
        .setName(name)
        .setProvider("example-provider")
        .setPlan(plan)
        .setFingerprint(fingerprint)
        .build()

    /** The invented account a stored Add of [NEW_NAME] on Claude Pro makes. */
    private fun newAccount(): Account = account("example-account-4", NEW_NAME, "Claude Pro", NEW_FINGERPRINT)

    private fun addedAccountResponse(): AccountsAddResponse =
        AccountsAddResponse.newBuilder().setAccount(newAccount()).build()

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

    /** Taps the title row's +, and waits until the add sheet is up. */
    private fun openAddSheet() {
        composeRule.onNodeWithTag(ADD_TAG).performClick()
        composeRule.awaitExactlyOne(hasTestTag(SHEET_TAG))
    }

    /**
     * Taps [tag] through its click action. A touch injected into a sheet's or a menu's window does not reach its
     * content under Robolectric, so a tap inside one is sent as the accessibility click that the same tap runs.
     */
    private fun tapInSheet(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Types [text] into the field [tag]. */
    private fun typeIn(tag: String, text: String) {
        composeRule.onNodeWithTag(tag).performTextInput(text)
    }

    /** Chooses [option] from the plan select's menu. */
    private fun choosePlan(option: String) {
        tapInSheet(PLAN_TAG)
        composeRule.awaitExactlyOne(hasTestTag("add-plan:$option"))
        tapInSheet("add-plan:$option")
        composeRule.awaitNone(hasTestTag("add-plan-menu"))
    }

    /** The token the one Add request the server received carried. */
    private fun sentAddRequest(): AccountsAddRequest =
        AccountsAddRequest.parseFrom(server.requests.single { it.rawPath == ACCOUNTS_ADD_PATH }.body)

    /** Waits until [MARKER_TOKEN] is on screen, so the negative checks after it check something that was there. */
    private fun awaitMarkerOnScreen() {
        composeRule.awaitUntil("the marker token typed into the sheet") {
            composeRule.onAllNodes(hasText(MARKER_TOKEN, substring = true), useUnmergedTree = true)
                .fetchSemanticsNodes()
                .isNotEmpty()
        }
    }

    /** Waits until the token field shows its placeholder, which only an empty field shows. */
    private fun awaitTokenFieldEmpty() {
        composeRule.awaitUntil("the token field's placeholder") {
            composeRule.onAllNodes(hasText(TOKEN_PLACEHOLDER), useUnmergedTree = true).fetchSemanticsNodes().size == 1
        }
    }

    /** Fails if [MARKER_TOKEN] is the value of any field or the text of any node, in any window on screen. */
    private fun assertMarkerNowhere() {
        val holders = composeRule.onAllNodes(hasText(MARKER_TOKEN, substring = true), useUnmergedTree = true)
            .fetchSemanticsNodes()
        assertTrue("the marker token is on screen in ${holders.size} node(s)", holders.isEmpty())
    }

    @Test
    fun plusOpensTheAddSheetWithNamePlanSelectAndMaskedToken() {
        launch()
        openAccounts()

        openAddSheet()

        composeRule.awaitExactlyOne(hasTestTag(NAME_TAG))
        composeRule.onNodeWithTag(PLAN_TAG).assertTextEquals("Claude Max")
        composeRule.onNodeWithTag(TOKEN_TAG).assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Password))
        composeRule.awaitExactlyOne(hasText(TOKEN_PLACEHOLDER))
        composeRule.awaitExactlyOne(
            hasText("Run claude setup-token on a machine with a browser and paste what it prints."),
        )
        composeRule.onNodeWithTag(CANCEL_TAG).assertTextEquals("Cancel")
        composeRule.onNodeWithTag(SUBMIT_TAG).assertTextEquals("Add")

        tapInSheet(PLAN_TAG)
        composeRule.awaitExactlyOne(hasTestTag("add-plan:Claude Max"))
        composeRule.awaitExactlyOne(hasTestTag("add-plan:Claude Pro"))
    }

    @Test
    fun anAddedAccountAppearsInTheListWithThePlanAndTokenItWasSentWith() {
        server.respond(ACCOUNTS_ADD_PATH, addedAccountResponse())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto(added = newAccount()))
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        choosePlan("Claude Pro")
        typeIn(TOKEN_TAG, TOKEN_STORED)
        tapInSheet(SUBMIT_TAG)

        composeRule.awaitNone(hasTestTag(SHEET_TAG))
        composeRule.awaitExactlyOne(hasText(NEW_NAME))
        composeRule.awaitExactlyOne(hasText("Claude Pro · #77ab09"))
        val sent = sentAddRequest()
        assertEquals(NEW_NAME, sent.name)
        assertEquals("anthropic", sent.provider)
        assertEquals("Claude Pro", sent.plan)
        assertEquals(TOKEN_STORED, sent.token)
    }

    @Test
    fun aRefusalIsShownInTheSheetAndTheSheetStaysOpenWithItsContents() {
        server.fail(ACCOUNTS_ADD_PATH, Code.INVALID_ARGUMENT, "that name is already in use")
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, TOKEN_REFUSED)
        tapInSheet(SUBMIT_TAG)

        composeRule.awaitExactlyOne(hasText("Not added: that name is already in use Nothing changed."))
        composeRule.awaitExactlyOne(hasTestTag(SHEET_TAG))
        composeRule.onNodeWithTag(NAME_TAG).assertTextEquals(NEW_NAME)
        assertEquals(1, server.requests.count { it.rawPath == ACCOUNTS_LIST_PATH })
    }

    @Test
    fun aDroppedAddClosesTheSheetAndSaysItCouldNotTell() {
        server.drop(ACCOUNTS_ADD_PATH)
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, TOKEN_DROPPED)
        tapInSheet(SUBMIT_TAG)

        composeRule.awaitNone(hasTestTag(SHEET_TAG))
        composeRule.awaitExactlyOne(hasText(UNANSWERED_NOTE))
        composeRule.awaitUntil("the page read again after the Add") {
            server.requests.count { it.rawPath == ACCOUNTS_LIST_PATH } == 2
        }
    }

    @Test
    fun aStoredAddLeavesTheTokenOutOfTheSheetAndTheTree() {
        server.respond(ACCOUNTS_ADD_PATH, addedAccountResponse())
        server.respond(ACCOUNTS_LIST_PATH, accountsProto(added = newAccount()))
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, MARKER_TOKEN)
        awaitMarkerOnScreen()
        tapInSheet(SUBMIT_TAG)

        composeRule.awaitNone(hasTestTag(SHEET_TAG))
        composeRule.awaitExactlyOne(hasText(NEW_NAME))
        assertMarkerNowhere()
        assertEquals(MARKER_TOKEN, sentAddRequest().token)

        openAddSheet()
        awaitTokenFieldEmpty()
        assertMarkerNowhere()
    }

    @Test
    fun cancelLeavesTheTokenOutOfTheSheetAndTheTree() {
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, MARKER_TOKEN)
        awaitMarkerOnScreen()
        tapInSheet(CANCEL_TAG)

        composeRule.awaitNone(hasTestTag(SHEET_TAG))
        assertMarkerNowhere()
        assertTrue("Cancel sends no Add", server.requests.none { it.rawPath == ACCOUNTS_ADD_PATH })

        openAddSheet()
        awaitTokenFieldEmpty()
        assertMarkerNowhere()
    }

    @Test
    fun aSwipeDismissalLeavesTheTokenOutOfTheSheetAndTheTree() {
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, MARKER_TOKEN)
        awaitMarkerOnScreen()
        composeRule.onNodeWithTag(SHEET_TAG).performTouchInput { swipeDown() }

        composeRule.awaitNone(hasTestTag(SHEET_TAG))
        assertMarkerNowhere()
        assertTrue("a swipe sends no Add", server.requests.none { it.rawPath == ACCOUNTS_ADD_PATH })

        openAddSheet()
        awaitTokenFieldEmpty()
        assertMarkerNowhere()
    }

    @Test
    fun leavingAccountsAndReturningThroughTheTopRowLeavesTheTokenOut() {
        launch()
        openAccounts()
        openAddSheet()

        typeIn(NAME_TAG, NEW_NAME)
        typeIn(TOKEN_TAG, MARKER_TOKEN)
        awaitMarkerOnScreen()
        tapInSheet(CANCEL_TAG)
        composeRule.awaitNone(hasTestTag(SHEET_TAG))

        composeRule.onNodeWithTag("title-back").performClick()
        composeRule.awaitExactlyOne(hasTestTag(OPEN_ACCOUNTS_TAG))
        composeRule.onNodeWithTag(OPEN_ACCOUNTS_TAG).performClick()
        composeRule.awaitExactlyOne(hasText("max-1"))
        assertMarkerNowhere()

        openAddSheet()
        awaitTokenFieldEmpty()
        assertMarkerNowhere()
    }
}
