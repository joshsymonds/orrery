package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// The motion of the round 8 phone sheet on a pane: it unfolds down from the band under the head, folds back up into it on
// close, switches its body in place, and swaps at once with the animator duration scale at 0. Read through the semantics
// tree alone, on the virtual clock, with the pane's records invented here.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PaneMotionTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val PANE_ID = "%7"
        const val ORRERY_ID = "synthetic-orrery-id"
        const val REPOSITORY = "/home/user/lighthouse"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val DESIGN_STATE = "0 of 1 accepted"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val EPIC_ID = "synthetic-owner/synthetic-epic"
        const val EPIC_SLUG = "synthetic-epic"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val DESIGN_BODY_TAG = "sheet-body:design:$DESIGN_ID"
        const val CONTRACT_BODY_TAG = "sheet-body:contract:$CONTRACT_ID"
        const val CONTRACT_ROW_TAG = "design-contract:$CONTRACT_ID"
        const val PANE_WATCH_PATH = "/orrery.v1.Panes/WatchConversation"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val DESIGN_GET_PATH = "/orrery.v1.Designs/Get"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val context: Context = ApplicationProvider.getApplicationContext()

    /** Sets the system's animator duration scale, which the phone sheet reads when the pane is composed. */
    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, scale)
    }

    private fun setPane(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { NOW }) }
        }
    }

    /** Serves the pane [PANE_ID] with the orrery id [ORRERY_ID], and no stream, so its screen polls. */
    private fun servePane(server: FakeOrreryServer) {
        server.route(PANE_WATCH_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        val pane = PaneProto.newBuilder()
            .setId(PANE_ID)
            .setOrreryId(ORRERY_ID)
            .setHost("example-host")
            .setCwd(REPOSITORY)
            .setCommand("claude")
            .setState("waiting")
            .setLabel("synthetic pane")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(pane).build())
    }

    /**
     * The designs, epics and contracts the pane's head reads: [designs] and [epics] as the server lists them. Each of
     * [designs] is also what Designs.Get answers for its id, and an id none of them has is not found.
     */
    private fun serveWork(server: FakeOrreryServer, designs: List<Design>, epics: List<Epic>) {
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addAllDesigns(designs).build())
        server.route(DESIGN_GET_PATH) { request ->
            val id = DesignsGetRequest.parseFrom(request.body).id
            val design = designs.firstOrNull { it.id == id }
            if (design == null) {
                Reply.Failure(Code.NOT_FOUND, "", 0L)
            } else {
                Reply.Message(DesignsGetResponse.newBuilder().setDesign(design).build(), 0L)
            }
        }
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addAllEpics(epics).build())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    private fun design(
        id: String,
        title: String,
        conversation: String,
        contracts: List<DesignContract> = emptyList(),
    ): Design = Design.newBuilder()
        .setId(id)
        .setTitle(title)
        .setConversation(conversation)
        .setCreated(NOW - 600)
        .setOfOne(false)
        .setState(DESIGN_STATE)
        .addAllContracts(contracts)
        .build()

    private fun designContract(id: String, epic: String = ""): DesignContract = DesignContract.newBuilder()
        .setId(id)
        .setTitle("Synthetic contract")
        .setRepository("synthetic-owner")
        .setState("proposed")
        .setPosition(1L)
        .setEpic(epic)
        .build()

    /** An epic [steering] names as its conversation, live, with the id [id] and the slug [slug]. */
    private fun epic(id: String, slug: String, steering: String): Epic = Epic.newBuilder()
        .setId(id)
        .setRepository(REPOSITORY)
        .setSlug(slug)
        .setSource("orreryd")
        .setOutcome("accepted")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setSteering(steering)
        .build()

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /**
     * Opens the design's sheet from its work link once the link is drawn, then stops the clock: from here on the test
     * moves time only by the virtual clock, and the tap's first frame is the start of the motion.
     */
    private fun openDesignFromItsLink() {
        awaitTag(DESIGN_LINK_TAG)
        composeRule.mainClock.autoAdvance = false
        composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
    }

    /** Advances the clock past the sheet's rise, its delay included, so a rise that began at the last tap has ended. */
    private fun settleSheet() {
        composeRule.mainClock.advanceTimeBy((OrreryTokens.Motion.Delay.rise + Move.RISE.millis).toLong() * 2)
    }

    /** The node tagged [tag] in the unmerged tree, as it is drawn now. */
    private fun node(tag: String): SemanticsNode =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode()

    /** The pose the phone sheet's node is at now. */
    private fun sheetPose(): SheetPose = node(PHONE_SHEET_TAG).config[SheetPoseKey]

    /** The alpha the node tagged [tag] is at now: the band's, or a body's. */
    private fun sheetFade(tag: String): Float = node(tag).config[SheetFadeKey]

    /** The virtual clock's time now, in milliseconds. */
    private fun clockNow(): Long = composeRule.mainClock.currentTime

    /** Whether the phone sheet is drawn now. */
    private fun sheetDrawn(): Boolean =
        composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    /**
     * Advances the virtual clock one frame at a time until it reads [until] or later, calling [atFrame] with the time of each
     * frame that falls before [until]. The frames are the test clock's, and no wall clock is read.
     */
    private fun stepFramesUntil(until: Long, atFrame: (Long) -> Unit = {}) {
        while (clockNow() < until) {
            composeRule.mainClock.advanceTimeByFrame()
            if (clockNow() < until) atFrame(clockNow())
        }
    }

    /**
     * Steps the clock frame by frame from the tap at [opened] until the sheet's alpha leaves 0, and returns the clock's time at
     * that frame. Each frame before it holds the sheet at [SheetPose.FOLDED], transparent, and it must show by the end of its rise.
     */
    private fun stepUntilSheetShows(opened: Long): Long {
        val deadline = opened + OrreryTokens.Motion.Delay.rise + Move.RISE.millis
        var shown = -1L
        while (shown < 0) {
            composeRule.mainClock.advanceTimeByFrame()
            val pose = sheetPose()
            if (pose.alpha > 0f) {
                shown = clockNow()
            } else {
                assertEquals("the sheet is held folded ${clockNow() - opened} ms after the tap", SheetPose.FOLDED, pose)
                assertTrue("the sheet shows by the end of its rise", clockNow() < deadline)
            }
        }
        return shown
    }

    @Test
    fun paneMotionUnfoldsTheSheetDownFromTheBandAndRestsAtFullHeight() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()

            composeRule.mainClock.advanceTimeBy(OrreryTokens.Motion.Delay.rise + Move.RISE.millis.toLong() / 2)

            val midway = sheetPose()
            assertTrue("the sheet is part way down its unfold", midway.scaleY > 0.5f && midway.scaleY < 1f)
            assertTrue(
                "the sheet is part way up from its rest place",
                midway.offsetY > SheetPose.FOLDED.offsetY && midway.offsetY < SheetPose.REST.offsetY,
            )
            assertTrue(
                "the sheet's top lies at or below the band's bottom",
                node(PHONE_SHEET_TAG).boundsInRoot.top >= node(SHEET_BAND_TAG).boundsInRoot.bottom,
            )

            composeRule.mainClock.advanceTimeBy(Move.RISE.millis.toLong() + OrreryTokens.Motion.Delay.rise)

            assertEquals("the sheet rests at full height", SheetPose.REST, sheetPose())
            assertEquals("the band is opaque at rest", 1f, sheetFade(SHEET_BAND_TAG), 0f)
        }
    }

    @Test
    fun paneMotionFoldsTheSheetUpIntoTheBandOnClose() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()
            settleSheet()
            assertEquals("the sheet rests before it closes", SheetPose.REST, sheetPose())

            composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).performClick()
            val closed = clockNow()
            composeRule.mainClock.advanceTimeBy(Move.FOLD.millis.toLong() / 2)

            val midway = sheetPose()
            assertTrue("the sheet is part way folded", midway.scaleY > 0.5f && midway.scaleY < 1f)
            assertEquals("the sheet is still solid half way through its fold", 1f, midway.alpha, 0f)

            // Past 70% of the fold the sheet is fading; 90% of the way through it is part way transparent.
            stepFramesUntil(closed + Move.FOLD.millis * 9 / 10)

            val fading = sheetPose()
            assertTrue("the sheet is part way transparent", fading.alpha > 0f && fading.alpha < 1f)

            composeRule.mainClock.advanceTimeBy(Move.FOLD.millis.toLong())

            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(0)
            composeRule.onAllNodesWithTag(SHEET_BAND_TAG, useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun paneMotionSwitchFadesOneBodyIntoAnotherWhileTheSheetStays() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            val epicRow = epic(EPIC_ID, EPIC_SLUG, steering = ORRERY_ID)
            val tide = design(
                DESIGN_ID,
                DESIGN_TITLE,
                ORRERY_ID,
                contracts = listOf(designContract(CONTRACT_ID, epic = EPIC_ID)),
            )
            servePane(server)
            serveWork(server, designs = listOf(tide), epics = listOf(epicRow))
            setPane(server)
            awaitTag(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
            awaitTag(CONTRACT_ROW_TAG)
            composeRule.mainClock.autoAdvance = false
            settleSheet()

            composeRule.onNodeWithTag(CONTRACT_ROW_TAG, useUnmergedTree = true).performScrollTo().performClick()
            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong() / 2)

            composeRule.onNodeWithTag(DESIGN_BODY_TAG, useUnmergedTree = true).assertExists()
            composeRule.onNodeWithTag(CONTRACT_BODY_TAG, useUnmergedTree = true).assertExists()
            assertTrue("the design's body is fading out", sheetFade(DESIGN_BODY_TAG) < 1f)
            assertTrue("the contract's body is fading in", sheetFade(CONTRACT_BODY_TAG) > 0f)
            assertEquals("the sheet stays at rest through the switch", SheetPose.REST, sheetPose())

            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong())

            composeRule.onAllNodesWithTag(DESIGN_BODY_TAG, useUnmergedTree = true).assertCountEquals(0)
            composeRule.onNodeWithTag(CONTRACT_BODY_TAG, useUnmergedTree = true).assertExists()
            assertEquals("the contract's body is opaque after the fade", 1f, sheetFade(CONTRACT_BODY_TAG), 0f)
        }
    }

    @Test
    fun paneMotionReducedMotionSwapsTheSheetWithNoIntermediateFrame() {
        setAnimatorDurationScale(0f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()

            composeRule.mainClock.advanceTimeByFrame()

            assertEquals("the sheet is at rest one frame after the tap", SheetPose.REST, sheetPose())
            assertEquals("the band is opaque one frame after the tap", 1f, sheetFade(SHEET_BAND_TAG), 0f)

            composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).performClick()
            composeRule.mainClock.advanceTimeByFrame()

            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun paneMotionRiseBolderSheetHoldsFoldedThroughTheRiseDelay() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()
            val opened = clockNow()

            assertEquals("the folded sheet is 0.15 of its height", 0.15f, SheetPose.FOLDED.scaleY, 0f)
            assertEquals("the folded sheet is one rise up", -OrreryTokens.Motion.Distance.rise, SheetPose.FOLDED.offsetY)
            assertEquals("the folded sheet is transparent", 0f, SheetPose.FOLDED.alpha, 0f)

            // Every frame from the tap is held folded and transparent until the sheet shows, which is past the rise delay.
            val shown = stepUntilSheetShows(opened)
            assertTrue("the sheet shows only once the rise delay has passed", shown - opened > OrreryTokens.Motion.Delay.rise)
        }
    }

    @Test
    fun paneMotionRiseBolderSheetUnfoldsOverTheRiseAndIsSolidBy30PercentOfIt() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()
            val shown = stepUntilSheetShows(clockNow())

            // The fade is counted from the frame the sheet first shows, which is the rise delay's end within a frame.
            stepFramesUntil(shown + Move.RISE.millis * 3 / 10)
            assertEquals("the sheet is solid 30% of its rise on", 1f, sheetPose().alpha, 0f)

            stepFramesUntil(shown + Move.RISE.millis / 2)
            val midway = sheetPose()
            assertTrue("the sheet is part way down its rise", midway.scaleY > 0.15f && midway.scaleY < 1f)
            assertTrue(
                "the sheet is part way up from its folded place",
                midway.offsetY > SheetPose.FOLDED.offsetY && midway.offsetY < SheetPose.REST.offsetY,
            )

            settleSheet()
            assertEquals("the sheet rests at full height", SheetPose.REST, sheetPose())
        }
    }

    @Test
    fun paneMotionRiseBolderSheetFoldsOverTheFoldAndStaysSolidUntil70PercentOfIt() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()
            settleSheet()
            assertEquals("the sheet rests before it closes", SheetPose.REST, sheetPose())

            composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).performClick()
            val closed = clockNow()

            // Until 70% of the fold, the sheet is solid at every frame.
            stepFramesUntil(closed + Move.FOLD.millis * 7 / 10) {
                assertEquals("the sheet is solid ${it - closed} ms into its fold", 1f, sheetPose().alpha, 0f)
            }

            // Then it fades as it folds, and it is drawn until the fold is done.
            stepFramesUntil(closed + Move.FOLD.millis * 9 / 10) {
                assertTrue("the sheet is drawn ${it - closed} ms into its fold", sheetDrawn())
            }
            val fading = sheetPose()
            assertTrue("the sheet is part way transparent", fading.alpha > 0f && fading.alpha < 1f)
            assertTrue("the sheet is part way folded", fading.scaleY < 1f)

            stepFramesUntil(closed + Move.FOLD.millis) {
                assertTrue("the sheet is drawn ${it - closed} ms into its fold", sheetDrawn())
            }
            stepFramesUntil(closed + Move.FOLD.millis * 2L)
            assertFalse("the sheet is gone once its fold is done", sheetDrawn())
        }
    }

    @Test
    fun paneMotionRiseBolderReducedMotionHasNoIntermediateFrameAtEitherEnd() {
        setAnimatorDurationScale(0f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()
            val opened = clockNow()

            // Through the window the rise would take, the sheet is at rest and opaque in every frame: the open is one step.
            stepFramesUntil(opened + OrreryTokens.Motion.Delay.rise + Move.RISE.millis) {
                assertEquals("the sheet is at rest ${it - opened} ms after the tap", SheetPose.REST, sheetPose())
            }

            composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).performClick()
            val closed = clockNow()

            // The close is one step as well: no frame of the fold is drawn at any pose in between.
            stepFramesUntil(closed + Move.FOLD.millis) {
                assertFalse("the sheet is gone ${it - closed} ms after the close", sheetDrawn())
            }
        }
    }
}
