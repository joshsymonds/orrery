package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTextReplacement
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ProposeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ProposeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StartProposal
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class HostsDroidStarterTest {
    private companion object {
        const val PROPOSE_PATH = "/orrery.v1.Picker/Propose"
        const val HOST_STATUS_PATH = "/orrery.v1.Host/Status"
        const val START_PATH = "/orrery.v1.Control/Start"
        const val PROMPT = "Why is the roster page slow past forty shifts?"
        const val TIDEPOOL = "/home/user/code/tidepool"
        const val OFFLINE = "/home/user/code/tidepool.worktrees/offline"
        const val COMMONFRAME = "/home/user/work/commonframe"
        const val KELP = "/home/user/code/kelp"
        const val BUOYS = "/home/user/work/buoys"
        const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
        const val PLACEHOLDER = "What should it do? Say it in your words"
        const val HINT = "Press ⏎ and Orrery proposes where to run it."
        const val FINDING = "Finding where to run it…"
        const val NONE_FIT = "No reachable host has a folder that fits this. Choose a host and type a folder."
        const val ASK_UNAVAILABLE = "The search did not finish: the server could not answer."
        val ID_FORM = Regex("^[0-9a-f]{32}$")
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val started = AtomicReference<String?>(null)

    /** The serving host halyard and sextant answer; brine does not. */
    private fun hostStatus(): HostStatusResponse = HostStatusResponse.newBuilder()
        .setHost("halyard")
        .addHosts(hostEntry("halyard", reachable = true))
        .addHosts(hostEntry("sextant", reachable = true))
        .addHosts(hostEntry("brine", reachable = false))
        .build()

    private fun hostEntry(name: String, reachable: Boolean): HostEntry =
        HostEntry.newBuilder().setName(name).setReachable(reachable).build()

    private fun proposal(host: String, dir: String): StartProposal =
        StartProposal.newBuilder().setHost(host).setDir(dir).build()

    /** The ranked answer: brine's folder ranks first, but brine does not answer, so it is in no proposal. */
    private fun proposeBody(): ProposeResponse = ProposeResponse.newBuilder()
        .addProposals(proposal("brine", KELP))
        .addProposals(proposal("halyard", TIDEPOOL))
        .addProposals(proposal("halyard", OFFLINE))
        .addProposals(proposal("sextant", COMMONFRAME))
        .build()

    private fun showStarter(server: FakeOrreryServer) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onStarted = { started.set(it) },
                onClose = {},
            )
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

    private fun countTagged(tag: String): Int = composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().size

    private fun textOf(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    /**
     * Whether the node tagged [tag] has a descendant that [matcher] names, read in the unmerged tree where each label
     * is its own node, so a row or a button that merges its labels still answers for each of them.
     */
    private fun hasUnder(tag: String, matcher: SemanticsMatcher): Boolean =
        composeRule.onAllNodes(hasTestTag(tag) and hasAnyDescendant(matcher), useUnmergedTree = true)
            .fetchSemanticsNodes().isNotEmpty()

    private fun fieldText(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text

    private fun selected(index: Int): Boolean =
        composeRule.onNodeWithTag("picker-proposal:$index").fetchSemanticsNode().config[SemanticsProperties.Selected]

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    /** Submits [PROMPT] and waits until the proposals are drawn, with the top one preselected. */
    private fun proposeAndWait() {
        submit(PROMPT)
        composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
    }

    @Test
    fun aHostsDroidStarterTakesThePromptFirstAndProposesNothingUntilItIsSubmitted() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            showStarter(server)
            composeRule.awaitUntil { server.requests.any { it.rawPath == HOST_STATUS_PATH } }

            composeRule.onNodeWithText(PLACEHOLDER, useUnmergedTree = true).assertExists()
            composeRule.onNodeWithTag("picker-hint").assertTextEquals(HINT)
            assertEquals(0, countTagged("start-form"))
            assertEquals(0, countTagged("start-submit"))
            assertTrue(postsTo(server, PROPOSE_PATH).isEmpty())
        }
    }

    @Test
    fun aHostsDroidStarterSubmitProposesThePromptAndSaysFindingUntilItAnswers() {
        FakeOrreryServer().use { server ->
            val release = CountDownLatch(1)
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.route(PROPOSE_PATH) {
                release.await()
                Reply.Message(proposeBody(), 0L)
            }
            showStarter(server)

            submit(PROMPT)
            composeRule.onNodeWithTag("picker-working").assertTextEquals(FINDING)
            release.countDown()
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }

            assertEquals(0, countTagged("picker-working"))
            val posts = postsTo(server, PROPOSE_PATH)
            assertEquals(1, posts.size)
            assertEquals(PROMPT, ProposeRequest.parseFrom(posts.single().body).text)
        }
    }

    @Test
    fun aHostsDroidStarterDropsTheProposalsDownUnderThePromptWithTheTopOnePreselectedAndPlaced() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            showStarter(server)

            proposeAndWait()

            composeRule.waitForIdle()
            val prompt = composeRule.onNodeWithTag("picker-text").fetchSemanticsNode().boundsInRoot
            val list = composeRule.onNodeWithTag("picker-proposal-list").fetchSemanticsNode().boundsInRoot
            assertTrue(list.top >= prompt.bottom)
            assertTrue(hasUnder("picker-proposal:0", hasText(TIDEPOOL, substring = true)))
            assertTrue(hasUnder("picker-proposal:0", hasText("halyard")))
            assertTrue(hasUnder("picker-proposal:2", hasText(COMMONFRAME, substring = true)))
            assertTrue(hasUnder("picker-proposal:2", hasText("sextant")))
            assertEquals(0, countTagged("picker-proposal:3"))
            assertTrue(selected(0))
            assertFalse(selected(1))
            assertEquals(TIDEPOOL, fieldText("start-dir"))
            assertTrue(hasUnder("picker-host", hasText("halyard")))
        }
    }

    @Test
    fun aHostsDroidStarterPickingAnotherProposalSetsThePlaceFromIt() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            showStarter(server)
            proposeAndWait()

            tap("picker-proposal:2")
            composeRule.awaitUntil { fieldText("start-dir") == COMMONFRAME }
            assertTrue(hasUnder("picker-host", hasText("sextant")))

            tap("picker-proposals")
            composeRule.awaitUntil { countTagged("picker-proposal-list") == 1 }
            assertTrue(selected(2))
            assertFalse(selected(0))
        }
    }

    @Test
    fun aHostsDroidStarterWithNoFitSaysSoAndStartStaysOffUntilAFolderIsTyped() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, ProposeResponse.getDefaultInstance())
            showStarter(server)

            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-none") == 1 }

            composeRule.onNodeWithTag("picker-none").assertTextEquals(NONE_FIT)
            assertEquals(0, countTagged("picker-proposal:0"))
            composeRule.onNodeWithTag("start-submit").assertIsNotEnabled()

            composeRule.onNodeWithTag("start-dir").performTextReplacement(BUOYS)
            composeRule.onNodeWithTag("start-submit").assertIsEnabled()
        }
    }

    @Test
    fun aHostsDroidStarterFailedKeepsThePromptAndOffersTryAgainAndATypedPlace() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.fail(PROPOSE_PATH, Code.UNAVAILABLE, "server text")
            showStarter(server)

            submit(PROMPT)
            composeRule.awaitUntil { countTagged("picker-error") == 1 }

            composeRule.onNodeWithTag("picker-error").assertTextEquals(ASK_UNAVAILABLE)
            assertEquals(PROMPT, fieldText("picker-text"))
            composeRule.onNodeWithTag("picker-retry").assertIsEnabled()
            composeRule.onNodeWithTag("start-dir").performTextReplacement(BUOYS)
            composeRule.onNodeWithTag("start-submit").assertIsEnabled()

            server.respond(PROPOSE_PATH, proposeBody())
            tap("picker-retry")
            composeRule.awaitUntil { countTagged("picker-proposal:0") == 1 }
            assertEquals(0, countTagged("picker-error"))
        }
    }

    @Test
    fun aHostsDroidStarterHostThatDoesNotAnswerIsInNeitherTheProposalsNorTheHostChoice() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            showStarter(server)
            proposeAndWait()

            for (index in 0..2) {
                assertFalse(hasUnder("picker-proposal:$index", hasText("brine", substring = true)))
            }
            tap("picker-host")
            composeRule.awaitUntil { countTagged("picker-host-option:sextant") == 1 }

            assertEquals(1, countTagged("picker-host-option:halyard"))
            assertEquals(0, countTagged("picker-host-option:brine"))
        }
    }

    @Test
    fun aHostsDroidStarterTypedFolderOnAChosenHostClosesTheListUntilAProposalIsPickedAgain() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            showStarter(server)
            proposeAndWait()

            tap("picker-host")
            composeRule.awaitUntil { countTagged("picker-host-option:sextant") == 1 }
            tap("picker-host-option:sextant")
            composeRule.awaitUntil { hasUnder("picker-host", hasText("sextant")) }
            composeRule.onNodeWithTag("start-dir").performTextReplacement(BUOYS)

            composeRule.awaitUntil { countTagged("picker-proposal-list") == 0 }
            assertTrue(textOf("picker-checked").contains("sextant"))
            composeRule.onNodeWithTag("start-submit").assertIsEnabled()

            tap("picker-proposals")
            composeRule.awaitUntil { countTagged("picker-proposal-list") == 1 }
            tap("picker-proposal:0")
            composeRule.awaitUntil { fieldText("start-dir") == TIDEPOOL }
            assertTrue(hasUnder("picker-host", hasText("halyard")))
            assertEquals(0, countTagged("picker-checked"))
        }
    }

    @Test
    fun aHostsDroidStarterStartSendsTheChosenHostAndFolderToControlStart() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS_PATH, hostStatus())
            server.respond(PROPOSE_PATH, proposeBody())
            server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
            showStarter(server)
            proposeAndWait()

            tap("picker-host")
            composeRule.awaitUntil { countTagged("picker-host-option:sextant") == 1 }
            tap("picker-host-option:sextant")
            composeRule.awaitUntil { hasUnder("picker-host", hasText("sextant")) }
            composeRule.onNodeWithTag("start-dir").performTextReplacement(BUOYS)
            tap("start-submit")
            composeRule.awaitUntil { started.get() != null }

            val starts = postsTo(server, START_PATH)
            assertEquals(1, starts.size)
            val request = StartRequest.parseFrom(starts.single().body)
            assertEquals("sextant", request.host)
            assertEquals(BUOYS, request.dir)
            assertEquals(PROMPT, request.prompt)
            assertTrue(ID_FORM.matches(request.id))
            assertEquals(ANSWERED_ID, started.get())
        }
    }
}
