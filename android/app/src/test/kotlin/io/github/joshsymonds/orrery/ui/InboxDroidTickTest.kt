package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Invented tmux panes, each bound to a Claude session, served through FakeOrreryServer on a fake clock that stands still:
 * each check moves it by hand. The inbox is started and stopped by hand through HandLifecycleOwner. A tick is TICK_MILLIS
 * of the fake clock, and the checks are the calls the server received and Compose semantics: the moving flag on a row and
 * the move notice at the foot.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidTickTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val HOST = "example-host"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val NOTICES_LIST = "/orrery.v1.Notices/List"
        const val ACCOUNTS_LIST = "/orrery.v1.Accounts/List"
        const val ROUTES_GET = "/orrery.v1.Routes/Get"
        const val USAGE_READINGS = "/orrery.v1.Usage/Readings"
        const val USAGE_SPEND = "/orrery.v1.Usage/Spend"
        const val ALPHA_TAG = "project:|$ALPHA"
        const val BETA_TAG = "project:|$BETA"
        const val MOVING_PANE = "%1"
        const val STAYING_PANE = "%2"
        const val MOVING_ID = "10000000000000000000000000000001"
        const val STAYING_ID = "10000000000000000000000000000002"
        const val MOVING_TAG = "convo:pane:$HOST:$MOVING_PANE"
        const val STAYING_TAG = "convo:pane:$HOST:$STAYING_PANE"
        const val NOTICE_LINE = "move-notice-line"
        const val MOVE_LINE = "Moved “label-%1” to beta."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val moving: SemanticsMatcher = SemanticsMatcher.expectValue(Moving, true)

    private fun tmuxPane(id: String, orreryId: String, cwd: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(NOW - 60)
        .setSeen(NOW - 60)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(NOW - 60)
        .setOrreryId(orreryId)
        .setClaudeSession("synthetic-claude-$id")
        .build()

    /** The moving pane in [movingCwd] and the staying pane in alpha, with their sessions unarchived and unended. */
    private fun serve(server: FakeOrreryServer, movingCwd: String) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_LIST, ContractsListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane(MOVING_PANE, MOVING_ID, movingCwd))
                .addPanes(tmuxPane(STAYING_PANE, STAYING_ID, ALPHA))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(SessionProto.newBuilder().setId(MOVING_ID))
                .addSessions(SessionProto.newBuilder().setId(STAYING_ID))
                .build(),
        )
    }

    /** A lifecycle the test starts, already at started, as the activity's is while the inbox shows. */
    private fun startedOwner(): HandLifecycleOwner =
        HandLifecycleOwner().also { it.lifecycle.currentState = Lifecycle.State.STARTED }

    /** Shows the inbox under [owner], made once outside the composition so a recomposition reads with the same client. */
    private fun setScreen(server: FakeOrreryServer, owner: LifecycleOwner) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides owner) {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                )
            }
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    /**
     * Every call the inbox has made so far, by procedure path. The bell reads the notice history each time the inbox starts,
     * as it always has, and the usage band reads the accounts, the routes, the readings and the spend on its own minute
     * while started, so those reads are left out: the inbox's own reads are the rest.
     */
    private fun readsOf(server: FakeOrreryServer): Map<String, Int> =
        server.requests.filter { it.rawPath !in otherReads }.groupingBy { it.rawPath }.eachCount()

    /** The reads the bell and the usage band make on their own, which are not the inbox's row reads. */
    private val otherReads = setOf(NOTICES_LIST, ACCOUNTS_LIST, ROUTES_GET, USAGE_READINGS, USAGE_SPEND)

    /** The calls made since [before], by path, leaving out the paths that were not called. */
    private fun readsSince(server: FakeOrreryServer, before: Map<String, Int>): Map<String, Int> {
        val now = readsOf(server)
        return (now.keys + before.keys).associateWith { now.getOrDefault(it, 0) - before.getOrDefault(it, 0) }
            .filterValues { it != 0 }
    }

    /** Lets the main thread and every request it started finish, so the screen has applied what it read. */
    private fun settle() {
        Quiescence.awaitUntil("the screen's work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    /** Applies what the screen has written, then draws one frame, so the composition shows it. */
    private fun drawFrame() {
        Snapshot.sendApplyNotifications()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
    }

    /** Waits until [condition] holds on the drawn screen. The clock stands still, so each check draws a frame first. */
    private fun awaitDrawn(description: String, condition: () -> Boolean) {
        composeRule.awaitUntil(description) {
            drawFrame()
            condition()
        }
    }

    /** Shows the inbox with the moving row in alpha, and waits until the first answer draws it. */
    private fun showFirstAnswer(server: FakeOrreryServer, owner: LifecycleOwner) {
        serve(server, movingCwd = ALPHA)
        setScreen(server, owner)
        awaitDrawn("the first answer draws the moving row in alpha") {
            countNodes(hasTestTag(MOVING_TAG) and hasAnyAncestor(hasTestTag(ALPHA_TAG))) == 1
        }
        settle()
    }

    @Test
    fun whileStartedOneTickReadsOnlyThePanesHeadlessAndSessionLists() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            showFirstAnswer(server, startedOwner())
            val before = readsOf(server)

            composeRule.mainClock.advanceTimeBy(TICK_MILLIS, ignoreFrameDuration = true)
            composeRule.awaitUntil("the tick reads the row lists") {
                readsOf(server).getOrDefault(PANES_LIST, 0) > before.getOrDefault(PANES_LIST, 0)
            }
            settle()

            assertEquals(
                "one tick calls the three row lists once each and no other read",
                mapOf(PANES_LIST to 1, LIST_HEADLESS to 1, SESSIONS_LIST to 1),
                readsSince(server, before),
            )
        }
    }

    @Test
    fun aRowThatMovesOnATickShowsTheMovingFlagAndTheMoveNotice() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            showFirstAnswer(server, startedOwner())

            serve(server, movingCwd = BETA)
            composeRule.mainClock.advanceTimeBy(TICK_MILLIS, ignoreFrameDuration = true)
            awaitDrawn("the moved row is drawn in beta, with the move notice") {
                countNodes(hasTestTag(MOVING_TAG) and hasAnyAncestor(hasTestTag(BETA_TAG))) == 1 &&
                    countNodes(hasTestTag(NOTICE_LINE)) == 1
            }

            assertEquals("the moved row carries the moving flag", 1, countNodes(hasTestTag(MOVING_TAG) and moving))
            assertEquals("the row that stayed carries no moving flag", 0, countNodes(hasTestTag(STAYING_TAG) and moving))
            composeRule.onNodeWithTag(NOTICE_LINE).assertTextEquals(MOVE_LINE)
        }
    }

    @Test
    fun onceStoppedTheTickStopsAndNoListIsReadAgain() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            val owner = startedOwner()
            showFirstAnswer(server, owner)

            // While started, a tick reads the lists, so the quiet that follows a stop is the stop's doing.
            composeRule.mainClock.advanceTimeBy(TICK_MILLIS, ignoreFrameDuration = true)
            composeRule.awaitUntil("the tick reads while started") {
                readsOf(server).getOrDefault(PANES_LIST, 0) == 2
            }
            settle()

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.CREATED }
            settle()
            val before = readsOf(server)
            composeRule.mainClock.advanceTimeBy(TICK_MILLIS * 3, ignoreFrameDuration = true)
            settle()

            assertEquals("no list is read while the inbox is stopped", emptyMap<String, Int>(), readsSince(server, before))
        }
    }

    @Test
    fun theTickStartsAgainWhenTheInboxIsStartedAgain() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            val owner = startedOwner()
            showFirstAnswer(server, owner)
            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.CREATED }
            settle()
            val before = readsOf(server)

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.STARTED }
            settle()
            assertEquals("starting again reads nothing at once", emptyMap<String, Int>(), readsSince(server, before))

            composeRule.mainClock.advanceTimeBy(TICK_MILLIS, ignoreFrameDuration = true)
            composeRule.awaitUntil("the tick reads the row lists again") {
                readsOf(server).getOrDefault(PANES_LIST, 0) > before.getOrDefault(PANES_LIST, 0)
            }
            settle()

            assertEquals(
                "the tick calls the three row lists once each",
                mapOf(PANES_LIST to 1, LIST_HEADLESS to 1, SESSIONS_LIST to 1),
                readsSince(server, before),
            )
        }
    }
}
