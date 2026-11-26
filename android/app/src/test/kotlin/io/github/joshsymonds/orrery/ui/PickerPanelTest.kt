package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryClient
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ProposeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StartProposal
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException
import java.net.SocketTimeoutException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PickerPanelTest {
    private companion object {
        const val PROPOSE_PATH = "/orrery.v1.Picker/Propose"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val FULL_FOLDER = "/home/user/projects/synthetic-workspace/nested/deeper/folder-that-is-long"
        const val OTHER_FOLDER = "/home/user/projects/synthetic-workspace/another-folder"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        const val PLACEHOLDER = "What should it do? Say it in your words"
        const val HINT = "Press ⏎ and Orrery proposes where to run it."
        const val FINDING = "Finding where to run it…"
        const val NONE_FIT = "No reachable host has a folder that fits this. Choose a host and type a folder."
        const val ASK_NINETY = "The search did not finish: the server did not answer in 90 seconds."
        const val ASK_UNAVAILABLE = "The search did not finish: the server could not answer."
        const val ASK_NOT_SET_UP = "New is not set up on this server."
        const val ASK_FAILED = "The search did not finish."
        const val START_FAILED = "The session did not start."
        const val PROMPT = "fix the test"
        val ID_FORM = Regex("^[0-9a-f]{32}$")
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val started = AtomicReference<String?>(null)
    private val closed = AtomicInteger(0)

    /** Two places, the first on halyard and the second on sextant: the top one is the first. */
    private fun proposalsResponse(): ProposeResponse = ProposeResponse.newBuilder()
        .addProposals(StartProposal.newBuilder().setHost("halyard").setDir(FULL_FOLDER))
        .addProposals(StartProposal.newBuilder().setHost("sextant").setDir(OTHER_FOLDER))
        .build()

    private fun setPanel(server: FakeOrreryServer) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onStarted = { started.set(it) },
                onClose = { closed.incrementAndGet() },
            )
        }
    }

    private fun fieldText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text

    /** Holds the next propose open until the test counts [release] down, then answers it with [response]. */
    private fun holdPropose(server: FakeOrreryServer, release: CountDownLatch, response: ProposeResponse) {
        server.route(PROPOSE_PATH) {
            release.await()
            Reply.Message(response, 0L)
        }
    }

    private fun submit(text: String) {
        composeRule.onNodeWithTag("picker-text").performTextInput(text)
        composeRule.onNodeWithTag("picker-text").performImeAction()
    }

    /** Submits [PROMPT] and waits until the proposals are drawn, the top one preselected. */
    private fun proposeAndWait(server: FakeOrreryServer) {
        server.respond(PROPOSE_PATH, proposalsResponse())
        setPanel(server)
        submit(PROMPT)
        composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
    }

    /** Proposes, then starts the top proposal with [PROMPT]: the Start button is on once the place is set. */
    private fun startTopProposal(server: FakeOrreryServer) {
        proposeAndWait(server)
        tap("start-submit")
    }

    /**
     * Taps [tag] through its click action. A touch injected into the sheet's window does not reach the sheet's content
     * under Robolectric, so a tap inside the sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun dismissSheet() {
        composeRule.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Dismiss))
            .performSemanticsAction(SemanticsActions.Dismiss)
    }

    private fun countTagged(tag: String): Int = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().size

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    private fun startIds(server: FakeOrreryServer): List<String> =
        postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body).id }

    private fun errorShown(): Boolean =
        composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()

    /** Proposes with [code] failing and checks the sheet shows [sentence] and never the server's text. */
    private fun proposeFailingWith(code: Code, sentence: String) {
        FakeOrreryServer().use { server ->
            server.fail(PROPOSE_PATH, code, "server text")
            setPanel(server)

            submit(PROMPT)
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("picker-error").assertTextEquals(sentence)
        }
    }

    @Test
    fun sheetIsTitledStartASessionWithThePromptPlaceholderAndHintAndNoAskOrCancelButton() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-title").assertTextEquals("Start a session")
            composeRule.onNodeWithText(PLACEHOLDER, useUnmergedTree = true).assertExists()
            composeRule.onNodeWithTag("picker-hint").assertTextEquals(HINT)
            assertEquals(0, countTagged("picker-ask"))
            assertEquals(0, countTagged("picker-cancel"))
        }
    }

    @Test
    fun newInPaneListScreensHeadOpensTheSheet() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
            composeRule.setContent {
                PaneListScreen(
                    client = OrreryClient(server.baseUrl),
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                )
            }

            composeRule.onNodeWithTag("picker-new").performClick()

            composeRule.onNodeWithTag("picker-title").assertTextEquals("Start a session")
            composeRule.onNodeWithTag("picker-text").assertExists()
        }
    }

    @Test
    fun submitShowsFindingUntilProposalsArriveAndClearsTheHint() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            holdPropose(server, release, proposalsResponse())
            setPanel(server)

            submit(PROMPT)

            composeRule.onNodeWithTag("picker-working").assertTextEquals(FINDING)
            release.countDown()
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
            assertEquals(0, countTagged("picker-working"))
            assertEquals(0, countTagged("picker-hint"))
            assertEquals(1, postsTo(server, PROPOSE_PATH).size)
        }
    }

    @Test
    fun blankTextPostsNothing() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-text").performTextInput("   ")
            composeRule.onNodeWithTag("picker-text").performImeAction()

            assertTrue(postsTo(server, PROPOSE_PATH).isEmpty())
        }
    }

    @Test
    fun aSearchWhileAProposalRunsPostsNothing() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            holdPropose(server, release, proposalsResponse())
            setPanel(server)

            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-working") == 1 }
            composeRule.onNodeWithTag("picker-text").performImeAction()
            release.countDown()
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }

            assertEquals(1, postsTo(server, PROPOSE_PATH).size)
        }
    }

    @Test
    fun aProposeThatFindsNothingSaysSoWithItsWords() {
        FakeOrreryServer().use { server ->
            server.respond(PROPOSE_PATH, ProposeResponse.getDefaultInstance())
            setPanel(server)

            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-none") == 1 }

            composeRule.onNodeWithTag("picker-none").assertTextEquals(NONE_FIT)
            assertEquals(0, countTagged("picker-hint"))
            assertEquals(0, countTagged("picker-proposal:0"))
        }
    }

    @Test
    fun aProposeThatTimesOutShowsTheNinetySecondSentenceAndNeverTheServersText() {
        proposeFailingWith(Code.DEADLINE_EXCEEDED, ASK_NINETY)
    }

    @Test
    fun aProposeTheServerCannotAnswerShowsItsSentence() {
        proposeFailingWith(Code.UNAVAILABLE, ASK_UNAVAILABLE)
    }

    @Test
    fun aProposeWhenNewIsNotSetUpShowsItsSentence() {
        proposeFailingWith(Code.NOT_FOUND, ASK_NOT_SET_UP)
    }

    @Test
    fun aProposeWithAnotherCodeShowsTheGenericSentence() {
        proposeFailingWith(Code.UNKNOWN, ASK_FAILED)
    }

    @Test
    fun aSocketTimeoutNoAnswerToAProposeShowsTheNinetySecondSentence() {
        assertEquals(ASK_NINETY, askSentence(NoAnswerException(SocketTimeoutException("read timed out"))))
    }

    @Test
    fun anotherNoAnswerToAProposeShowsTheGenericSentence() {
        assertEquals(ASK_FAILED, askSentence(NoAnswerException(IOException("connection reset"))))
    }

    @Test
    fun aProposeFailureKeepsThePromptSoItCanBeResubmitted() {
        FakeOrreryServer().use { server ->
            server.fail(PROPOSE_PATH, Code.UNAVAILABLE, "server text")
            setPanel(server)

            submit(PROMPT)
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("picker-error").assertTextEquals(ASK_UNAVAILABLE)
            assertEquals(0, countTagged("picker-proposal:0"))
            assertEquals(PROMPT, fieldText("picker-text"))

            server.respond(PROPOSE_PATH, proposalsResponse())
            composeRule.onNodeWithTag("picker-text").performImeAction()
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }

            assertEquals(2, postsTo(server, PROPOSE_PATH).size)
            assertFalse(errorShown())
        }
    }

    @Test
    fun startingShowsWhileAStartRuns() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            server.route(START_PATH) {
                release.await()
                Reply.Message(StartResponse.newBuilder().setId(ANSWERED_ID).build(), 0L)
            }
            startTopProposal(server)
            composeRule.onNodeWithTag("start-starting").assertTextEquals("starting…")
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            release.countDown()
            composeRule.awaitUntil { started.get() != null }
            assertEquals(0, countTagged("start-starting"))
        }
    }

    @Test
    fun blankOrWhitespaceFolderDisablesStart() {
        FakeOrreryServer().use { server ->
            server.respond(PROPOSE_PATH, ProposeResponse.getDefaultInstance())
            setPanel(server)
            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-none") == 1 }
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            composeRule.onNodeWithTag("start-dir").performTextReplacement("   ")
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            composeRule.onNodeWithTag("start-dir").performTextReplacement(OTHER_FOLDER)
            composeRule.onNodeWithTag("start-submit").assertIsEnabled()
        }
    }

    @Test
    fun startPostsOneStartWithTheProposalsFolderPromptAndIdThenReportsTheAnsweredId() {
        FakeOrreryServer().use { server ->
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            startTopProposal(server)
            composeRule.awaitUntil { started.get() != null }

            val starts = postsTo(server, START_PATH)
            assertEquals(1, starts.size)
            val request = StartRequest.parseFrom(starts.single().body)
            assertEquals(FULL_FOLDER, request.dir)
            assertEquals("halyard", request.host)
            assertEquals(PROMPT, request.prompt)
            assertTrue(ID_FORM.matches(request.id))
            assertEquals(ANSWERED_ID, started.get())
        }
    }

    @Test
    fun aRefusedStartShowsItsSentenceAndKeepsThePromptAndTheFolder() {
        FakeOrreryServer().use { server ->
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "server text")
            startTopProposal(server)
            composeRule.awaitUntil { errorShown() }

            composeRule.onNodeWithTag("picker-error").assertTextEquals(START_FAILED)
            assertEquals(PROMPT, fieldText("picker-text"))
            assertEquals(FULL_FOLDER, fieldText("start-dir"))
            assertEquals(null, started.get())
        }
    }

    @Test
    fun aRefusedStartRetriedPostsTheSameId() {
        FakeOrreryServer().use { server ->
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            startTopProposal(server)
            composeRule.awaitUntil { errorShown() }
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val ids = startIds(server)
            assertTrue(ID_FORM.matches(ids[0]))
            assertEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aDroppedStartRetriedPostsTheSameId() {
        FakeOrreryServer().use { server ->
            val answers = AtomicInteger(0)
            server.route(START_PATH) {
                if (answers.incrementAndGet() == 1) {
                    Reply.Drop
                } else {
                    Reply.Message(StartResponse.newBuilder().setId(ANSWERED_ID).build(), 0L)
                }
            }
            startTopProposal(server)
            composeRule.awaitUntil { errorShown() }
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            val ids = startIds(server)
            assertEquals(2, ids.size)
            assertTrue(ID_FORM.matches(ids[0]))
            assertEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aStartAfterAnEditedPromptKeepsTheFormsId() {
        FakeOrreryServer().use { server ->
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            startTopProposal(server)
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("picker-text").performTextInput(" now")
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals("$PROMPT now", starts[1].prompt)
            assertTrue(ID_FORM.matches(starts[1].id))
            assertEquals(starts[0].id, starts[1].id)
        }
    }

    @Test
    fun aNewFormGetsANewId() {
        FakeOrreryServer().use { server ->
            server.respond(PROPOSE_PATH, proposalsResponse())
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            val open = mutableStateOf(true)
            composeRule.setContent {
                if (open.value) {
                    PickerPanel(
                        client = OrreryClient(server.baseUrl),
                        onStarted = {
                            started.set(it)
                            open.value = false
                        },
                        onClose = {},
                    )
                }
            }
            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            open.value = true
            composeRule.awaitUntil { countTagged("picker-text") == 1 }
            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val ids = startIds(server)
            assertTrue(ID_FORM.matches(ids[0]))
            assertTrue(ID_FORM.matches(ids[1]))
            assertNotEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aStartForAnotherFolderAfterARefusedStartKeepsTheFormsId() {
        FakeOrreryServer().use { server ->
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            startTopProposal(server)
            composeRule.awaitUntil { errorShown() }

            composeRule.onNodeWithTag("start-dir").performTextReplacement(OTHER_FOLDER)
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals(FULL_FOLDER, starts[0].dir)
            assertEquals(OTHER_FOLDER, starts[1].dir)
            assertEquals(starts[0].id, starts[1].id)
        }
    }

    @Test
    fun dismissingTheSheetClosesIt() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            dismissSheet()
            composeRule.awaitUntil { closed.get() == 1 }

            assertEquals(1, closed.get())
        }
    }
}
