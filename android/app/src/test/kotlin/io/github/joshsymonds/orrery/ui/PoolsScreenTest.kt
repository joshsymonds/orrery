package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
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
import io.github.joshsymonds.orrery.mainThreadComposeRule
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
        const val POOL = "example-pool"
        const val PROJECT = "/home/user/alpha"
        const val PROJECT_DIR = "alpha"
        const val OTHER_PROJECT = "/home/user/beta"
        const val OTHER_DIR = "beta"
        const val FREE = "example-free"
        const val GRANTED = "example-granted"
        const val STOPPING = "example-stopping"
        const val HELD = "example-held"
        const val SECOND_FREE = "example-second-free"
        const val SECOND_HELD = "example-second-held"
        const val ORPHAN = "example-orphan"
        const val ASLEEP = "example-asleep"
        const val UNAVAILABLE = "example-unavailable"
        const val WAKING = "example-waking"
        const val ADMITTING = "example-admitting"
        const val POOLS_STATUS = "/orrery.v1.Pools/Status"
        const val POOLS_HOLD = "/orrery.v1.Pools/Hold"
        const val POOLS_FREE = "/orrery.v1.Pools/Free"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setScreen(server: FakeOrreryServer, onBack: () -> Unit = {}) {
        // One client for the screen's life, as MainActivity remembers its own: a client built inside the content is
        // a new key for the screen's load effect each time the content recomposes, and so an extra reload.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PoolsScreen(client = client, onBack = onBack, now = { NOW })
        }
    }

    @Test
    fun aClientBuiltInsideTheContentReloadsTheScreenWhenTheContentRecomposes() {
        // The old setup built the client inside the content: any recomposition of the content gave the screen a new
        // client, a new key for its load effect, and a load nobody asked for, whose Loading and result could replace
        // what a test had just asserted (on the unconfined rule its writes also ran on the IO thread beside the main
        // thread's layout). The content recomposed on its own in some runs and not others, so this forces one.
        val recompose = mutableIntStateOf(0)
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            composeRule.setContent {
                recompose.intValue
                PoolsScreen(client = OrreryClient(server.baseUrl), onBack = {}, now = { NOW })
            }
            awaitTag("member:$FREE")
            val loadsBefore = statusCalls(server)

            composeRule.runOnIdle { recompose.intValue++ }

            composeRule.awaitUntil("a load nobody asked for") { statusCalls(server) > loadsBefore }
            awaitTag("member:$FREE")
        }
    }

    @Test
    fun aRecompositionAboveTheScreenKeepsItsClientAndDoesNotReload() {
        // As setScreen does now: one client for the screen's life, so neither the content's own recomposition nor a
        // forced one reloads.
        val recompose = mutableIntStateOf(0)
        val contentRuns = AtomicInteger()
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                contentRuns.incrementAndGet()
                recompose.intValue
                PoolsScreen(client = client, onBack = {}, now = { NOW })
            }
            awaitTag("member:$FREE")
            val runsBefore = contentRuns.get()

            composeRule.runOnIdle { recompose.intValue++ }
            // The content must really recompose, or "no reload" proves nothing.
            composeRule.awaitUntil("the content to recompose") { contentRuns.get() > runsBefore }
            composeRule.waitForIdle()

            assertEquals("nothing is in flight", null, Quiescence.inFlight())
            assertEquals(1, statusCalls(server))
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    /** How many nodes under the test tag [tag] show [text]. */
    private fun textUnder(tag: String, text: String): Int =
        countNodes(hasText(text) and hasAnyAncestor(hasTestTag(tag)))

    /** Asserts the one chip under the member [member] that reads [state] carries [tone]. */
    private fun assertChipTone(member: String, state: String, tone: ChipTone) {
        composeRule.onNode(hasText(state) and hasAnyAncestor(hasTestTag("member:$member")), useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(ChipToneKey, tone))
    }

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) == 1 }
    }

    private fun awaitText(text: String) {
        composeRule.awaitUntil { countNodes(hasText(text)) > 0 }
    }

    private fun statusCalls(server: FakeOrreryServer): Int =
        server.requests.count { it.rawPath == POOLS_STATUS }

    private fun freeMember(name: String): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder().setName(name).setState("awake").build()

    private fun grantedMember(
        name: String,
        state: String = "admitted",
        stopping: Boolean = false,
        heldSince: Long = 0L,
    ): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder()
            .setName(name)
            .setState(state)
            .setHeldSince(heldSince)
            .setHolder(
                PoolHolderProto.newBuilder()
                    .setProject(PROJECT)
                    .addCommand("make")
                    .addCommand("test")
                    .setSince(NOW - 120)
                    .setStopping(stopping),
            )
            .build()

    private fun heldMember(name: String, since: Long): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder().setName(name).setState("awake").setHeldSince(since).build()

    private fun stateMember(name: String, state: String): PoolMemberStatusProto =
        PoolMemberStatusProto.newBuilder().setName(name).setState(state).build()

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
    fun theTitleRowReadsPoolsAndItsBackCallsOnBack() {
        val backs = AtomicInteger()
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            setScreen(server, onBack = { backs.incrementAndGet() })
            awaitTag("member:$FREE")

            assertEquals(1, countNodes(hasTestTag("crumb:0") and hasText("Pools")))
            composeRule.onNodeWithTag("title-back").performClick()

            assertEquals(1, backs.get())
        }
    }

    @Test
    fun aPoolHeadShowsItsNameAndItsMembersAndWaitingCounts() {
        FakeOrreryServer().use { server ->
            server.respond(
                POOLS_STATUS,
                status(
                    members = listOf(freeMember(FREE), grantedMember(GRANTED), heldMember(HELD, NOW - 10_800)),
                    waiters = listOf(waiter(1)),
                ),
            )
            setScreen(server)
            awaitTag("member:$GRANTED")

            assertEquals(1, textUnder("pool:$POOL", POOL.uppercase()))
            assertEquals(1, textUnder("pool:$POOL", "3 members · 1 waiting"))
        }
    }

    @Test
    fun aPoolWithOneMemberAndNoWaitersSaysOneMember() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            setScreen(server)
            awaitTag("member:$FREE")

            assertEquals(1, textUnder("pool:$POOL", "1 member"))
        }
    }

    @Test
    fun aFreeMemberShowsItsStateChipAndFreeLineAndAHoldAction() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            setScreen(server)
            awaitTag("member:$FREE")

            assertEquals(1, textUnder("member:$FREE", FREE))
            assertEquals(1, textUnder("member:$FREE", "awake"))
            assertChipTone(FREE, "awake", ChipTone.Live)
            assertEquals(1, textUnder("member:$FREE", "free"))
            assertEquals(1, textUnder("member:$FREE", "Hold"))
        }
    }

    @Test
    fun aGrantedMemberShowsItsGrantLineAndAHoldAction() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(grantedMember(GRANTED))))
            setScreen(server)
            awaitTag("member:$GRANTED")

            assertEquals(1, textUnder("member:$GRANTED", "granted to $PROJECT_DIR · make test · 2m"))
            assertChipTone(GRANTED, "admitted", ChipTone.Live)
            assertEquals(1, textUnder("member:$GRANTED", "Hold"))
        }
    }

    @Test
    fun aStoppingGrantSaysStoppingAfterItsAge() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(grantedMember(STOPPING, stopping = true))))
            setScreen(server)
            awaitTag("member:$STOPPING")

            assertEquals(1, textUnder("member:$STOPPING", "granted to $PROJECT_DIR · make test · 2m · stopping"))
        }
    }

    @Test
    fun aMemberGrantedAndHeldByYouShowsBothLinesAndAFreeAction() {
        FakeOrreryServer().use { server ->
            server.respond(
                POOLS_STATUS,
                status(listOf(grantedMember(GRANTED, heldSince = NOW - 10_800))),
            )
            setScreen(server)
            awaitTag("member:$GRANTED")

            assertEquals(1, textUnder("member:$GRANTED", "granted to $PROJECT_DIR · make test · 2m\nheld by you · 3h"))
            assertChipTone(GRANTED, "admitted", ChipTone.Attention)
            assertEquals(1, textUnder("member:$GRANTED", "Free"))
        }
    }

    @Test
    fun aMemberHeldByYouShowsHeldByYouAndAFreeAction() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(heldMember(HELD, NOW - 10_800))))
            setScreen(server)
            awaitTag("member:$HELD")

            assertEquals(1, textUnder("member:$HELD", "held by you · 3h"))
            assertChipTone(HELD, "awake", ChipTone.Attention)
            assertEquals(1, textUnder("member:$HELD", "Free"))
            assertEquals(0, textUnder("member:$HELD", "Hold"))
        }
    }

    @Test
    fun aMemberChipTakesItsToneFromItsState() {
        FakeOrreryServer().use { server ->
            server.respond(
                POOLS_STATUS,
                status(
                    listOf(
                        stateMember(ASLEEP, "asleep"),
                        stateMember(UNAVAILABLE, "unavailable"),
                        stateMember(WAKING, "waking"),
                        stateMember(ADMITTING, "admitting"),
                    ),
                ),
            )
            setScreen(server)
            awaitTag("member:$ADMITTING")

            assertChipTone(ASLEEP, "asleep", ChipTone.Neutral)
            assertChipTone(UNAVAILABLE, "unavailable", ChipTone.Bad)
            assertChipTone(WAKING, "waking", ChipTone.Live)
            assertChipTone(ADMITTING, "admitting", ChipTone.Live)
        }
    }

    @Test
    fun aWaiterShowsItsPlaceProjectCommandCountAndAge() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE)), waiters = listOf(waiter(1))))
            setScreen(server)
            awaitTag("waiter:$POOL:1")

            assertEquals(1, textUnder("waiter:$POOL:1", "#1 $OTHER_DIR"))
            assertEquals(1, textUnder("waiter:$POOL:1", "make · needs 2"))
            assertEquals(1, textUnder("waiter:$POOL:1", "waiting 30s"))
        }
    }

    @Test
    fun holdsOutsideAnyPoolAreListedWithAFreeAction() {
        FakeOrreryServer().use { server ->
            server.respond(
                POOLS_STATUS,
                status(
                    members = listOf(heldMember(HELD, NOW - 10_800)),
                    holds = listOf(
                        PoolHoldProto.newBuilder().setMember(HELD).setSince(NOW - 10_800).build(),
                        PoolHoldProto.newBuilder().setMember(ORPHAN).setSince(NOW - 172_800).build(),
                    ),
                ),
            )
            setScreen(server)
            awaitTag("outside-hold:$ORPHAN")

            assertEquals(1, countNodes(hasText("HOLDS OUTSIDE ANY POOL")))
            assertEquals(1, textUnder("outside-hold:$ORPHAN", "held by you · 2d"))
            assertEquals(1, textUnder("outside-hold:$ORPHAN", "Free"))
            assertEquals(0, countNodes(hasTestTag("outside-hold:$HELD")))
        }
    }

    @Test
    fun noPoolsAndNoHoldsSayWhatPoolsAreFor() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, PoolsStatusResponse.getDefaultInstance())
            setScreen(server)
            awaitText("No pools are in use.")

            assertEquals(1, countNodes(hasText("A pool shows here when a step asks for a machine from it.")))
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

            composeRule.awaitUntil { textUnder("member:$FREE", "held by you · 0s") == 1 }
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
            composeRule.awaitUntil { statusCalls(server) == 2 && countNodes(hasTestTag("member:$FREE")) == 1 }
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

            composeRule.awaitUntil { textUnder("member:$HELD", "free") == 1 }
            val free = server.requests.single { it.rawPath == POOLS_FREE }
            assertEquals(HELD, PoolsFreeRequest.parseFrom(free.body).member)
            assertEquals(2, statusCalls(server))
            assertEquals(1, textUnder("member:$HELD", "Hold"))
        }
    }

    @Test
    fun freePostsTheHeldMemberWhoseFreeWasTappedWhenTwoAreHeld() {
        FakeOrreryServer().use { server ->
            server.respondStatusTwice(
                first = status(listOf(heldMember(HELD, NOW - 10_800), heldMember(SECOND_HELD, NOW - 60))),
                later = status(listOf(heldMember(HELD, NOW - 10_800), freeMember(SECOND_HELD))),
            )
            server.respond(POOLS_FREE, PoolsFreeResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("member:$SECOND_HELD")

            composeRule.onNodeWithTag("member-action:$SECOND_HELD").performClick()

            composeRule.awaitUntil { statusCalls(server) == 2 }
            val free = server.requests.single { it.rawPath == POOLS_FREE }
            assertEquals(SECOND_HELD, PoolsFreeRequest.parseFrom(free.body).member)
        }
    }

    @Test
    fun holdPostsTheMemberWhoseHoldWasTappedWhenTwoAreFree() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE), freeMember(SECOND_FREE))))
            server.respond(POOLS_HOLD, PoolsHoldResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("member:$SECOND_FREE")

            composeRule.onNodeWithTag("member-action:$SECOND_FREE").performClick()

            composeRule.awaitUntil { statusCalls(server) == 2 }
            val hold = server.requests.single { it.rawPath == POOLS_HOLD }
            assertEquals(SECOND_FREE, PoolsHoldRequest.parseFrom(hold.body).member)
        }
    }

    @Test
    fun freePostsTheOutsideHoldWhoseFreeWasTappedWhenAMemberIsAlsoHeld() {
        FakeOrreryServer().use { server ->
            server.respondStatusTwice(
                first = status(
                    members = listOf(heldMember(HELD, NOW - 10_800)),
                    holds = listOf(PoolHoldProto.newBuilder().setMember(ORPHAN).setSince(NOW - 60).build()),
                ),
                later = status(listOf(freeMember(HELD))),
            )
            server.respond(POOLS_FREE, PoolsFreeResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("outside-hold:$ORPHAN")

            composeRule.onNodeWithTag("outside-hold-action:$ORPHAN").performClick()

            composeRule.awaitUntil { statusCalls(server) == 2 }
            val free = server.requests.single { it.rawPath == POOLS_FREE }
            assertEquals(ORPHAN, PoolsFreeRequest.parseFrom(free.body).member)
        }
    }

    @Test
    fun failedHoldShowsAnErrorNoteAndKeepsTheMembers() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            server.fail(POOLS_HOLD, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitTag("member:$FREE")

            composeRule.onNodeWithTag("member-action:$FREE").performClick()

            awaitText("Could not hold $FREE.")
            composeRule.onNodeWithText("Could not hold $FREE.")
                .assert(SemanticsMatcher.expectValue(NoteToneKey, NoteTone.Error))
            composeRule.awaitUntil { statusCalls(server) == 2 }
            awaitTag("member:$FREE")
            assertEquals(1, textUnder("member:$FREE", "free"))
            assertEquals(0, countNodes(hasText("Could not load the pools.")))
        }
    }

    @Test
    fun failedFreeShowsAnErrorNoteAndKeepsTheMembers() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status(listOf(heldMember(HELD, NOW - 10_800))))
            server.fail(POOLS_FREE, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitTag("member:$HELD")

            composeRule.onNodeWithTag("member-action:$HELD").performClick()

            awaitText("Could not free $HELD.")
            composeRule.onNodeWithText("Could not free $HELD.")
                .assert(SemanticsMatcher.expectValue(NoteToneKey, NoteTone.Error))
            composeRule.awaitUntil { statusCalls(server) == 2 }
            awaitTag("member:$HELD")
            assertEquals(1, textUnder("member:$HELD", "held by you · 3h"))
            assertEquals(1, textUnder("member:$HELD", "Free"))
            assertEquals(0, countNodes(hasText("Could not load the pools.")))
        }
    }

    @Test
    fun failedStatusShowsAnErrorNoteAndRetryReloads() {
        FakeOrreryServer().use { server ->
            server.fail(POOLS_STATUS, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitText("Could not load the pools.")
            composeRule.onNodeWithText("Could not load the pools.")
                .assert(SemanticsMatcher.expectValue(NoteToneKey, NoteTone.Error))

            server.respond(POOLS_STATUS, status(listOf(freeMember(FREE))))
            composeRule.onNodeWithText("Retry").performClick()

            awaitTag("member:$FREE")
        }
    }

    private fun waiter(position: Int): PoolWaiterProto =
        PoolWaiterProto.newBuilder()
            .setPosition(position)
            .setProject(OTHER_PROJECT)
            .addCommand("make")
            .setCount(2)
            .setSince(NOW - 30)
            .build()
}
