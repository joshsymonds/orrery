package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.Move
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Invented tmux panes, each bound to a Claude session, read through serve in two answers. The moving row is in one
 * directory in the first answer and in another in the second, or behind the ended line in the second when it has ended;
 * a staying row stays in the first directory in both. The screen is removed and shown again with the same state, which
 * is the second answer. Compose semantics on a fake clock check the mark, its clearing, the ended line, reduced motion
 * and the open entry; the unit tests check spotsOf, movesBetween and the state's hold on invented projects.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidMoveTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val HOST = "example-host"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val ALPHA_TAG = "project:|$ALPHA"
        const val BETA_TAG = "project:|$BETA"
        const val ALPHA_ENDED_TAG = "ended:|$ALPHA"
        const val MOVING_PANE = "%1"
        const val STAYING_PANE = "%2"
        const val MOVING_ID = "10000000000000000000000000000001"
        const val STAYING_ID = "10000000000000000000000000000002"
        const val MOVING_TAG = "convo:pane:$HOST:$MOVING_PANE"
        const val STAYING_TAG = "convo:pane:$HOST:$STAYING_PANE"
        const val ALPHA_KEY = "alpha-key"
        const val BETA_KEY = "beta-key"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val state = PaneListState()
    private val opened = mutableListOf<String>()
    private var showing by mutableStateOf(true)

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

    /** A tmux pane's Session row: its marks, which serve sends with the id of the pane's @orrery_id. */
    private fun tmuxSession(id: String, ended: Boolean = false): SessionProto =
        SessionProto.newBuilder().setId(id).setEnded(ended).build()

    /** Serves the host and the two panes: the moving one in [cwd], ended or not, and the staying one in alpha. */
    private fun serve(server: FakeOrreryServer, cwd: String, ended: Boolean = false) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane(MOVING_PANE, MOVING_ID, cwd))
                .addPanes(tmuxPane(STAYING_PANE, STAYING_ID, ALPHA))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(tmuxSession(MOVING_ID, ended))
                .addSessions(tmuxSession(STAYING_ID))
                .build(),
        )
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            scale,
        )
    }

    /** Shows the inbox with the test's state. The first answer is awaited on the clock's own time. */
    private fun setScreen(server: FakeOrreryServer) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            if (showing) {
                PaneListScreen(
                    client = client,
                    onOpenPane = { opened.add(it.id) },
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                    state = state,
                )
            }
        }
    }

    /**
     * Removes the screen from the composition and shows it again with the same state, on a clock held still. The read that
     * the screen starts is the second answer, and it is applied with no frame drawn, so the marks begin at the returned
     * time. Draw a frame with [drawFrame] to show the answer.
     */
    private fun answerAgain(): Long {
        val firstRead = state.held
        composeRule.mainClock.autoAdvance = false
        composeRule.runOnUiThread { showing = false }
        drawFrame()
        composeRule.runOnUiThread { showing = true }
        drawFrame()
        val start = composeRule.mainClock.currentTime
        composeRule.awaitUntil("the second answer is applied") { state.held !== firstRead }
        return start
    }

    /** Applies the writes the test made, then draws one frame, so the composition shows them. */
    private fun drawFrame() {
        Snapshot.sendApplyNotifications()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
    }

    /** Moves the held clock forward to [time] without drawing a frame on the way. */
    private fun clockTo(time: Long) {
        composeRule.mainClock.advanceTimeBy(time - composeRule.mainClock.currentTime, ignoreFrameDuration = true)
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    private fun assertNoneMoving() {
        assertEquals(
            "no node carries moving",
            0,
            composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(Moving), useUnmergedTree = true)
                .fetchSemanticsNodes().size,
        )
    }

    /**
     * Holds the mark from [start], when its answer was applied, to Move.LAYOUT's end: still marked just short of that end,
     * and cleared once a frame draws past it.
     */
    private fun assertMarkClearsAfterTheLayoutMove(start: Long, matcher: SemanticsMatcher) {
        clockTo(start + Move.LAYOUT.millis - 1)
        assertEquals("still marked just before the layout move ends", 1, countNodes(matcher))
        clockTo(start + Move.LAYOUT.millis + 1)
        drawFrame()
        assertEquals("cleared once the layout move ends", 0, countNodes(matcher))
    }

    @Test
    fun aMovedRowIsMarkedMovingInItsNewPlaceUntilTheLayoutMoveEnds() {
        FakeOrreryServer().use { server ->
            setAnimatorDurationScale(1f)
            serve(server, cwd = ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(MOVING_TAG))
            assertNoneMoving()

            serve(server, cwd = BETA)
            val start = answerAgain()
            drawFrame()

            // The row is in its new place, and it alone is marked; the staying row is in its place and unmarked.
            assertEquals(1, countNodes(hasTestTag(MOVING_TAG) and hasAnyAncestor(hasTestTag(BETA_TAG))))
            assertEquals(1, countNodes(hasTestTag(STAYING_TAG) and hasAnyAncestor(hasTestTag(ALPHA_TAG))))
            assertEquals(1, countNodes(moving))

            assertMarkClearsAfterTheLayoutMove(start, hasTestTag(MOVING_TAG) and moving)
            assertNoneMoving()
        }
    }

    @Test
    fun aRowThatLandsBehindAShutEndedLineMarksTheLineAndKeepsItShut() {
        FakeOrreryServer().use { server ->
            setAnimatorDurationScale(1f)
            serve(server, cwd = ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(MOVING_TAG))

            serve(server, cwd = ALPHA, ended = true)
            val start = answerAgain()
            drawFrame()

            // The row lands behind the shut line: the line is marked, the row is not drawn, and the line counts it.
            assertEquals(0, countNodes(hasTestTag(MOVING_TAG)))
            assertEquals(1, countNodes(hasText("1 ended")))
            assertEquals(1, countNodes(hasTestTag(STAYING_TAG) and hasAnyAncestor(hasTestTag(ALPHA_TAG))))
            assertEquals(1, countNodes(hasTestTag(ALPHA_ENDED_TAG) and moving))
            assertEquals(1, countNodes(moving))

            assertMarkClearsAfterTheLayoutMove(start, hasTestTag(ALPHA_ENDED_TAG) and moving)
            assertEquals(1, countNodes(hasText("1 ended")))
        }
    }

    @Test
    fun withReducedMotionNothingIsMarkedButTheRowLandsInItsNewPlace() {
        FakeOrreryServer().use { server ->
            setAnimatorDurationScale(0f)
            serve(server, cwd = ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(MOVING_TAG))

            serve(server, cwd = BETA)
            answerAgain()
            drawFrame()

            assertEquals(1, countNodes(hasTestTag(MOVING_TAG) and hasAnyAncestor(hasTestTag(BETA_TAG))))
            assertNoneMoving()
        }
    }

    @Test
    fun theOpenEntryAMoveTookStaysDrawnInItsPlaceAndOpen() {
        FakeOrreryServer().use { server ->
            setAnimatorDurationScale(1f)
            serve(server, cwd = ALPHA)
            setScreen(server)
            awaitNode(hasTestTag(MOVING_TAG))
            composeRule.onNodeWithTag(MOVING_TAG).performClick()
            assertEquals(listOf(MOVING_PANE), opened)

            serve(server, cwd = ALPHA, ended = true)
            answerAgain()
            drawFrame()

            // The opened row that the answer ended is drawn in its tree place, its line is not drawn, and its move is not
            // marked.
            assertEquals(1, countNodes(hasTestTag(MOVING_TAG) and hasAnyAncestor(hasTestTag(ALPHA_TAG))))
            assertEquals(0, countNodes(hasTestTag(ALPHA_ENDED_TAG)))
            assertNoneMoving()

            composeRule.onNodeWithTag(MOVING_TAG).performClick()
            assertEquals(listOf(MOVING_PANE, MOVING_PANE), opened)
        }
    }

    private fun paneOf(id: String): Pane = Pane(
        id = id,
        host = HOST,
        session = "example-session",
        window = 1,
        pane = 0,
        cwd = ALPHA,
        command = "claude",
        state = "working",
        summary = "Synthetic summary.",
        label = "label-$id",
        changed = NOW - 60,
        seen = NOW - 60,
        question = "",
        questionHash = "",
    )

    private fun convoOf(id: String): Convo = Convo(
        key = "pane:$HOST:$id",
        pane = paneOf(id),
        hung = emptyList(),
        folded = emptyList(),
    )

    private fun projectOf(
        key: String,
        name: String,
        convos: List<Convo> = emptyList(),
        ended: List<Convo> = emptyList(),
    ): Project = Project(
        key = key,
        dir = "/home/user/$name",
        name = name,
        path = "~/$name",
        host = "",
        convos = convos,
        ended = ended,
    )

    private fun alphaWith(vararg ids: String): Project =
        projectOf(ALPHA_KEY, "alpha", convos = ids.map { convoOf(it) })

    private fun betaWith(vararg ids: String): Project =
        projectOf(BETA_KEY, "beta", convos = ids.map { convoOf(it) })

    @Test
    fun spotsOfDrawsEachConversationAtTheLineItIsOn() {
        val inbox = listOf(projectOf(ALPHA_KEY, "alpha", convos = listOf(convoOf("1")), ended = listOf(convoOf("2"))))
        val archived = listOf(projectOf(ALPHA_KEY, "alpha", convos = listOf(convoOf("3"))))

        val spots = spotsOf(inbox, archived)

        assertEquals(listOf("pane:$HOST:1", "pane:$HOST:2", "pane:$HOST:3"), spots.keys.toList())
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.TREE), spots["pane:$HOST:1"]?.spot)
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.ENDED), spots["pane:$HOST:2"]?.spot)
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.ARCHIVED), spots["pane:$HOST:3"]?.spot)
        assertEquals(paneOf("1"), spots["pane:$HOST:1"]?.pane)
    }

    @Test
    fun spotsOfLeavesOutStandInRows() {
        val standIn = Convo(key = "stand-in", pane = null, hung = emptyList(), folded = emptyList())
        val inbox = listOf(projectOf(ALPHA_KEY, "alpha", convos = listOf(convoOf("1"), standIn)))

        assertEquals(setOf("pane:$HOST:1"), spotsOf(inbox, emptyList()).keys)
    }

    @Test
    fun movesBetweenReportsOnlyTheRowsWhoseSpotChanged() {
        val before = spotsOf(listOf(alphaWith("1", "2", "4")), emptyList())
        val after = spotsOf(
            listOf(
                projectOf(ALPHA_KEY, "alpha", convos = listOf(convoOf("2")), ended = listOf(convoOf("1"))),
                betaWith("5", "3"),
            ),
            emptyList(),
        )

        val moves = movesBetween(before, after)

        // Row 2 stays on its line, row 4 is gone and rows 3 and 5 are new: none of them is a move.
        assertEquals(listOf("pane:$HOST:1"), moves.map { it.key })
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.TREE), moves.single().from)
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.ENDED), moves.single().to)
        assertEquals(paneOf("1"), moves.single().pane)
    }

    @Test
    fun movesBetweenListsTheMovesInTheLaterAnswersOrder() {
        val before = spotsOf(listOf(alphaWith("1", "2")), emptyList())
        val after = spotsOf(listOf(betaWith("2", "1")), emptyList())

        val moves = movesBetween(before, after)

        assertEquals(listOf("pane:$HOST:2", "pane:$HOST:1"), moves.map { it.key })
        assertEquals(Spot(ALPHA_KEY, "alpha", RowLine.TREE), moves[0].from)
        assertEquals(Spot(BETA_KEY, "beta", RowLine.TREE), moves[0].to)
    }

    @Test
    fun theFirstAnswerMovesNothingAndTheOwnersHandMovesAreNotMoves() {
        assertEquals(emptyList<Moved>(), state.answered(spotsOf(listOf(alphaWith("1", "2")), emptyList())))

        state.handMoved = setOf("pane:$HOST:1")
        val moves = state.answered(spotsOf(listOf(betaWith("1", "2")), emptyList()))

        assertEquals(listOf("pane:$HOST:2"), moves.map { it.key })
        assertEquals(moves, state.moves)
        assertEquals(emptySet<String>(), state.handMoved)
    }

    @Test
    fun theOpenEntryIsHeldOutOfTheTreeUntilAnotherEntryOpens() {
        state.answered(spotsOf(listOf(alphaWith("1")), emptyList()))
        state.opened = "pane:$HOST:1"
        val ended = spotsOf(listOf(projectOf(ALPHA_KEY, "alpha", ended = listOf(convoOf("1")))), emptyList())

        state.answered(ended)
        assertEquals("pane:$HOST:1", state.heldKey)

        state.answered(ended)
        assertEquals("pane:$HOST:1", state.heldKey)

        state.opened = "epic:some-epic"
        state.answered(ended)
        assertEquals(null, state.heldKey)
    }
}
