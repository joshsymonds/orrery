package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PickerPanelTest {
    private companion object {
        const val TIMEOUT_MS = 5_000L
        const val ASK_DELAY_MS = 800L
        const val ASK_PATH = "/orrery.v1.Picker/Ask"
        const val RUN_PATH = "/orrery.v1.Picker/Run"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val FULL_FOLDER = "/home/user/projects/synthetic-workspace/nested/deeper/folder-that-is-long"
        const val OTHER_FOLDER = "/home/user/projects/synthetic-workspace/another-folder"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        val ID_FORM = Regex("^[0-9a-f]{32}$")
    }

    @get:Rule
    val composeRule = createComposeRule()

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
        composeRule.waitUntil(TIMEOUT_MS) { rowExists(row) }
        composeRule.onNodeWithTag("picker-row:$row").performClick()
        composeRule.waitUntil(TIMEOUT_MS) { composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty() }
    }

    private fun submit(text: String) {
        composeRule.onNodeWithTag("picker-text").performTextInput(text)
        composeRule.onNodeWithTag("picker-ask").performClick()
    }

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

    @Test
    fun submitShowsWorkingThenRowsWithKindFolderLabelNoteAndPromptFlag() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, rowsResponse(), delayMillis = ASK_DELAY_MS)
            setPanel(server)

            submit("fix the test")

            composeRule.onNodeWithTag("picker-working").assertIsDisplayed()
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }
            assertTrue(composeRule.onAllNodesWithTag("picker-working").fetchSemanticsNodes().isEmpty())

            val asks = postsTo(server, ASK_PATH)
            assertEquals(1, asks.size)
            assertEquals(AskRequest.newBuilder().setText("fix the test").build(), AskRequest.parseFrom(asks.single().body))

            assertTrue(rowText(0).contains("jump"))
            assertTrue(rowText(0).contains("/work/alpha"))
            assertTrue(rowText(0).contains("alpha window"))
            assertFalse(rowText(0).contains("first prompt"))

            assertTrue(rowText(1).contains("resume"))
            assertTrue(rowText(1).contains("/work/beta"))
            assertTrue(rowText(1).contains("fix the flaky test"))
            assertTrue(rowText(1).contains("first prompt"))
        }
    }

    @Test
    fun tappingSecondRowRunsItsTokenAndReportsItsPane() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, rowsResponse())
            server.respond(RUN_PATH, RunResponse.newBuilder().setPane("%9").build())
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(1) }
            composeRule.onNodeWithTag("picker-row:1").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { opened.get() != null }

            assertEquals("%9", opened.get())
            val runs = postsTo(server, RUN_PATH)
            assertEquals(1, runs.size)
            assertEquals(RunRequest.newBuilder().setToken("tok-resume").build(), RunRequest.parseFrom(runs.single().body))
        }
    }

    @Test
    fun askFailureShowsItsReasonAndTheTextCanBeResubmitted() {
        FakeOrreryServer().use { server ->
            server.fail(ASK_PATH, Code.UNAVAILABLE, "no answer from the host")
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("picker-error").assertTextEquals("no answer from the host")
            assertFalse(rowExists(0))

            server.respond(ASK_PATH, rowsResponse())
            composeRule.onNodeWithTag("picker-ask").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }

            assertEquals(2, postsTo(server, ASK_PATH).size)
            assertTrue(composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isEmpty())
        }
    }

    @Test
    fun runFailureShowsItsReasonAndTheRowsStay() {
        FakeOrreryServer().use { server ->
            server.respond(ASK_PATH, rowsResponse())
            server.fail(RUN_PATH, Code.FAILED_PRECONDITION, "the session is gone")
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }
            composeRule.onNodeWithTag("picker-row:0").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("picker-error").assertTextEquals("the session is gone")
            assertTrue(rowExists(0))
            assertTrue(rowExists(1))
            assertEquals(null, opened.get())
        }
    }

    @Test
    fun blankTextPostsNothing() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-text").performTextInput("   ")
            composeRule.onNodeWithTag("picker-ask").performClick()

            assertTrue(server.requests.isEmpty())
        }
    }

    @Test
    fun paneAndFreshRowsPostNoRunAndShowTheirFullFolder() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 0)
            composeRule.onNodeWithTag("start-dir").assertTextEquals(FULL_FOLDER)
            assertEquals("", fieldText("start-message"))

            composeRule.onNodeWithTag("picker-row:1").performClick()
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

            composeRule.onNodeWithTag("picker-row:1").performClick()
            composeRule.waitForIdle()

            assertEquals("", fieldText("start-message"))
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

            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { started.get() != null }

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
    fun aRefusedStartShowsItsReasonAndKeepsTheMessage() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("picker-error").assertTextEquals("the folder is not stamped")
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

            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { errorShown() }
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { startIds(server).size == 2 }

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

            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { errorShown() }
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { started.get() != null }

            val ids = startIds(server)
            assertEquals(2, ids.size)
            assertTrue(ID_FORM.matches(ids[0]))
            assertEquals(ids[0], ids[1])
        }
    }

    @Test
    fun aStartAfterAnEditedMessagePostsANewId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.fail(START_PATH, Code.FAILED_PRECONDITION, "the folder is not stamped")
            composeRule.onNodeWithTag("start-message").performTextInput("start it")

            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { errorShown() }
            composeRule.onNodeWithTag("start-message").performTextInput(" now")
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals("start it now", starts[1].prompt)
            assertTrue(ID_FORM.matches(starts[1].id))
            assertNotEquals(starts[0].id, starts[1].id)
        }
    }

    @Test
    fun aStartAfterAnAnsweredStartForTheSameFolderAndMessageReusesItsId() {
        FakeOrreryServer().use { server ->
            openStartForm(server, row = 1)
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { started.get() != null }

            composeRule.onNodeWithTag("picker-row:1").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { startIds(server).size == 2 }

            val ids = startIds(server)
            assertTrue(ID_FORM.matches(ids[0]))
            assertEquals(ids[0], ids[1])
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
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(1) }
            composeRule.onNodeWithTag("picker-row:0").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("start-form").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { errorShown() }

            composeRule.onNodeWithTag("picker-row:1").performClick()
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("start-dir").assertTextEquals(OTHER_FOLDER)
            composeRule.onNodeWithTag("start-message").performTextInput("start it")
            composeRule.onNodeWithTag("start-submit").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { startIds(server).size == 2 }

            val starts = postsTo(server, START_PATH).map { StartRequest.parseFrom(it.body) }
            assertEquals(FULL_FOLDER, starts[0].dir)
            assertEquals(OTHER_FOLDER, starts[1].dir)
            assertTrue(ID_FORM.matches(starts[1].id))
            assertNotEquals(starts[0].id, starts[1].id)
        }
    }

    @Test
    fun cancelClosesThePanel() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-cancel").performClick()

            assertEquals(1, closed.get())
        }
    }
}
