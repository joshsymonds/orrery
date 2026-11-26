package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
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
        const val HOST_STATUS = "/orrery.v1.Host/Status"

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

    @Test
    fun theHostsViewListsEachHostInTheServersOrder() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, threeHosts())
            setHosts(server)
            composeRule.awaitUntil("the hosts are read") { countNodes(hasTestTag("host-row:$REMOTE")) == 1 }
            assertEquals("the view is under the title Hosts", 1, countNodes(hasTestTag("crumb:0") and hasText("Hosts")))

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
