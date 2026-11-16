package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.fadeIn
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasText
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.spec
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicsGroupMotionTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val REPO = "example-repo"
        const val SLUG = "example-epic"
        const val LIVE_ID = "$REPO/$SLUG"
        const val ANSWER_PHRASE = "waits on your answer · 2 min"
        const val PARK_PHRASE = "parked · synthetic park"
        const val PLANNING_PHRASE = "planning"
        const val PAUSED_PHRASE = "paused"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun aChangedBlockerPhraseCrossfadesAtMoveStateAndSettles() {
        setAnimatorDurationScale(1f)
        val detail = showLine(detailOf(questions = listOf(question())))
        composeRule.mainClock.autoAdvance = false

        composeRule.runOnIdle { detail.value = detailOf(parked = "synthetic park") }
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeBy(Move.STATE.millis / 2L)
        composeRule.waitForIdle()
        assertEquals("the old blocker phrase is still fading out", 1, countText(ANSWER_PHRASE))
        assertEquals("the new blocker phrase is fading in", 1, countText(PARK_PHRASE))

        composeRule.mainClock.advanceTimeBy(2L * Move.STATE.millis)
        composeRule.waitForIdle()
        assertEquals("the old blocker phrase is gone once settled", 0, countText(ANSWER_PHRASE))
        assertEquals("the new blocker phrase stays once settled", 1, countText(PARK_PHRASE))
    }

    @Test
    fun aChangedStatePhraseCrossfadesAtMoveStateAndSettles() {
        setAnimatorDurationScale(1f)
        val detail = showLine(detailOf())
        composeRule.mainClock.autoAdvance = false

        composeRule.runOnIdle { detail.value = detailOf(paused = true) }
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeBy(Move.STATE.millis / 2L)
        composeRule.waitForIdle()
        assertEquals("the old state phrase is still fading out", 1, countText(PLANNING_PHRASE))
        assertEquals("the new state phrase is fading in", 1, countText(PAUSED_PHRASE))

        composeRule.mainClock.advanceTimeBy(2L * Move.STATE.millis)
        composeRule.waitForIdle()
        assertEquals("the old state phrase is gone once settled", 0, countText(PLANNING_PHRASE))
        assertEquals("the new state phrase stays once settled", 1, countText(PAUSED_PHRASE))
    }

    @Test
    fun atAnimatorScaleZeroAChangedBlockerPhraseShowsAtOnce() {
        setAnimatorDurationScale(0f)
        val detail = showLine(detailOf(questions = listOf(question())))
        composeRule.mainClock.autoAdvance = false

        composeRule.runOnIdle { detail.value = detailOf(parked = "synthetic park") }
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()

        assertEquals("the old blocker phrase is gone at once", 0, countText(ANSWER_PHRASE))
        assertEquals("the new blocker phrase shows at once", 1, countText(PARK_PHRASE))
    }

    @Test
    fun atAnimatorScaleZeroAChangedStatePhraseShowsAtOnce() {
        setAnimatorDurationScale(0f)
        val detail = showLine(detailOf())
        composeRule.mainClock.autoAdvance = false

        composeRule.runOnIdle { detail.value = detailOf(paused = true) }
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()

        assertEquals("the old state phrase is gone at once", 0, countText(PLANNING_PHRASE))
        assertEquals("the new state phrase shows at once", 1, countText(PAUSED_PHRASE))
    }

    @Test
    fun aJustEndedRowEntersWithTheLayoutFadeWhenAnimationsAreOn() {
        setAnimatorDurationScale(1f)
        val epics = showEpics(listOf(liveEpic()))

        composeRule.runOnIdle { epics.value = listOf(endedEpic()) }
        composeRule.waitForIdle()
        assertEquals(listOf(fadeIn(Move.LAYOUT.spec(animated = true))), enterTransitions())
    }

    @Test
    fun atAnimatorScaleZeroAJustEndedRowEntersWithAnInstantFade() {
        setAnimatorDurationScale(0f)
        val epics = showEpics(listOf(liveEpic()))

        composeRule.runOnIdle { epics.value = listOf(endedEpic()) }
        composeRule.waitForIdle()
        assertEquals(listOf(fadeIn(Move.LAYOUT.spec(animated = false))), enterTransitions())
    }

    @Test
    fun aNeedsYouItemEntersWithTheLayoutFadeWhenAnimationsAreOn() {
        setAnimatorDurationScale(1f)
        val details = showNeeds(emptyMap())

        composeRule.runOnIdle { details.value = mapOf(LIVE_ID to detailOf(questions = listOf(question()))) }
        composeRule.waitForIdle()
        assertEquals(listOf(fadeIn(Move.LAYOUT.spec(animated = true))), enterTransitions())
    }

    @Test
    fun atAnimatorScaleZeroANeedsYouItemEntersWithAnInstantFade() {
        setAnimatorDurationScale(0f)
        val details = showNeeds(emptyMap())

        composeRule.runOnIdle { details.value = mapOf(LIVE_ID to detailOf(questions = listOf(question()))) }
        composeRule.waitForIdle()
        assertEquals(listOf(fadeIn(Move.LAYOUT.spec(animated = false))), enterTransitions())
    }

    @Test
    fun aWaitingPaneEntersNeedsYouWithTheLayoutFade() {
        setAnimatorDurationScale(1f)
        val waiting = showWaiting(emptyList())

        composeRule.runOnIdle { waiting.value = listOf(waitingPane()) }
        composeRule.waitForIdle()
        assertEquals(listOf(fadeIn(Move.LAYOUT.spec(animated = true))), enterTransitions())
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            scale,
        )
    }

    /** Composes Home's Epics group with one live epic whose detail the test changes through the returned state. */
    private fun showLine(first: EpicDetail): MutableState<EpicDetail> {
        val detail = mutableStateOf(first)
        val epic = liveEpic()
        composeRule.setContent {
            EpicsGroup(epics = listOf(epic), details = mapOf(LIVE_ID to detail.value), now = NOW, onOpenEpic = {})
        }
        return detail
    }

    /** Composes Home's Epics group with the given epics, which the test changes through the returned state. */
    private fun showEpics(first: List<EpicRow>): MutableState<List<EpicRow>> {
        val epics = mutableStateOf(first)
        composeRule.setContent { EpicsGroup(epics = epics.value, onOpenEpic = {}, now = NOW) }
        return epics
    }

    /** Composes Home's Needs you over the live epic, whose detail the test changes through the returned state. */
    private fun showNeeds(first: Map<String, EpicDetail>): MutableState<Map<String, EpicDetail>> {
        val details = mutableStateOf(first)
        composeRule.setContent {
            NeedsYou(
                waiting = emptyList(),
                epics = listOf(liveEpic()),
                details = details.value,
                onOpenPane = {},
                onOpenEpic = {},
                onAnswer = { _, _, _ -> "" },
                onRetry = {},
                onStop = {},
            )
        }
        return details
    }

    /** Composes Home's Needs you over the given waiting panes, which the test changes through the returned state. */
    private fun showWaiting(first: List<Pane>): MutableState<List<Pane>> {
        val waiting = mutableStateOf(first)
        composeRule.setContent {
            NeedsYou(
                waiting = waiting.value,
                epics = emptyList(),
                details = emptyMap(),
                onOpenPane = {},
                onOpenEpic = {},
                onAnswer = { _, _, _ -> "" },
                onRetry = {},
                onStop = {},
            )
        }
        return waiting
    }

    private fun waitingPane(): Pane = Pane(
        id = "%1",
        host = "box",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = "/work/api",
        command = "claude",
        state = "waiting",
        summary = "",
        label = "api",
        changed = 0L,
        seen = 0L,
        question = "",
        questionHash = "",
    )

    private fun countText(text: String): Int =
        composeRule.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().size

    /** The enter transition of each row that is entering, as the row runs it. */
    private fun enterTransitions(): List<EnterTransition> =
        composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(EnterSemantics), useUnmergedTree = true)
            .fetchSemanticsNodes()
            .map { it.config[EnterSemantics] }

    private fun liveEpic(): EpicRow = EpicRow(
        id = LIVE_ID,
        repository = REPO,
        slug = SLUG,
        outcome = "",
        changed = NOW,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
    )

    private fun endedEpic(): EpicRow =
        liveEpic().copy(live = false, source = "orreryd", phaseAt = NOW - 60L, outcome = "shipped")

    private fun question(): EpicQuestionRow =
        EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = NOW - 120L)

    private fun detailOf(
        questions: List<EpicQuestionRow> = emptyList(),
        parked: String = "",
        paused: Boolean = false,
    ): EpicDetail = EpicDetail(
        epic = liveEpic(),
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        questions = questions,
        parked = parked,
        paused = paused,
    )
}
