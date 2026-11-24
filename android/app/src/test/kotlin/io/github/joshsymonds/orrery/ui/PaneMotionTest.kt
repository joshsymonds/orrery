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
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.Move
import org.junit.Assert.assertEquals
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
        const val EPIC_BODY_TAG = "sheet-body:epic:$EPIC_ID"
        const val KID_TAG = "hung:epic:$EPIC_ID"
        const val PANE_WATCH_PATH = "/orrery.v1.Panes/WatchConversation"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
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

    /** The designs, epics and contracts the pane's head reads: [designs] and [epics] as the server lists them. */
    private fun serveWork(server: FakeOrreryServer, designs: List<Design>, epics: List<Epic>) {
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addAllDesigns(designs).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addAllEpics(epics).build())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    /** Serves [epic] as its detail reads it, with no history, so its screen draws the epic's summary. */
    private fun serveEpicDetail(server: FakeOrreryServer, epic: Epic) {
        server.respond(EPICS_GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).setRepository(REPOSITORY).build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
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

    /** Advances the clock past the longest move, so a move that began at the last tap has ended. */
    private fun settleSheet() {
        composeRule.mainClock.advanceTimeBy(Move.DRILL.millis.toLong() * 2)
    }

    /** The node tagged [tag] in the unmerged tree, as it is drawn now. */
    private fun node(tag: String): SemanticsNode =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode()

    /** The pose the phone sheet's node is at now. */
    private fun sheetPose(): SheetPose = node(PHONE_SHEET_TAG).config[SheetPoseKey]

    /** The alpha the node tagged [tag] is at now: the band's, or a body's. */
    private fun sheetFade(tag: String): Float = node(tag).config[SheetFadeKey]

    @Test
    fun paneMotionUnfoldsTheSheetDownFromTheBandAndRestsAtFullHeight() {
        setAnimatorDurationScale(1f)
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            openDesignFromItsLink()

            composeRule.mainClock.advanceTimeBy(Move.DRILL.millis.toLong() / 2)

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

            composeRule.mainClock.advanceTimeBy(Move.DRILL.millis.toLong())

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
            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong() / 2)

            val midway = sheetPose()
            assertTrue("the sheet is part way folded", midway.scaleY > 0.5f && midway.scaleY < 1f)
            assertTrue("the sheet is part way transparent", midway.alpha < 1f)

            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong())

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
            serveEpicDetail(server, epicRow)
            setPane(server)
            awaitTag(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
            awaitTag(KID_TAG)
            composeRule.mainClock.autoAdvance = false
            settleSheet()

            composeRule.onNodeWithTag(KID_TAG, useUnmergedTree = true).performScrollTo().performClick()
            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong() / 2)

            composeRule.onNodeWithTag(DESIGN_BODY_TAG, useUnmergedTree = true).assertExists()
            composeRule.onNodeWithTag(EPIC_BODY_TAG, useUnmergedTree = true).assertExists()
            assertTrue("the design's body is fading out", sheetFade(DESIGN_BODY_TAG) < 1f)
            assertTrue("the epic's body is fading in", sheetFade(EPIC_BODY_TAG) > 0f)
            assertEquals("the sheet stays at rest through the switch", SheetPose.REST, sheetPose())

            composeRule.mainClock.advanceTimeBy(Move.LEAVE.millis.toLong())

            composeRule.onAllNodesWithTag(DESIGN_BODY_TAG, useUnmergedTree = true).assertCountEquals(0)
            composeRule.onNodeWithTag(EPIC_BODY_TAG, useUnmergedTree = true).assertExists()
            assertEquals("the epic's body is opaque after the fade", 1f, sheetFade(EPIC_BODY_TAG), 0f)
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
}
