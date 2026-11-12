package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicsGroupTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val REPO = "example-repo"
        const val SLUG = "example-epic"
        const val SLUG_TWO = "example-epic-two"
        const val LIVE_ID = "$REPO/$SLUG"
        const val LIVE_TWO_ID = "$REPO/$SLUG_TWO"
        const val FOLDED_ID = "other-repo/folded-epic"
        const val FOLDED_TWO_ID = "other-repo/folded-epic-two"
    }

    @get:Rule
    val composeRule = createComposeRule()

    private val opened = mutableListOf<String>()

    private fun step(
        id: String,
        held: Boolean = false,
        agent: String = "agent-$id",
        task: String = "task-$id",
        doing: String = "Doing synthetic work $id.",
    ): StepRow = StepRow(
        id = id,
        task = task,
        agent = agent,
        end = "",
        status = "running",
        held = held,
        started = NOW - 60,
        ended = 0,
        doing = doing,
        checks = emptyList(),
        reason = "",
    )

    private fun epic(
        id: String,
        repository: String,
        slug: String,
        live: Boolean,
        running: List<StepRow> = emptyList(),
        ended: Int = 0,
    ): EpicRow = EpicRow(
        id = id,
        repository = repository,
        slug = slug,
        outcome = "",
        changed = NOW,
        live = live,
        runningSteps = running,
        endedSteps = ended,
        problem = "",
    )

    private fun setGroup(epics: List<EpicRow>) {
        composeRule.setContent {
            EpicsGroup(epics = epics, onOpenEpic = { opened.add(it) })
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun hasTagPrefix(prefix: String): SemanticsMatcher =
        SemanticsMatcher("test tag starts with $prefix") {
            it.config.contains(SemanticsProperties.TestTag) &&
                it.config[SemanticsProperties.TestTag].startsWith(prefix)
        }

    @Test
    fun liveEpicShowsSlugRepositoryAndItsRunningSteps() {
        setGroup(
            listOf(
                epic(
                    LIVE_ID,
                    REPO,
                    SLUG,
                    live = true,
                    running = listOf(step("s1", agent = "synthetic-agent", task = "synthetic-task", doing = "Writing the synthetic module.")),
                ),
            ),
        )

        composeRule.onNodeWithText(SLUG).assertIsDisplayed()
        composeRule.onNodeWithText(REPO).assertIsDisplayed()
        composeRule.onNodeWithText("synthetic-agent").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic-task").assertIsDisplayed()
        composeRule.onNodeWithText("Writing the synthetic module.").assertIsDisplayed()
        assertEquals(1, countNodes(hasTestTag("step:s1")))
    }

    @Test
    fun atMostFourRunningStepRowsShowWithAMoreLineForTheRest() {
        val running = (1..6).map { step("s$it") }
        setGroup(listOf(epic(LIVE_ID, REPO, SLUG, live = true, running = running)))

        assertEquals(4, countNodes(hasTagPrefix("step:")))
        for (n in 1..4) assertEquals(1, countNodes(hasTestTag("step:s$n")))
        for (n in 5..6) assertEquals(0, countNodes(hasTestTag("step:s$n")))
        composeRule.onNodeWithText("2 more").assertIsDisplayed()
    }

    @Test
    fun heldStepIsMarkedHeldAndRunningStepIsNot() {
        setGroup(
            listOf(
                epic(
                    LIVE_ID,
                    REPO,
                    SLUG,
                    live = true,
                    running = listOf(step("s1", held = true), step("s2", held = false)),
                ),
            ),
        )

        assertEquals(1, countNodes(hasTestTag("held:s1")))
        assertEquals(0, countNodes(hasTestTag("held:s2")))
        assertEquals(1, countNodes(hasText("held")))
    }

    @Test
    fun endedStepsShowOnlyAsOneCountLineNeverAsRows() {
        setGroup(listOf(epic(LIVE_ID, REPO, SLUG, live = true, running = emptyList(), ended = 3)))

        composeRule.onNodeWithText("3 ended").assertIsDisplayed()
        assertEquals(0, countNodes(hasTagPrefix("step:")))
    }

    @Test
    fun foldedEpicsFoldIntoOneLineThatListsThemWhenOpened() {
        setGroup(
            listOf(
                epic(LIVE_ID, REPO, SLUG, live = true),
                epic(FOLDED_ID, "other-repo", "folded-epic", live = false),
                epic(FOLDED_TWO_ID, "other-repo", "folded-epic-two", live = false),
            ),
        )

        composeRule.onNodeWithText("2 folded").assertIsDisplayed()
        assertEquals(0, countNodes(hasTestTag("epic:$FOLDED_ID")))
        assertEquals(0, countNodes(hasTestTag("epic:$FOLDED_TWO_ID")))

        composeRule.onNodeWithText("2 folded").performClick()

        assertEquals(1, countNodes(hasTestTag("epic:$FOLDED_ID")))
        assertEquals(1, countNodes(hasTestTag("epic:$FOLDED_TWO_ID")))
        assertEquals(1, countNodes(hasTestTag("epic:$LIVE_ID")))
    }

    @Test
    fun tappingAnEpicStepOrFoldLineCallsOnOpenEpicWithTheEpicId() {
        setGroup(
            listOf(
                epic(
                    LIVE_ID,
                    REPO,
                    SLUG,
                    live = true,
                    running = (1..5).map { step("s$it") },
                ),
                epic(FOLDED_ID, "other-repo", "folded-epic", live = false),
            ),
        )

        composeRule.onNodeWithTag("epic:$LIVE_ID").performClick()
        assertEquals(listOf(LIVE_ID), opened)

        composeRule.onNodeWithTag("step:s1").performClick()
        assertEquals(listOf(LIVE_ID, LIVE_ID), opened)

        composeRule.onNodeWithText("1 more").performClick()
        assertEquals(listOf(LIVE_ID, LIVE_ID, LIVE_ID), opened)

        composeRule.onNodeWithText("1 folded").performClick()
        composeRule.onNodeWithTag("epic:$FOLDED_ID").performClick()
        assertEquals(listOf(LIVE_ID, LIVE_ID, LIVE_ID, FOLDED_ID), opened)
    }

    @Test
    fun noEpicsShowsNoGroup() {
        setGroup(emptyList())

        assertEquals(0, countNodes(hasTestTag("epics-group")))
    }
}
