package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.Mocha
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
        const val WAITING_SLUG = "waiting-epic"
        const val WAITING_TWO_SLUG = "waiting-epic-two"
        const val WAITING_ID = "other-repo/$WAITING_SLUG"
        const val WAITING_TWO_ID = "other-repo/$WAITING_TWO_SLUG"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

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
        waiting: Boolean = false,
        phaseAt: Long = 0L,
    ): EpicRow = EpicRow(
        id = id,
        repository = repository,
        slug = slug,
        outcome = "",
        changed = NOW,
        live = live,
        waiting = waiting,
        runningSteps = running,
        endedSteps = ended,
        problem = "",
        phaseAt = phaseAt,
    )

    private fun evidenceRun(phase: String, passed: Boolean) = EvidenceRunRow(
        id = "run-$phase",
        phase = phase,
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "",
        judgeReason = "",
        passed = passed,
        revision = "",
        created = 0L,
        ended = 0L,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )

    private fun requirement(id: String, vararg runs: EvidenceRunRow) = EpicRequirementRow(
        id = id,
        text = "",
        evidence = "",
        kind = "check",
        runs = runs.toList(),
    )

    private fun buildTask(id: String) = EpicBuildTaskRow(
        id = id,
        title = "",
        status = "open",
        stage = "",
        red = null,
        green = null,
    )

    private fun question(asked: Long) = EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = asked)

    private fun detail(
        epic: EpicRow,
        requirements: List<EpicRequirementRow> = emptyList(),
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        requirements = requirements,
        buildTasks = buildTasks,
        questions = questions,
    )

    private fun setGroup(epics: List<EpicRow>, details: Map<String, EpicDetail> = emptyMap()) {
        composeRule.setContent {
            EpicsGroup(epics = epics, details = details, now = NOW, onOpenEpic = { opened.add(it) })
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
    fun waitingLiveEpicsFoldIntoOneLineThatOpensToTheirNames() {
        setGroup(
            listOf(
                epic(LIVE_ID, REPO, SLUG, live = true),
                epic(WAITING_ID, "other-repo", WAITING_SLUG, live = true, waiting = true),
                epic(WAITING_TWO_ID, "other-repo", WAITING_TWO_SLUG, live = true, waiting = true),
            ),
        )

        composeRule.onNodeWithText("2 accepted, waiting to start").assertIsDisplayed()
        assertEquals(0, countNodes(hasText(WAITING_SLUG)))
        assertEquals(0, countNodes(hasText(WAITING_TWO_SLUG)))
        assertEquals(0, countNodes(hasTestTag("epic:$WAITING_ID")))
        assertEquals(0, countNodes(hasTestTag("epic:$WAITING_TWO_ID")))
        assertEquals(1, countNodes(hasTestTag("epic:$LIVE_ID")))

        composeRule.onNodeWithText("2 accepted, waiting to start").performClick()

        composeRule.onNodeWithText(WAITING_SLUG).assertIsDisplayed()
        composeRule.onNodeWithText(WAITING_TWO_SLUG).assertIsDisplayed()
        assertEquals(1, countNodes(hasTestTag("epic:$WAITING_ID")))
        assertEquals(1, countNodes(hasTestTag("epic:$WAITING_TWO_ID")))
        assertEquals(1, countNodes(hasTestTag("epic:$LIVE_ID")))

        composeRule.onNodeWithTag("epic:$WAITING_TWO_ID").performClick()
        assertEquals(listOf(WAITING_TWO_ID), opened)
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

    @Test
    fun liveEpicWithDetailShowsItsCountStateBlockerAndSmallOrrery() {
        val live = epic(LIVE_ID, REPO, SLUG, live = true, phaseAt = NOW - 2_940L)
        setGroup(
            listOf(live),
            details = mapOf(
                LIVE_ID to detail(
                    live,
                    requirements = listOf(
                        requirement("R1", evidenceRun("green", true)),
                        requirement("R2", evidenceRun("green", true)),
                        requirement("R3", evidenceRun("red", true)),
                    ),
                    buildTasks = listOf(buildTask("T1")),
                    questions = listOf(question(asked = NOW - 120L)),
                ),
            ),
        )

        composeRule.onNodeWithText(SLUG).assertIsDisplayed()
        composeRule.onNodeWithText("2 of 3 proven").assertIsDisplayed()
        composeRule.onNodeWithText("building · 49 min · 0 of 1 units integrated").assertIsDisplayed()
        composeRule.onNodeWithText("waits on your answer · 2 min").assertIsDisplayed()
        assertEquals(1, countNodes(hasTestTag("mini-orrery:$LIVE_ID")))
    }

    @Test
    fun anOpenQuestionShowsItsBlockerWhateverElseTheLineShows() {
        val first = epic(LIVE_ID, REPO, SLUG, live = true, running = (1..6).map { step("s$it") })
        val second = epic(LIVE_TWO_ID, REPO, SLUG_TWO, live = true)
        setGroup(
            listOf(first, second),
            details = mapOf(
                LIVE_ID to detail(first, questions = listOf(question(asked = NOW - 120L))),
                LIVE_TWO_ID to detail(second, questions = listOf(question(asked = 0L))),
            ),
        )

        composeRule.onNodeWithText("2 more").assertIsDisplayed()
        composeRule.onNodeWithText("waits on your answer · 2 min").assertIsDisplayed()
        composeRule.onNodeWithText("waits on your answer").assertIsDisplayed()
    }

    @Test
    fun aLiveEpicWithoutDetailShowsEpicUnavailableAndNoOrrery() {
        setGroup(listOf(epic(LIVE_ID, REPO, SLUG, live = true)))

        composeRule.onNodeWithText(SLUG).assertIsDisplayed()
        composeRule.onNodeWithText("epic unavailable").assertIsDisplayed()
        assertEquals(0, countNodes(hasTagPrefix("mini-orrery:")))
    }

    @Test
    fun aBlockerIsDrawnInPeach() {
        val live = epic(LIVE_ID, REPO, SLUG, live = true)
        setGroup(
            listOf(live),
            details = mapOf(LIVE_ID to detail(live, questions = listOf(question(asked = NOW - 120L)))),
        )

        val colours = composeRule.onNodeWithTag("blocker:$LIVE_ID", useUnmergedTree = true)
            .fetchSemanticsNode().config[SemanticsProperties.Text].single().spanStyles.map { it.item.color }
        assertEquals(listOf(Mocha.Peach), colours)
    }

    @Test
    fun anEpicWithNothingToWaitOnShowsNoBlockerLine() {
        val live = epic(LIVE_ID, REPO, SLUG, live = true)
        setGroup(listOf(live), details = mapOf(LIVE_ID to detail(live)))

        composeRule.onNodeWithText("0 of 0 tasks done").assertIsDisplayed()
        assertEquals(0, countNodes(hasTestTag("blocker:$LIVE_ID")))
    }
}
