package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val HEAD_TAG = "head"
private const val TITLE_TAG = "head-title"
private const val METERS_TAG = "head-meters"
private const val BAND_TAG = "band"
private const val BAND_GOAL_TAG = "band-goal"
private const val BAND_HIDE_TAG = "band-hide"
private const val COMPACT_TAG = "compact-head"
private const val ASKING_TAG = "compact-asking"
private const val MORE_TAG = "head-more"
private const val MENU_HIDE_TAG = "head-menu-hide"
private const val RUN_LINE_TAG = "band-line:epic-one"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PhoneHeadViewTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val stats = listOf(
        HostStat("cpu", "42%", 42.0, "ok"),
        HostStat("disk", "71%", 71.0, "warn"),
    )
    private val lines = listOf(
        AskLine(epicId = "epic-one", slug = "rota-export", text = "asks: Bump the embedded tz database?", way = AskWay.ANSWER),
    )
    private val badges = listOf(
        HeadBadge(key = "epic-one", label = "rota", count = 0, asks = true),
        HeadBadge(key = "agents", label = "agents", count = 2, asks = false),
    )

    private var openedEpics = mutableListOf<String>()
    private var hides = 0
    private var shows = 0

    private fun setHead(fold: HeadFold, asking: String = "") {
        composeRule.setContent {
            MaterialTheme {
                Head(
                    dir = "tidepool",
                    host = "halyard",
                    title = "Tide table cache warm-up",
                    context = null,
                    note = "",
                    stats = stats,
                    otherHost = null,
                    goal = "Every port’s tide table is cached through December 17.",
                    lines = lines,
                    asking = asking,
                    badges = badges,
                    fold = fold,
                    onBack = {},
                    onOpenEpic = { openedEpics += it },
                    onHide = { hides++ },
                    onShow = { shows++ },
                )
            }
        }
    }

    private fun stateIs(state: String) = SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state)

    @Test
    fun openDrawsTheTitleMetersAndBandAndNoCompactLine() {
        setHead(HeadFold.OPEN)

        composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("open"))
        composeRule.onNodeWithTag(TITLE_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(METERS_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(BAND_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(COMPACT_TAG).assertCountEquals(0)
    }

    @Test
    fun readingDrawsTheTitleAndCompactLineAndNoBandOrMeters() {
        setHead(HeadFold.READING)

        composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("reading"))
        composeRule.onNodeWithTag(TITLE_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(BAND_TAG).assertCountEquals(0)
        composeRule.onAllNodesWithTag(BAND_GOAL_TAG).assertCountEquals(0)
        composeRule.onAllNodesWithTag(METERS_TAG).assertCountEquals(0)
    }

    @Test
    fun hiddenDrawsTheTitleAndCompactLineAndNoBandOrMeters() {
        setHead(HeadFold.HIDDEN)

        composeRule.onNodeWithTag(HEAD_TAG).assert(stateIs("hidden"))
        composeRule.onNodeWithTag(TITLE_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(COMPACT_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(BAND_TAG).assertCountEquals(0)
        composeRule.onAllNodesWithTag(METERS_TAG).assertCountEquals(0)
    }

    @Test
    fun theFoldedLineNamesTheAskingWords() {
        setHead(HeadFold.READING, asking = "rota-export asks")

        composeRule.onNodeWithTag(ASKING_TAG, useUnmergedTree = true).assertTextEquals("rota-export asks")
    }

    @Test
    fun tappingTheCompactLineCallsOnShowOnce() {
        setHead(HeadFold.HIDDEN)

        composeRule.onNodeWithTag(COMPACT_TAG).performClick()

        assertEquals(1, shows)
    }

    @Test
    fun moreHideTheHeaderCallsOnHide() {
        setHead(HeadFold.OPEN)

        composeRule.onNodeWithTag(MORE_TAG).performClick()
        composeRule.onNodeWithTag(MENU_HIDE_TAG).performClick()

        assertEquals(1, hides)
    }

    @Test
    fun bandHideCallsOnHide() {
        setHead(HeadFold.OPEN)

        composeRule.onNodeWithTag(BAND_HIDE_TAG).performClick()

        assertEquals(1, hides)
    }

    @Test
    fun aBandLineCallsOnOpenEpicWithItsId() {
        setHead(HeadFold.OPEN)

        composeRule.onNodeWithTag(RUN_LINE_TAG).performClick()

        assertEquals(listOf("epic-one"), openedEpics)
    }
}
