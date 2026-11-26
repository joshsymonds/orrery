package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.tapInboxAction
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class HostsViewTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val HEADLESS_LIST = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val NOTICES_LIST = "/orrery.v1.Notices/List"

        // Invented hosts. The server lists them controller, spare, remote, which is not their alphabetical order.
        const val CONTROLLER = "example-controller"
        const val SPARE = "example-spare"
        const val REMOTE = "example-remote"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    /** The matcher [matcher] within the row of host [name]. */
    private fun inRow(name: String, matcher: SemanticsMatcher): SemanticsMatcher =
        matcher and hasAnyAncestor(hasTestTag("host-row:$name"))

    private fun hostEntry(name: String, load: Double, memory: Double, reachable: Boolean): HostEntryProto =
        HostEntryProto.newBuilder()
            .setName(name)
            .setLoadPercent(load)
            .setMemoryPercent(memory)
            .setReachable(reachable)
            .build()

    /** Two answering hosts, one with a load over a full CPU, and one that does not answer, in the server's order. */
    private fun threeHosts(controllerLoad: Double = 12.4): HostStatusResponse =
        HostStatusResponse.newBuilder()
            .setHost(CONTROLLER)
            .addHosts(hostEntry(CONTROLLER, controllerLoad, 40.6, true))
            .addHosts(hostEntry(SPARE, 130.0, 7.2, true))
            .addHosts(hostEntry(REMOTE, 0.0, 0.0, false))
            .build()

    /** Draws the hosts view on its own, as the inbox's Hosts action opens it. */
    private fun setHosts(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            HostsView(client = client, onBack = {})
        }
    }

    /** Draws the inbox, whose Hosts action opens the hosts view in its place, as MainActivity shows it. */
    private fun setInbox(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, threeHosts())
        server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
        server.respond(HEADLESS_LIST, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(NOTICES_LIST, NoticesListResponse.getDefaultInstance())
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            var open by remember { mutableStateOf(false) }
            if (open) {
                HostsView(client = client, onBack = { open = false })
            } else {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    onOpenHosts = { open = true },
                    now = { NOW },
                )
            }
        }
    }

    @Test
    fun theInboxHeadsHostsActionOpensTheHostsViewListingEachHostsLoadMemoryAndReach() {
        FakeOrreryServer().use { server ->
            setInbox(server)
            composeRule.awaitUntil("the inbox head is drawn") { countNodes(hasTestTag("inbox-head")) == 1 }

            composeRule.tapInboxAction("open-hosts")

            composeRule.awaitUntil("the hosts view lists $REMOTE") { countNodes(hasTestTag("host-row:$REMOTE")) == 1 }
            assertEquals("the view is under the title Hosts", 1, countNodes(hasTestTag("crumb:0") and hasText("Hosts")))
            // A reachable host shows its load and memory percents, and no unreachable mark.
            assertEquals(1, countNodes(inRow(CONTROLLER, hasText("12%"))))
            assertEquals(1, countNodes(inRow(CONTROLLER, hasText("41%"))))
            assertEquals(0, countNodes(inRow(CONTROLLER, hasText("unreachable", substring = true))))
            assertEquals(1, countNodes(inRow(SPARE, hasText("130%"))))
            assertEquals(1, countNodes(inRow(SPARE, hasText("7%"))))
            // A host that does not answer shows the word unreachable and no numbers.
            assertEquals(1, countNodes(inRow(REMOTE, hasText("unreachable"))))
            assertEquals(0, countNodes(inRow(REMOTE, hasText("%", substring = true))))
            assertEquals(0, countNodes(inRow(REMOTE, hasTestTag("host-meters"))))
        }
    }

    @Test
    fun theHostsViewListsEachHostInTheServersOrder() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, threeHosts())
            setHosts(server)
            composeRule.awaitUntil("the hosts are read") { countNodes(hasTestTag("host-row:$REMOTE")) == 1 }

            val tops = listOf(CONTROLLER, SPARE, REMOTE).map { name ->
                val row = composeRule.onNodeWithTag("host-row:$name", useUnmergedTree = true)
                row.fetchSemanticsNode().boundsInRoot.top
            }

            assertEquals("the rows run down the screen in the server's order", tops.sorted(), tops)
        }
    }

    @Test
    fun theHostsViewReadsHostStatusAgainEverySecondAndShowsTheAnswerItGets() {
        FakeOrreryServer().use { server ->
            val reads = AtomicInteger()
            server.route(HOST_STATUS) {
                // The first answer has a load of 12%; every later one has 34%.
                val load = if (reads.incrementAndGet() == 1) 12.0 else 34.0
                Reply.Message(threeHosts(controllerLoad = load), 0L)
            }
            setHosts(server)

            composeRule.awaitUntil("the first answer shows 12%") { countNodes(inRow(CONTROLLER, hasText("12%"))) == 1 }
            composeRule.awaitUntil("the view reads again and shows 34%") {
                countNodes(inRow(CONTROLLER, hasText("34%"))) == 1
            }
        }
    }
}
