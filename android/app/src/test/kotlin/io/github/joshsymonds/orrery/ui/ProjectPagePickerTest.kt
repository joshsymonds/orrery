package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ProjectPagePickerTest {
    private companion object {
        const val ASK_PATH = "/orrery.v1.Picker/Ask"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val PROJECT_DIR = "/home/user/projects/synthetic-workspace/project-a"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        const val HINT = "Name a directory to start a session there, or say a few words about a session to find it."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val started = AtomicReference<String?>(null)

    private fun setPanel(server: FakeOrreryServer, startIn: String? = null) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onOpened = {},
                onStarted = { started.set(it) },
                onClose = {},
                startIn = startIn,
            )
        }
    }

    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun countTagged(tag: String): Int = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().size

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    @Test
    fun startInDrawsTheStartFormForThatDirectoryWithNoSearchResults() {
        FakeOrreryServer().use { server ->
            setPanel(server, startIn = PROJECT_DIR)

            composeRule.awaitUntil { countTagged("start-form") == 1 }
            composeRule.onNodeWithTag("start-dir").assertTextEquals(PROJECT_DIR)
            assertEquals("", composeRule.onNodeWithTag("start-message").fetchSemanticsNode()
                .config[SemanticsProperties.EditableText].text)
            assertEquals(0, countTagged("picker-row:0"))
            assertEquals(0, countTagged("picker-none"))
            assertEquals(emptyList<RecordedRequest>(), postsTo(server, ASK_PATH))
        }
    }

    @Test
    fun startInStartSendsStartWithThatDirectoryAndTheTypedMessageThenReportsTheSession() {
        FakeOrreryServer().use { server ->
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            setPanel(server, startIn = PROJECT_DIR)
            composeRule.awaitUntil { countTagged("start-form") == 1 }

            composeRule.onNodeWithTag("start-message").performTextInput("retry the webhook")
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            val starts = postsTo(server, START_PATH)
            assertEquals(1, starts.size)
            val request = StartRequest.parseFrom(starts.single().body)
            assertEquals(PROJECT_DIR, request.dir)
            assertEquals("retry the webhook", request.prompt)
            assertTrue(Regex("^[0-9a-f]{32}$").matches(request.id))
            assertEquals(ANSWERED_ID, started.get())
        }
    }

    @Test
    fun withoutStartInThePanelOpensOnTheEmptySearch() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-hint").assertTextEquals(HINT)
            assertEquals(0, countTagged("start-form"))
            assertEquals(0, countTagged("picker-row:0"))
            assertEquals(emptyList<RecordedRequest>(), postsTo(server, ASK_PATH))
        }
    }
}
