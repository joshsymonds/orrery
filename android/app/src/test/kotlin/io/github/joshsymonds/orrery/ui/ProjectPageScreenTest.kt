package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ProjectPageScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val HOST = "example-host"
        const val REMOTE = "example-remote"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val EPIC_ID = "example-repo/example-epic"
        const val EPIC_REPOSITORY = "example-repo"
        const val EPIC_SLUG = "example-epic"
        const val CONTRACT_ID = "example-repo/example-contract"
        const val CONTRACT_TITLE = "Synthetic contract title"
        const val ALPHA_SESSION = "00000000000000000000000000000001"
        const val ENDED_SESSION = "00000000000000000000000000000004"
        const val ALPHA_KEY = "|$ALPHA"
        const val ALPHA_NAME = "alpha"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var openedPane: Pane? = null
    private var openedEpic: String? = null
    private var openedContract: String? = null
    private var newHere: String? = null
    private var backed = false

    /** Whether the page is in the composition: a test takes it out and puts it back to leave the page and return. */
    private var pageShown by mutableStateOf(true)

    private fun convoTag(key: String): String = "project-convo:$key"

    private fun paneKey(paneId: String, host: String = HOST): String = "pane:$host:$paneId"

    private fun pane(
        id: String,
        cwd: String,
        state: String,
        changed: Long,
        seen: Long,
        newestReply: Long,
        orreryId: String = "",
        question: String = "",
        host: String = HOST,
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(host)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState(state)
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(changed)
        .setSeen(seen)
        .setQuestion(question)
        .setQuestionHash("")
        .setNewestReply(newestReply)
        .setOrreryId(orreryId)
        .build()

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun headless(id: String, dir: String, state: String, changed: Long): HeadlessSession =
        HeadlessSession.newBuilder()
            .setId(id)
            .setDir(dir)
            .setLabel("synthetic-$state")
            .setState(state)
            .setChanged(changed)
            .setSeen(0)
            .setSummary("Synthetic $state session.")
            .setNewestReply(changed)
            .build()

    private fun liveEpic(steering: String): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setProblem("")
        .setSteering(steering)
        .build()

    private fun contract(id: String, conversation: String, repository: String): Contract =
        Contract.newBuilder()
            .setId(id)
            .setRepository(repository)
            .setTitle(CONTRACT_TITLE)
            .setCare("serious")
            .setState("proposed")
            .setVersion(1L)
            .setOpenDecisions(0L)
            .setConversation(conversation)
            .setCreated(NOW - 600)
            .setChanged(NOW - 600)
            .build()

    /** The host the panes come from is the serving host, with no epics listed, unless a test says otherwise. */
    private fun serveDefaults(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
    }

    private fun setPage(server: FakeOrreryServer, projectKey: String, state: PaneListState = PaneListState()) {
        // Made once, outside the composition, so a recomposition does not hand the page a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            if (pageShown) {
                ProjectPageScreen(
                    client = client,
                    projectKey = projectKey,
                    state = state,
                    onOpenPane = { openedPane = it },
                    onOpenEpic = { openedEpic = it },
                    onNewHere = { newHere = it },
                    onBack = { backed = true },
                    onOpenContract = { id, _ -> openedContract = id },
                    now = { NOW },
                )
            }
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    /**
     * Alpha holds %1, waiting on a question and last changed a minute ago, and %2, working, which steers a live epic with
     * no question. Beta is not in the tree.
     */
    private fun serveAlphaWithAnEpic(server: FakeOrreryServer) {
        serveDefaults(server)
        server.respond(
            PANES_LIST,
            panesOf(
                pane("%1", ALPHA, "waiting", NOW - 60, NOW - 120, NOW - 60, question = "Ship the build?"),
                pane("%2", ALPHA, "working", NOW - 90, NOW - 90, NOW - 90, ALPHA_SESSION),
            ),
        )
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(ALPHA_SESSION)).build())
        server.respond(EPICS_GET, EpicsGetResponse.newBuilder().setEpic(liveEpic(ALPHA_SESSION)).build())
    }

    @Test
    fun theTitleIsTheProjectsNameAndBackCallsOnBack() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            composeRule.onNodeWithText(ALPHA_NAME).assertExists()
            assertFalse(backed)

            composeRule.onNodeWithTag("title-back").performClick()

            assertTrue(backed)
        }
    }

    @Test
    fun thePathShowsWithTheHostWhenItIsAnotherHost() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "idle", NOW - 60, NOW - 60, NOW - 60, host = REMOTE)))
            setPage(server, "$REMOTE|$ALPHA")
            awaitNode(hasTestTag(convoTag(paneKey("%1", REMOTE))))

            composeRule.onNodeWithText("~/alpha · on $REMOTE").assertExists()
        }
    }

    @Test
    fun theCountsSayConversationsEpicsWhatWaitsAndTheLastChange() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            composeRule.onNodeWithText("2 conversations").assertExists()
            composeRule.onNodeWithText("1 epic").assertExists()
            composeRule.onNodeWithText("1 thing waits on you").assertExists()
            composeRule.onNodeWithText("last change 1m ago").assertExists()
        }
    }

    @Test
    fun aConversationShowsItsGlyphTitleSecondLineAndAgeAndATapOpensItsPane() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, ALPHA_KEY)
            val row = convoTag(paneKey("%1"))
            awaitNode(hasTestTag(row))

            assertEquals(1, countNodes(hasTestTag(row) and hasAnyDescendant(hasContentDescription("waiting on you"))))
            assertEquals(1, countNodes(hasTestTag(row) and hasAnyDescendant(hasText("label-%1"))))
            assertEquals(1, countNodes(hasTestTag(row) and hasAnyDescendant(hasText("? Ship the build?"))))
            assertEquals(1, countNodes(hasTestTag(row) and hasAnyDescendant(hasText("1m"))))

            composeRule.onNodeWithTag(row).performClick()

            assertEquals("%1", openedPane?.id)
        }
    }

    @Test
    fun anEpicCardUnderItsConversationOpensTheEpic() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag("card:epic:$EPIC_ID"))

            composeRule.onNodeWithTag("card:epic:$EPIC_ID").performClick()

            assertEquals(EPIC_ID, openedEpic)
        }
    }

    @Test
    fun aContractCardShowsItsTitleAndOpensNothingWhenNoEpicHoldsIt() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder().addContracts(contract(CONTRACT_ID, ALPHA_SESSION, ALPHA)).build(),
            )
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag("card:contract:$CONTRACT_ID"))

            composeRule.onNodeWithText(CONTRACT_TITLE).assertExists()
            composeRule.onNodeWithTag("card:contract:$CONTRACT_ID").performClick()

            assertNull(openedEpic)
            assertNull(openedPane)
        }
    }

    @Test
    fun aStandInRowShowsItsWordsAndItsCardAndCountsAsAConversation() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf())
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "unlisted-session", ALPHA))
                    .build(),
            )
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag("card:contract:$CONTRACT_ID"))

            composeRule.onNodeWithText("its conversation is not listed").assertExists()
            composeRule.onNodeWithTag("project-age:standin:unlisted:$ALPHA_KEY").assertTextEquals("10m")
            composeRule.onNodeWithText("1 conversation").assertExists()
        }
    }

    @Test
    fun aContractCardOpensTheContractAsTheTreeDoes() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf())
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "unlisted-session", ALPHA))
                    .build(),
            )
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag("card:contract:$CONTRACT_ID"))

            composeRule.onNodeWithTag("card-open:contract:$CONTRACT_ID", useUnmergedTree = true).performClick()

            assertEquals(CONTRACT_ID, openedContract)
        }
    }

    @Test
    fun theEndedLineUnfoldsItsEndedConversationsAndItsSecondTapFoldsThemAgain() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder().addSessions(headless(ENDED_SESSION, ALPHA, "stopped", NOW - 900)).build(),
            )
            val state = PaneListState()
            setPage(server, ALPHA_KEY, state)
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            composeRule.onNodeWithText("1 ended conversation").assertExists()
            assertEquals(0, countNodes(hasTestTag(convoTag("session:$ENDED_SESSION"))))

            composeRule.onNodeWithTag("project-ended").performClick()

            assertEquals(1, countNodes(hasTestTag(convoTag("session:$ENDED_SESSION"))))

            composeRule.onNodeWithTag("project-ended").performClick()

            assertEquals(0, countNodes(hasTestTag(convoTag("session:$ENDED_SESSION"))))
        }
    }

    @Test
    fun newSessionHereCallsOnNewHereWithTheProjectsDirectory() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, ALPHA_KEY)
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            composeRule.onNodeWithTag("project-new-here").performClick()

            assertEquals(ALPHA, newHere)
        }
    }

    @Test
    fun theNewSessionHereBarIsAbsentForAProjectWithNoDirectory() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", "", "working", NOW - 30, NOW - 10, NOW - 30)))
            setPage(server, "nodir")
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            composeRule.onNodeWithText("No directory").assertExists()
            assertEquals(0, countNodes(hasTestTag("project-new-here")))
        }
    }

    @Test
    fun aProjectKeyTheTreeNoLongerHoldsSaysSo() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            setPage(server, "|/home/user/gone")
            awaitNode(hasText("Nothing in the inbox is in this project."))

            composeRule.onNodeWithTag("title-back").assertExists()
        }
    }

    @Test
    fun aFailedReadWithNothingHeldSaysTheServerDidNotAnswer() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")
            setPage(server, ALPHA_KEY)
            awaitNode(hasText("The server did not answer."))
        }
    }

    @Test
    fun theTreeHeldByTheInboxSurvivesLeavingAndComingBackWhileTheReadFails() {
        FakeOrreryServer().use { server ->
            serveAlphaWithAnEpic(server)
            val state = PaneListState()
            setPage(server, ALPHA_KEY, state)
            awaitNode(hasTestTag(convoTag(paneKey("%1"))))

            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")
            pageShown = false
            composeRule.waitForIdle()
            pageShown = true

            awaitNode(hasTestTag(convoTag(paneKey("%1"))))
        }
    }
}
