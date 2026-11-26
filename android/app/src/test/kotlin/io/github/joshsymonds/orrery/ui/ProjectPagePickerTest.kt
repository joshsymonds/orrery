package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
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
        const val PROPOSE_PATH = "/orrery.v1.Picker/Propose"
        const val HOST_STATUS_PATH = "/orrery.v1.Host/Status"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val PROJECT_DIR = "/home/user/projects/synthetic-workspace/project-a"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        const val HINT = "Press ⏎ and Orrery proposes where to run it."
        const val SERVING_HOST = "halyard"
        const val OTHER_HOST = "sextant"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val started = AtomicReference<String?>(null)

    /** The serving host halyard, and sextant, both reachable. */
    private fun hostStatus(): HostStatusResponse = HostStatusResponse.newBuilder()
        .setHost(SERVING_HOST)
        .addHosts(HostEntry.newBuilder().setName(SERVING_HOST).setReachable(true))
        .addHosts(HostEntry.newBuilder().setName(OTHER_HOST).setReachable(true))
        .build()

    private fun setPanel(server: FakeOrreryServer, startIn: String? = null, startHost: String? = null) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onStarted = { started.set(it) },
                onClose = {},
                startIn = startIn,
                startHost = startHost,
            )
        }
    }

    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun countTagged(tag: String): Int = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().size

    private fun fieldText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text

    /** Whether the host choice names [host], read in the unmerged tree, where the host's label is its own node. */
    private fun hostIs(host: String): Boolean =
        composeRule.onAllNodes(hasTestTag("picker-host") and hasAnyDescendant(hasText(host)), useUnmergedTree = true)
            .fetchSemanticsNodes().isNotEmpty()

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    @Test
    fun startInOpensTheStarterWithThatFolderTypedAndNoProposals() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            setPanel(server, startIn = PROJECT_DIR)

            composeRule.awaitUntil { countTagged("start-dir") == 1 }
            assertEquals(PROJECT_DIR, fieldText("start-dir"))
            assertEquals("", fieldText("picker-text"))
            assertEquals(0, countTagged("picker-proposal:0"))
            assertEquals(0, countTagged("picker-hint"))
            assertEquals(emptyList<RecordedRequest>(), postsTo(server, PROPOSE_PATH))
        }
    }

    @Test
    fun startInTypesTheFolderOnTheServingHostWhenItsCallerGivesNoHost() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            setPanel(server, startIn = PROJECT_DIR)

            composeRule.awaitUntil { hostIs(SERVING_HOST) }
        }
    }

    @Test
    fun startInStartSendsStartWithThatDirectoryAndTheTypedPromptThenReportsTheSession() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            setPanel(server, startIn = PROJECT_DIR)
            composeRule.awaitUntil { countTagged("picker-host") == 1 }

            composeRule.onNodeWithTag("picker-text").performTextInput("retry the webhook")
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
    fun startInOnTheHostItsCallerGivesStartsThere() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            setPanel(server, startIn = PROJECT_DIR, startHost = OTHER_HOST)
            composeRule.awaitUntil { hostIs(OTHER_HOST) }

            composeRule.onNodeWithTag("picker-text").performTextInput("retry the webhook")
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            assertEquals(OTHER_HOST, StartRequest.parseFrom(postsTo(server, START_PATH).single().body).host)
        }
    }

    @Test
    fun withoutStartInTheStarterOpensOnThePromptAlone() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-hint").assertTextEquals(HINT)
            assertEquals(0, countTagged("start-dir"))
            assertEquals(0, countTagged("picker-proposal:0"))
            assertEquals(emptyList<RecordedRequest>(), postsTo(server, PROPOSE_PATH))
        }
    }
}
