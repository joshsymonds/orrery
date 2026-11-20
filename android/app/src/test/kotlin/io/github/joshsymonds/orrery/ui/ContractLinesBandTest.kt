package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val BAND_TAG = "band"
private const val BAND_GOAL_TAG = "band-goal"
private const val BAND_MORE_TAG = "band-more"
private const val BAND_LINE_PREFIX = "band-line:"
private const val BAND_CONTRACT_PREFIX = "band-contract:"
private const val HEAD_TAG = "head"
private const val READ_AND_DECIDE = "Read and decide ›"
private const val OPEN_ON_THE_RIGHT = "open on the right ›"

private val tide = ContractBandLine(
    id = "tidepool/tide-alerts",
    slug = "tide-alerts",
    text = "1 decision is yours; read “can’t be undone” before Accept",
)
private val rota = ContractBandLine(id = "tidepool/rota-export", slug = "rota-export", text = "2 decisions are yours")
private val harbour = ContractBandLine(id = "tidepool/harbour", slug = "harbour", text = "nothing left to settle")
private val orbit = ContractBandLine(id = "tidepool/orbit", slug = "orbit", text = "1 decision is yours")

private val epicLines = listOf(
    AskLine(epicId = "epic-one", slug = "notices-usable", text = "is parked: R5 is already met", way = AskWay.DECIDE),
    AskLine(epicId = "epic-two", slug = "orbit-labels", text = "release step 2 failed: CI red", way = AskWay.OPEN),
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractLinesBandTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setBand(
        goal: String,
        contracts: List<ContractBandLine>,
        lines: List<AskLine> = emptyList(),
        hereContract: String = "",
        onOpenContract: (String) -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                Band(
                    goal = goal,
                    lines = lines,
                    onOpen = {},
                    onHide = null,
                    contracts = contracts,
                    hereContract = hereContract,
                    onOpenContract = onOpenContract,
                )
            }
        }
    }

    /** The band's direct children's test tags, in the order they are drawn, for the contract and epic lines only. */
    private fun askTagsInOrder(): List<String> = composeRule.onNodeWithTag(BAND_TAG, useUnmergedTree = true)
        .fetchSemanticsNode()
        .children
        .mapNotNull { it.config.getOrNull(SemanticsProperties.TestTag) }
        .filter { it.startsWith(BAND_CONTRACT_PREFIX) || it.startsWith(BAND_LINE_PREFIX) }

    @Test
    fun contractLineDrawsItsNameTextAndReadAndDecide() {
        setBand("synthetic goal", listOf(tide))

        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + tide.id).assertIsDisplayed()
        composeRule.onNodeWithText("tide-alerts contract", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText(tide.text, useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText(READ_AND_DECIDE, useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun contractLineWithNoGoalStillDrawsTheBand() {
        setBand("", listOf(tide))

        composeRule.onNodeWithTag(BAND_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + tide.id).assertIsDisplayed()
        composeRule.onAllNodesWithTag(BAND_GOAL_TAG).assertCountEquals(0)
    }

    @Test
    fun theContractOpenOnTheRightSaysSoAndOffersNoWayIn() {
        setBand("synthetic goal", listOf(tide), hereContract = tide.id)

        composeRule.onNodeWithText(OPEN_ON_THE_RIGHT, useUnmergedTree = true).assertIsDisplayed()
        composeRule.onAllNodesWithText(READ_AND_DECIDE, useUnmergedTree = true).assertCountEquals(0)
        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + tide.id).assertHasNoClickAction()
    }

    @Test
    fun aContractNotOpenOnTheRightOffersTheWayIn() {
        setBand("synthetic goal", listOf(tide), hereContract = rota.id)

        composeRule.onNodeWithText(READ_AND_DECIDE, useUnmergedTree = true).assertIsDisplayed()
        composeRule.onAllNodesWithText(OPEN_ON_THE_RIGHT, useUnmergedTree = true).assertCountEquals(0)
        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + tide.id).assertHasClickAction()
    }

    @Test
    fun aPressCallsOnOpenContractWithItsId() {
        val opened = mutableListOf<String>()
        setBand("synthetic goal", listOf(tide, rota), onOpenContract = { opened += it })

        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + rota.id).performClick()

        assertEquals(listOf(rota.id), opened)
    }

    @Test
    fun contractLinesComeBeforeEpicLines() {
        setBand("synthetic goal", listOf(tide, rota), lines = epicLines)

        assertEquals(
            listOf(
                BAND_CONTRACT_PREFIX + tide.id,
                BAND_CONTRACT_PREFIX + rota.id,
                BAND_LINE_PREFIX + "epic-one",
            ),
            askTagsInOrder(),
        )
    }

    @Test
    fun atMostThreeLinesShowInAllWithTheRestUnderMore() {
        setBand("synthetic goal", listOf(tide, rota, harbour, orbit), lines = epicLines)

        assertEquals(
            listOf(BAND_CONTRACT_PREFIX + tide.id, BAND_CONTRACT_PREFIX + rota.id, BAND_CONTRACT_PREFIX + harbour.id),
            askTagsInOrder(),
        )
        composeRule.onAllNodesWithTag(BAND_CONTRACT_PREFIX + orbit.id).assertCountEquals(0)
        composeRule.onAllNodesWithTag(BAND_LINE_PREFIX + "epic-one").assertCountEquals(0)
        composeRule.onNodeWithTag(BAND_MORE_TAG).assertIsDisplayed()
        composeRule.onNodeWithText("and 3 more ask", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText(
            "orbit contract, notices-usable, orbit-labels",
            useUnmergedTree = true,
        ).assertIsDisplayed()
    }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HeadContractLinesTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    @Test
    fun headPassesTheContractLinesThroughToTheBand() {
        val opened = mutableListOf<String>()
        composeRule.setContent {
            MaterialTheme {
                Head(
                    dir = "tidepool",
                    host = "halyard",
                    title = "Tide table cache warm-up",
                    context = null,
                    note = "",
                    stats = emptyList(),
                    otherHost = null,
                    goal = "Every port’s tide table is cached through December 17.",
                    lines = epicLines.take(1),
                    asking = "",
                    badges = emptyList(),
                    fold = HeadFold.OPEN,
                    onBack = {},
                    onOpenEpic = {},
                    onHide = {},
                    onShow = {},
                    contracts = listOf(tide),
                    hereContract = rota.id,
                    onOpenContract = { opened += it },
                )
            }
        }

        composeRule.onNodeWithTag(HEAD_TAG).assertIsDisplayed()
        composeRule.onNodeWithText("tide-alerts contract", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithTag(BAND_CONTRACT_PREFIX + tide.id).performClick()

        assertEquals(listOf(tide.id), opened)
    }
}
