package io.github.joshsymonds.orrery.ui

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
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryClient
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
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
        const val ASK_PATH = "/orrery.v1.Picker/Ask"
        const val RUN_PATH = "/orrery.v1.Picker/Run"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val FULL_FOLDER = "/home/user/projects/synthetic-workspace/nested/deeper/folder-that-is-long"
        const val OTHER_FOLDER = "/home/user/projects/synthetic-workspace/another-folder"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        const val PLACEHOLDER = "A directory, a session, or what you want to start"
        const val HINT = "Name a directory to start a session there, or say a few words about a session to find it."
        const val CHIP = "starts with your words"
        const val ASK_NINETY = "The search did not finish: the server did not answer in 90 seconds."
        const val ASK_UNAVAILABLE = "The search did not finish: the server could not answer."
        const val ASK_NOT_SET_UP = "New is not set up on this server."
        const val ASK_FAILED = "The search did not finish."
        const val RUN_STALE = "That result has gone stale. Ask again."
        const val RUN_EXPIRED = "It did not open: the result expired, or tmux is not running on the server."
        const val RUN_UNAVAILABLE = "It did not open: the server could not run it."
        const val RUN_FAILED = "It did not open."
        const val START_FAILED = "The session did not start."
        const val NOTHING_FOUND = "Nothing found for “fix the test”. Try a directory name."
        val ID_FORM = Regex("^[0-9a-f]{32}$")
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val opened = AtomicReference<String?>(null)
    private val started = AtomicReference<String?>(null)
    private val closed = AtomicInteger(0)

    private fun rowsResponse(): AskResponse = AskResponse.newBuilder()
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-jump")
                .setKind("jump")
                .setDir("/work/alpha")
                .setPane("%4")
                .setLabel("alpha window")
                .setNote("")
                .setPrompt(false),
        )
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-resume")
                .setKind("resume")
                .setDir("/work/beta")
                .setPane("")
                .setLabel("")
                .setNote("fix the flaky test")
                .setPrompt(true),
        )
        .build()

    private fun kindsResponse(): AskResponse = AskResponse.newBuilder()
        .addRows(kindRow("jump", "/work/a"))
        .addRows(kindRow("resume", "/work/b"))
        .addRows(kindRow("continue", "/work/c"))
        .addRows(kindRow("pane", "/work/d"))
        .addRows(kindRow("fresh", "/work/e"))
        .build()

    private fun kindRow(kind: String, dir: String): PickerRow = PickerRow.newBuilder()
        .setToken("tok-$kind")
        .setKind(kind)
        .setDir(dir)
        .setPane("")
        .setLabel("")
        .setNote("")
        .setPrompt(false)
        .build()

    private fun startRowsResponse(): AskResponse = AskResponse.newBuilder()
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-pane")
                .setKind("pane")
                .setDir(FULL_FOLDER)
                .setPane("%7")
                .setLabel("")
                .setNote("")
                .setPrompt(false),
        )
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-fresh")
                .setKind("fresh")
                .setDir(FULL_FOLDER)
                .setPane("")
                .setLabel("")
                .setNote("")
                .setPrompt(false),
        )
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-pane-prompt")
                .setKind("pane")
                .setDir(FULL_FOLDER)
                .setPane("%8")
                .setLabel("")
                .setNote("")
                .setPrompt(true),
        )
        .build()

    private fun freshRow(token: String, dir: String): PickerRow = PickerRow.newBuilder()
        .setToken(token)
        .setKind("fresh")
        .setDir(dir)
        .setPane("")
        .setLabel("")
        .setNote("")
        .setPrompt(false)
        .build()

    private fun setPanel(server: FakeOrreryServer) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onOpened = { opened.set(it) },
                onStarted = { started.set(it) },
                onClose = { closed.incrementAndGet() },
            )
        }
    }

    private fun fieldText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text

    private fun openStartForm(server: FakeOrreryServer, row: Int) {
        server.respond(ASK_PATH, startRowsResponse())
        setPanel(server)
        submit("fix the test")
        composeRule.awaitUntil { rowExists(row) }
        tap("picker-row:$row")
        composeRule.awaitUntil { composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty() }
    }

    /** Holds the next ask open until the test counts [release] down, then answers it with [response]. */
    private fun holdAsk(server: FakeOrreryServer, release: CountDownLatch, response: AskResponse) {
        server.route(ASK_PATH) {
            release.await()
            Reply.Message(response, 0L)
        }
    }

    private fun submit(text: String) {
        composeRule.onNodeWithTag("picker-text").performTextInput(text)
        composeRule.onNodeWithTag("picker-text").performImeAction()
    }

    /**
     * Taps [tag] through its click action. A touch injected into the sheet's window does not reach the sheet's content
     * under Robolectric, so a tap inside the sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Goes back from the start form to the rows it replaced. */
    private fun otherResults() {
        tap("start-back")
        composeRule.awaitUntil { countTagged("start-form") == 0 }
    }

    private fun dismissSheet() {
        composeRule.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Dismiss))
            .performSemanticsAction(SemanticsActions.Dismiss)
    }

    private fun countTagged(tag: String): Int = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().size

    private fun rowExists(index: Int): Boolean =
        composeRule.onAllNodesWithTag("picker-row:$index").fetchSemanticsNodes().isNotEmpty()

    private fun rowText(index: Int): String =
        composeRule.onNodeWithTag("picker-row:$index").fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    private fun startIds(server: FakeOrreryServer): List<String> =
        postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body).id }

    private fun errorShown(): Boolean =
        composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()

    /** Asks with [code] failing and checks the sheet shows [sentence] and never the server's text. */
    private fun askFailingWith(code: Code, sentence: String) {
        FakeOrreryServer().use { server ->
            server.fail(ASK_PATH, code, "server text")
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("picker-error").assertTextEquals(sentence)
        }
    }

    /** Runs a row with [code] failing and checks the sheet shows [sentence], the rows stay, and nothing opens. */
    private fun runFailingWith(code: Code, sentence: String) {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, rowsResponse())
            server.fail(RUN_PATH, code, "server text")
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { rowExists(0) }
            tap("picker-row:0")
            composeRule.awaitUntil { errorShown() }

            composeRule.onNodeWithTag("picker-error").assertTextEquals(sentence)
            assertTrue(rowExists(0))
            assertEquals(null, opened.get())
        }
    }

    @Test
    fun sheetIsTitledNewSessionWithTheFieldPlaceholderAndHintAndNoAskOrCancelButton() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-title").assertTextEquals("New session")
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

            composeRule.onNodeWithTag("picker-title").assertTextEquals("New session")
            composeRule.onNodeWithTag("picker-text").assertExists()
        }
    }

    @Test
    fun submitShowsAskingThenRowsWithDirLabelNoteAndTheChipOnAPromptRow() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            holdAsk(server, release, rowsResponse())
            setPanel(server)

            submit("fix the test")

            composeRule.onNodeWithTag("picker-working").assertTextEquals("asking…")
            release.countDown()
            composeRule.awaitUntil { rowExists(0) }
            assertEquals(0, countTagged("picker-working"))
            assertEquals(0, countTagged("picker-hint"))

            val asks = postsTo(server, ASK_PATH)
            assertEquals(1, asks.size)
            assertEquals(AskRequest.newBuilder().setText("fix the test").build(), AskRequest.parseFrom(asks.single().body))

            assertTrue(rowText(0).contains("/work/alpha"))
            assertTrue(rowText(0).contains("alpha window"))
            assertFalse(rowText(0).contains(CHIP))

            assertTrue(rowText(1).contains("/work/beta"))
            assertTrue(rowText(1).contains("fix the flaky test"))
            assertTrue(rowText(1).contains(CHIP))
        }
    }

    @Test
    fun eachRowIsLedByItsKindsWord() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, kindsResponse())
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { rowExists(4) }

            assertTrue(rowText(0).startsWith("go to "))
            assertTrue(rowText(1).startsWith("resume "))
            assertTrue(rowText(2).startsWith("continue "))
            assertTrue(rowText(3).startsWith("start in "))
            assertTrue(rowText(4).startsWith("start in "))
        }
    }

    @Test
    fun placeholderAndHintShowUntilTheFirstAskAndAskingShowsWhileItRuns() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            holdAsk(server, release, rowsResponse())
            setPanel(server)

            composeRule.onNodeWithTag("picker-hint").assertTextEquals(HINT)
            composeRule.onNodeWithText(PLACEHOLDER, useUnmergedTree = true).assertExists()

            composeRule.onNodeWithTag("picker-text").performTextInput("fix the test")
            composeRule.onNodeWithTag("picker-text").performImeAction()
            composeRule.onNodeWithTag("picker-working").assertTextEquals("asking…")
            release.countDown()
            composeRule.awaitUntil { rowExists(0) }

            assertEquals(0, countTagged("picker-hint"))
            assertEquals(0, countTagged("picker-working"))
        }
    }

    @Test
    fun anAskThatFindsNothingSaysSoWithItsWords() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, AskResponse.getDefaultInstance())
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { countTagged("picker-none") == 1 }

            composeRule.onNodeWithTag("picker-none").assertTextEquals(NOTHING_FOUND)
            assertEquals(0, countTagged("picker-hint"))
            assertEquals(0, countTagged("picker-row:0"))
        }
    }

    @Test
    fun anAskThatTimesOutShowsTheNinetySecondSentenceAndNeverTheServersText() {
        askFailingWith(Code.DEADLINE_EXCEEDED, ASK_NINETY)
    }

    @Test
    fun anAskTheServerCannotAnswerShowsItsSentence() {
        askFailingWith(Code.UNAVAILABLE, ASK_UNAVAILABLE)
    }

    @Test
    fun anAskWhenNewIsNotSetUpShowsItsSentence() {
        askFailingWith(Code.NOT_FOUND, ASK_NOT_SET_UP)
    }

    @Test
    fun anAskWithAnotherCodeShowsTheGenericSentence() {
        askFailingWith(Code.UNKNOWN, ASK_FAILED)
    }

    @Test
    fun aSocketTimeoutNoAnswerToAnAskShowsTheNinetySecondSentence() {
        assertEquals(ASK_NINETY, askSentence(NoAnswerException(SocketTimeoutException("read timed out"))))
    }

    @Test
    fun anotherNoAnswerToAnAskShowsTheGenericSentence() {
        assertEquals(ASK_FAILED, askSentence(NoAnswerException(IOException("connection reset"))))
    }

    @Test
    fun tappingSecondRowRunsItsTokenAndReportsItsPane() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, rowsResponse())
            server.respond(RUN_PATH, RunResponse.newBuilder().setPane("%9").build())
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { rowExists(1) }
            tap("picker-row:1")
            composeRule.awaitUntil { opened.get() != null }

            assertEquals("%9", opened.get())
            val runs = postsTo(server, RUN_PATH)
            assertEquals(1, runs.size)
            assertEquals(RunRequest.newBuilder().setToken("tok-resume").build(), RunRequest.parseFrom(runs.single().body))
        }
    }

    @Test
    fun askFailureKeepsTheTextSoItCanBeResubmitted() {
        FakeOrreryServer().use { server ->
            server.fail(ASK_PATH, Code.UNAVAILABLE, "server text")
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("picker-error").assertTextEquals(ASK_UNAVAILABLE)
            assertFalse(rowExists(0))
            assertEquals("fix the test", fieldText("picker-text"))

            server.respond(ASK_PATH, rowsResponse())
            composeRule.onNodeWithTag("picker-text").performImeAction()
            composeRule.awaitUntil { rowExists(0) }

            assertEquals(2, postsTo(server, ASK_PATH).size)
            assertFalse(errorShown())
        }
    }

    @Test
    fun runFailureShowsItsSentenceAndTheRowsStay() {
        runFailingWith(Code.FAILED_PRECONDITION, RUN_EXPIRED)
    }

    @Test
    fun aRunOfAStaleRowShowsItsSentence() {
        runFailingWith(Code.NOT_FOUND, RUN_STALE)
    }

    @Test
    fun aRunTheServerCannotMakeShowsItsSentence() {
        runFailingWith(Code.UNAVAILABLE, RUN_UNAVAILABLE)
    }

    @Test
    fun aRunWithAnotherCodeShowsTheGenericSentence() {
        runFailingWith(Code.UNKNOWN, RUN_FAILED)
    }

    @Test
    fun blankTextPostsNothing() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-text").performTextInput("   ")
            composeRule.onNodeWithTag("picker-text").performImeAction()

            assertTrue(server.requests.isEmpty())
        }
    }

    @Test
    fun aSearchWhileAnAskRunsPostsNothing() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            holdAsk(server, release, rowsResponse())
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { countTagged("picker-working") == 1 }
            composeRule.onNodeWithTag("picker-text").performImeAction()
            release.countDown()
            composeRule.awaitUntil { rowExists(0) }

            assertEquals(1, postsTo(server, ASK_PATH).size)
        }
    }

    @Test
    fun paneAndFreshRowsPostNoRunAndShowTheirFullFolder() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 0)
            composeRule.onNodeWithTag("start-dir").assertTextEquals(FULL_FOLDER)
            assertEquals("", fieldText("start-message"))

            otherResults()
            tap("picker-row:1")
            composeRule.waitForIdle()

            composeRule.onNodeWithTag("start-dir").assertTextEquals(FULL_FOLDER)
            assertEquals("", fieldText("start-message"))
            assertTrue(postsTo(server, RUN_PATH).isEmpty())
        }
    }

    @Test
    fun aPromptRowsMessageIsTheAskedTextAndANonPromptRowsIsEmpty() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 2)
            assertEquals("fix the test", fieldText("start-message"))

            otherResults()
            tap("picker-row:1")
            composeRule.waitForIdle()

            assertEquals("", fieldText("start-message"))
        }
    }

    @Test
    fun theFormShowsItsDirectoryFirstMessageOtherResultsAndStartInPlaceOfTheRows() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)

            composeRule.onNodeWithText("Directory").assertExists()
            composeRule.onNodeWithTag("start-dir").assertTextEquals(FULL_FOLDER)
            composeRule.onNodeWithText("First message").assertExists()
            composeRule.onNodeWithText("Other results").assertExists()
            composeRule.onNodeWithText("Start the session").assertExists()
            assertEquals(0, countTagged("picker-row:0"))
        }
    }

    @Test
    fun otherResultsReturnsToTheRowsAsTheyWere() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)

            otherResults()

            composeRule.onNodeWithTag("picker-row:0").assertExists()
            assertTrue(rowText(2).contains(FULL_FOLDER))
            assertEquals(0, countTagged("start-form"))
        }
    }

    @Test
    fun startingShowsWhileAStartRuns() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            val release = CountDownLatch(1)
            server.route(START_PATH) {
                release.await()
                Reply.Message(StartResponse.newBuilder().setId(ANSWERED_ID).build(), 0L)
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
            composeRule.onNodeWithTag("start-starting").assertTextEquals("starting…")
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()
            composeRule.onNodeWithTag("start-back").assertIsNotEnabled()

            release.countDown()
            composeRule.awaitUntil { started.get() != null }
            assertEquals(0, countTagged("start-starting"))
        }
    }

    @Test
    fun blankOrWhitespaceMessageDisablesStart() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            composeRule.onNodeWithTag("start-message").performTextInput("   ")
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            composeRule.onNodeWithTag("start-submit").assertIsEnabled()
        }
    }

    @Test
    fun startPostsOneStartWithFolderMessageAndIdThenReportsTheAnsweredId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            val starts = postsTo(server, START_PATH)
            assertEquals(1, starts.size)
            val request = StartRequest.parseFrom(starts.single().body)
            assertEquals(FULL_FOLDER, request.dir)
            assertEquals("start it", request.prompt)
            assertTrue(ID_FORM.matches(request.id))
            assertEquals(ANSWERED_ID, started.get())
        }
    }

    @Test
    fun aRefusedStartShowsItsSentenceAndKeepsTheMessage() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "server text")
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
            composeRule.awaitUntil { errorShown() }

            composeRule.onNodeWithTag("picker-error").assertTextEquals(START_FAILED)
            assertEquals("start it", fieldText("start-message"))
            assertEquals(null, started.get())
        }
    }

    @Test
    fun aRefusedStartRetriedWithTheSameMessagePostsTheSameId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
            composeRule.awaitUntil { errorShown() }
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val ids = startIds(server)
            assertTrue(ID_FORM.matches(ids[0]))
            assertEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aDroppedStartRetriedWithTheSameMessagePostsTheSameId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            val answers = AtomicInteger(0)
            server.route(START_PATH) {
                if (answers.incrementAndGet() == 1) {
                    Reply.Drop
                } else {
                    Reply.Message(StartResponse.newBuilder().setId(ANSWERED_ID).build(), 0L)
                }
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
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
    fun aStartAfterAnEditedMessageKeepsTheFormsId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            tap("start-submit")
            composeRule.awaitUntil { errorShown() }
            composeRule.onNodeWithTag("start-message").performTextInput(" now")
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals("start it now", starts[1].prompt)
            assertTrue(ID_FORM.matches(starts[1].id))
            assertEquals(starts[0].id, starts[1].id)
        }
    }

    @Test
    fun aNewFormForTheSameFolderAndMessageGetsANewId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            tap("picker-row:1")
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val ids = startIds(server)
            assertTrue(ID_FORM.matches(ids[0]))
            assertTrue(ID_FORM.matches(ids[1]))
            assertNotEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aStartForAnotherFolderAfterARefusedStartPostsANewId() {
        FakeOrreryServer().use { server ->
            server.respond(
                ASK_PATH,
                AskResponse.newBuilder()
                    .addRows(freshRow("tok-first", FULL_FOLDER))
                    .addRows(freshRow("tok-second", OTHER_FOLDER))
                    .build(),
            )
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            setPanel(server)

            submit("fix the test")
            composeRule.awaitUntil { rowExists(1) }
            tap("picker-row:0")
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            tap("start-submit")
            composeRule.awaitUntil { errorShown() }

            otherResults()
            tap("picker-row:1")
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("start-dir").assertTextEquals(OTHER_FOLDER)
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            tap("start-submit")
            composeRule.awaitUntil { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals(FULL_FOLDER, starts[0].dir)
            assertEquals(OTHER_FOLDER, starts[1].dir)
            assertTrue(ID_FORM.matches(starts[1].id))
            assertNotEquals(starts[0].id, starts[1].id)
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
