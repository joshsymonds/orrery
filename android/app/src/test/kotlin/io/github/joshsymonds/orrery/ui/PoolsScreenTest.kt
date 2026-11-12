package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHold as PoolHoldProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHolder as PoolHolderProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolMemberStatus as PoolMemberStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolStatus as PoolStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolWaiter as PoolWaiterProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsFreeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsFreeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsStatusResponse
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PoolsScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val TIMEOUT_MS = 5_000L
        const val POOL = "example-pool"
        const val PROJECT = "/home/user/alpha"
        const val OTHER_PROJECT = "/home/user/beta"
        const val FREE = "example-free"
        const val GRANTED = "example-granted"
        const val HELD = "example-held"
        const val ORPHAN = "example-orphan"
        const val POOLS_STATUS = "/orrery.v1.Pools/Status"
        const val POOLS_HOLD = "/orrery.v1.Pools/Hold"
        const val POOLS_FREE = "/orrery.v1.Pools/Free"
    }

    @get:Rule
    val composeRule = createComposeRule()

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            PoolsScreen(client = OrreryClient(server.baseUrl), onBack = {}, now = { NOW })
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    /** How many nodes under the test tag [tag] show [text]. */
    private fun textUnder(tag: String, text: String): Int =
        countNodes(hasText(text) and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitTag(tag: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasTestTag(tag)) == 1 }
    }

    private fun awaitText(text: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasText(text)) > 0 }
    }

    private fun statusCalls(server: FakeOrreryServer): Int =
        server.requests.count { it.rawPath == POOLS_STATUS }

    private fun freeMember(name: String): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder().setName(name).setState("awake").build()

    private fun grantedMember(name: String): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder()
            .setName(name)
            .setState("admitted")
            .setHolder(
                PoolHolderProto.newBuilder()
                    .setProject(PROJECT)
                    .addCommand("make")
                    .addCommand("test")
                    .setSince(NOW - 120),
            )
            .build()

    private fun heldMember(name: String, since: Long): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder().setName(name).setState("awake").setHeldSince(since).build()

    private fun status(
        members: List<PoolMemberStatusProto>,
        waiters: List<PoolWaiterProto> = emptyList(),
        holds: List<PoolHoldProto> = emptyList(),
    ): PoolsStatusResponse =
        PoolsStatusResponse.newBuilder()
            .addPools(
                PoolStatusProto.newBuilder()
                    .setName(POOL)
                    .setCap(2)
                    .addAllMembers(members)
                    .addAllWaiters(waiters),
            )
            .addAllHolds(holds)
            .build()

    /** Answers Status with [first] on its first call and with [later] on every call after it. */
    private fun FakeOrreryServer.respondStatusTwice(first: PoolsStatusResponse, later: PoolsStatusResponse) {
        val calls = AtomicInteger()
        route(POOLS_STATUS) { Reply.Message(if (calls.incrementAndGet() == 1) first else later, 0L) }
    }

    @Test
    fun poolsShowEachMemberWithItsStateWaitersAndOutsideHolds() {
        FakeOrreryServer().use { server ->
            server.respond(
                POOLS_STATUS,
                status(
                    members = listOf(freeMember(FREE), grantedMember(GRANTED), heldMember(HELD, NOW - 10_800)),
                    waiters = listOf(
                        PoolWaiterProto.newBuilder()
                            .setPosition(1)
                            .setProject(OTHER_PROJECT)
                            .addCommand("make")
                            .setCount(2)
                            .setSince(NOW - 30)
                            .build(),
                    ),
                    holds = listOf(
                        PoolHoldProto.newBuilder().setMember(HELD).setSince(NOW - 10_800).build(),
                        PoolHoldProto.newBuilder().setMember(ORPHAN).setSince(NOW - 172_800).build(),
                    ),
                ),
            )
            setScreen(server)
            awaitTag("member:$GRANTED")

            assertEquals(1, countNodes(hasText(POOL)))
            assertEquals(1, textUnder("member:$FREE", FREE))
            assertEquals(1, textUnder("member:$FREE", "free"))
            assertEquals(1, textUnder("member:$FREE", "awake"))
            assertEquals(1, textUnder("member:$GRANTED", "admitted"))
            assertEquals(1, textUnder("member:$GRANTED", GRANTED))
            assertEquals(1, textUnder("member:$GRANTED", "granted"))
            assertEquals(1, textUnder("member:$GRANTED", PROJECT))
            assertEquals(1, textUnder("member:$GRANTED", "make test"))
            assertEquals(1, textUnder("member:$GRANTED", "2m"))
            assertEquals(1, textUnder("member:$HELD", "held"))
            assertEquals(1, textUnder("member:$HELD", "3h"))
            assertEquals(1, textUnder("waiter:$POOL:1", "#1"))
            assertEquals(1, textUnder("waiter:$POOL:1", OTHER_PROJECT))
            assertEquals(1, textUnder("waiter:$POOL:1", "make"))
            assertEquals(1, textUnder("waiter:$POOL:1", "needs 2"))
            assertEquals(1, countNodes(hasTestTag("outside-hold:$ORPHAN")))
            assertEquals(1, textUnder("outside-hold:$ORPHAN", "2d"))
            assertEquals(0, countNodes(hasTestTag("outside-hold:$HELD")))
        }
    }

    @Test
    fun holdPostsTheFreeMemberThenReloadsShowingItHeld() {
        FakeOrreryServer().use { server ->
            server.respondStatusTwice(
                first = status(listOf(freeMember(FREE))),
                later = status(listOf(heldMember(FREE, NOW))),
            )
            server.respond(POOLS_HOLD, PoolsHoldResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("member:$FREE")
            assertEquals(1, textUnder("member:$FREE", "Hold"))

            composeRule.onNodeWithTag("member-action:$FREE").performClick()

            composeRule.waitUntil(TIMEOUT_MS) { textUnder("member:$FREE", "held") == 1 }
            val hold = server.requests.single { it.rawPath == POOLS_HOLD }
            assertEquals(FREE, PoolsHoldRequest.parseFrom(hold.body).member)
            assertEquals(2, statusCalls(server))
            assertEquals(1, textUnder("member:$FREE", "Free"))
        }
    }

    @Test
    fun freePostsAHoldOutsideAnyPoolThenReloads() {
        FakeOrreryServer().use { server ->
            server.respondStatusTwice(
                first = status(
                    members = listOf(freeMember(FREE)),
                    holds = listOf(PoolHoldProto.newBuilder().setMember(ORPHAN).setSince(NOW - 60).build()),
                ),
                later = status(listOf(freeMember(FREE))),
            )
            server.respond(POOLS_FREE, PoolsFreeResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("outside-hold:$ORPHAN")

            composeRule.onNodeWithTag("outside-hold-action:$ORPHAN").performClick()

            // The reload shows Loading first, which also hides the hold, so wait for the reloaded members.
            composeRule.waitUntil(TIMEOUT_MS) { statusCalls(server) == 2 && countNodes(hasTestTag("member:$FREE")) == 1 }
            assertEquals(0, countNodes(hasTestTag("outside-hold:$ORPHAN")))
            val free = server.requests.single { it.rawPath == POOLS_FREE }
            assertEquals(ORPHAN, PoolsFreeRequest.parseFrom(free.body).member)
        }
    }

    @Test
    fun freePostsTheHeldMemberThenReloadsShowingItFree() {
        FakeOrreryServer().use { server ->
            server.respondStatusTwice(
                first = status(listOf(heldMember(HELD, NOW - 10_800))),
                later = status(listOf(freeMember(HELD))),
            )
            server.respond(POOLS_FREE, PoolsFreeResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("member:$HELD")
            assertEquals(1, textUnder("member:$HELD", "Free"))

            composeRule.onNodeWithTag("member-action:$HELD").performClick()

            composeRule.waitUntil(TIMEOUT_MS) { textUnder("member:$HELD", "free") == 1 }
            val free = server.requests.single { it.rawPath == POOLS_FREE }
            assertEquals(HELD, PoolsFreeRequest.parseFrom(free.body).member)
            assertEquals(2, statusCalls(server))
            assertEquals(1, textUnder("member:$HELD", "Hold"))
        }
    }

    @Test
    fun failedHoldShowsAMessageAndKeepsTheMembers() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            server.fail(POOLS_HOLD, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitTag("member:$FREE")

            composeRule.onNodeWithTag("member-action:$FREE").performClick()

            awaitText("could not hold $FREE")
            composeRule.waitUntil(TIMEOUT_MS) { statusCalls(server) == 2 }
            awaitTag("member:$FREE")
            assertEquals(1, textUnder("member:$FREE", "free"))
            assertEquals(0, countNodes(hasText("could not load pools")))
        }
    }

    @Test
    fun failedStatusShowsCouldNotLoadPoolsAndRetryReloads() {
        FakeOrreryServer().use { server ->
            server.fail(POOLS_STATUS, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitText("could not load pools")

            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            composeRule.onNodeWithText("Retry").performClick()

            awaitTag("member:$FREE")
        }
    }
}
