package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// A conversation's work links on the phone: the links workLinksOf names from synthetic rows, the title row's one link and
// "+N", the taps, the lit link, and the sheet of its designs, read through the semantics tree alone at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class WorkLinkTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val CONVO = "session:conv-1"
        const val OTHER_CONVO = "session:conv-2"
        const val TIDE_DESIGN = "design-tide"
        const val BLANK_DESIGN = "design-blank"
        const val TIDE_EPIC = "lighthouse/tide-epic"
        const val LOOSE_EPIC = "lighthouse/loose-export"
        const val BEACON_EPIC = "lighthouse/beacon-epic"
        const val LAMP_CONTRACT = "lighthouse/lamp-wiring"
        const val BEACON_CONTRACT = "lighthouse/beacon"
        const val REPOSITORY = "/home/user/lighthouse"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val opened = mutableListOf<WorkLink>()
    private val openedKeys = mutableListOf<String>()
    private var allCalls = 0

    private fun epic(id: String) = EpicRow(
        id = id,
        repository = REPOSITORY,
        slug = id.substringAfter('/'),
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        phaseAt = NOW - 49 * 60,
        steering = CONVO,
    )

    private fun epicNode(id: String) =
        EpicNode(epic(id), EpicDetail(epic(id), emptyList(), emptyList(), emptyList()), null)

    private fun contract(id: String, epic: String = "") = DesignContractRow(
        id = id,
        title = "",
        repository = REPOSITORY,
        state = "proposed",
        position = 1L,
        after = emptyList(),
        epic = epic,
        epicPhase = "",
    )

    private fun design(
        id: String,
        title: String,
        conversation: String,
        ofOne: Boolean = false,
        contracts: List<DesignContractRow> = emptyList(),
    ) = DesignRow(
        id = id,
        title = title,
        conversation = conversation,
        created = 0L,
        ofOne = ofOne,
        state = "working",
        contracts = contracts,
    )

    // The conversation's designs in an order that is not the links' order: a design of one first, a design of another
    // conversation between, and a blank-titled design last.
    private val lampOfOne = design(
        "design-lamp", "Lamp", CONVO, ofOne = true,
        contracts = listOf(contract(LAMP_CONTRACT)),
    )
    private val tide = design(
        TIDE_DESIGN, "Tide alerts", CONVO,
        contracts = listOf(contract("lighthouse/tide-alerts", epic = TIDE_EPIC)),
    )
    private val otherDesign = design("design-other", "Elsewhere", OTHER_CONVO)
    private val beaconOfOne = design(
        "design-beacon", "Beacon", CONVO, ofOne = true,
        contracts = listOf(contract(BEACON_CONTRACT, epic = BEACON_EPIC)),
    )
    private val blank = design(BLANK_DESIGN, " ", CONVO)
    private val allDesigns = listOf(lampOfOne, tide, otherDesign, beaconOfOne, blank)
    // The tide epic is carried by the tide design's contract, so only the loose epic is a link of its own.
    private val steered = listOf(epic(TIDE_EPIC), epic(LOOSE_EPIC))

    private val designLink = WorkLink("design:$TIDE_DESIGN", WorkKind.DESIGN, TIDE_DESIGN, "Tide alerts")
    private val blankLink = WorkLink("design:$BLANK_DESIGN", WorkKind.DESIGN, BLANK_DESIGN, BLANK_DESIGN)
    private val lampLink = WorkLink("contract:$LAMP_CONTRACT", WorkKind.CONTRACT, LAMP_CONTRACT, "lamp-wiring")
    private val beaconLink = WorkLink("epic:$BEACON_EPIC", WorkKind.EPIC, BEACON_EPIC, "beacon")
    private val looseLink = WorkLink("epic:$LOOSE_EPIC", WorkKind.EPIC, LOOSE_EPIC, "loose-export")

    @Test
    fun theLinksAreTheDesignsThenTheDesignsOfOneThenTheSteeredEpicsNoDesignCarries() {
        assertEquals(
            listOf(designLink, blankLink, lampLink, beaconLink, looseLink),
            workLinksOf(CONVO, allDesigns, steered),
        )
    }

    @Test
    fun anEmptyConversationIdNamesNoLinks() {
        val withBlankConversation = allDesigns + design("design-orphan", "Orphan", conversation = "")

        assertEquals(emptyList<WorkLink>(), workLinksOf("", withBlankConversation, steered))
    }

    private fun setRow(work: List<WorkLink>, lit: String? = null) {
        composeRule.setContent {
            MaterialTheme {
                HeadTitleRow(
                    dir = "lighthouse",
                    title = "Tide table cache warm-up",
                    context = null,
                    note = "",
                    onBack = {},
                    onMore = {},
                    moreOpen = false,
                    onDismissMore = {},
                    onHideHeader = {},
                    work = work,
                    workLit = lit,
                    onOpenWork = { opened.add(it) },
                    onAllWork = { allCalls++ },
                )
            }
        }
    }

    @Test
    fun noLinkDrawsNoWorkLinksNodeAndTheRowStillDrawsItsProject() {
        setRow(emptyList())

        composeRule.onNodeWithTag("head-dir").assertTextEquals("lighthouse")
        composeRule.onAllNodesWithTag("work-links").assertCountEquals(0)
    }

    @Test
    fun oneLinkDrawsInsideTheTitleRowAsItsNameAlone() {
        setRow(listOf(designLink))

        composeRule.onNodeWithTag("work-link:${designLink.key}").assertTextEquals("Tide alerts")
        composeRule.onNode(
            hasTestTag("work-link:${designLink.key}") and hasAnyAncestor(hasTestTag("head-title")),
            useUnmergedTree = true,
        ).assertExists()
        composeRule.onAllNodesWithTag("work-link-all").assertCountEquals(0)
    }

    @Test
    fun threeLinksDrawTheFirstAndPlus2() {
        setRow(listOf(designLink, lampLink, beaconLink))

        composeRule.onNodeWithTag("work-link:${designLink.key}").assertTextEquals("Tide alerts")
        composeRule.onAllNodesWithTag("work-link:${lampLink.key}").assertCountEquals(0)
        composeRule.onAllNodesWithTag("work-link:${beaconLink.key}").assertCountEquals(0)
        composeRule.onNodeWithTag("work-link-all").assertTextEquals("+2")
    }

    @Test
    fun tappingTheFirstLinkCallsOnOpenWorkAndTheMoreCallsOnAllWork() {
        setRow(listOf(designLink, lampLink, beaconLink))

        composeRule.onNodeWithTag("work-link:${designLink.key}").performClick()
        composeRule.onNodeWithTag("work-link-all").performClick()

        assertEquals(listOf(designLink), opened)
        assertEquals(1, allCalls)
    }

    @Test
    fun theLitLinkIsSelectedAndTheOthersAreNot() {
        setRow(listOf(designLink, lampLink, beaconLink), lit = designLink.key)

        composeRule.onNodeWithTag("work-link:${designLink.key}").assertIsSelected()
        composeRule.onNodeWithTag("work-link-all").assertIsNotSelected()
    }

    @Test
    fun thePlusNLinkIsSelectedWhenTheListOfAllIsLit() {
        setRow(listOf(designLink, lampLink, beaconLink), lit = "all")

        composeRule.onNodeWithTag("work-link-all").assertIsSelected()
        composeRule.onNodeWithTag("work-link:${designLink.key}").assertIsNotSelected()
    }

    private fun setSheet(blocks: List<DesignBlock>, loose: List<Hung>) {
        composeRule.setContent {
            MaterialTheme {
                WorkSheetBody(
                    designs = blocks,
                    loose = loose,
                    now = NOW,
                    onOpen = { openedKeys.add(it) },
                )
            }
        }
    }

    @Test
    fun theSheetDrawsEachDesignsTabAndWellAndATapOnAKidCallsOnOpenWithItsKey() {
        val tideBlock = DesignBlock(tide, kids = listOf(epicNode(TIDE_EPIC)))
        val blankBlock = DesignBlock(blank, kids = emptyList())
        setSheet(blocks = listOf(tideBlock, blankBlock), loose = emptyList())

        composeRule.onNodeWithTag("design-tab:$TIDE_DESIGN", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("design-well:$TIDE_DESIGN", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("design-tab:$BLANK_DESIGN", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("design-well:$BLANK_DESIGN", useUnmergedTree = true).assertExists()

        composeRule.onNodeWithTag("hung:epic:$TIDE_EPIC", useUnmergedTree = true).performClick()

        assertEquals(listOf("epic:$TIDE_EPIC"), openedKeys)
    }

    @Test
    fun aLooseEpicIsUnderTheLabelForAnEpicWithNoDesign() {
        setSheet(blocks = emptyList(), loose = listOf(epicNode(LOOSE_EPIC)))

        composeRule.onNodeWithTag("work-sheet-loose").assertTextEquals("an epic with no design")
        composeRule.onNodeWithTag("hung:epic:$LOOSE_EPIC", useUnmergedTree = true).assertExists()
    }
}
