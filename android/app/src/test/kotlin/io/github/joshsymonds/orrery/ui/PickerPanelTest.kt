package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
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
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
    }

    @get:Rule
    val composeRule = createComposeRule()

    private val opened = AtomicReference<String?>(null)
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

    private fun setPanel(server: FakeOrreryServer) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onOpened = { opened.set(it) },
                onClose = { closed.incrementAndGet() },
            )
        }
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
    fun cancelClosesThePanel() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-cancel").performClick()

            assertEquals(1, closed.get())
        }
    }
}
