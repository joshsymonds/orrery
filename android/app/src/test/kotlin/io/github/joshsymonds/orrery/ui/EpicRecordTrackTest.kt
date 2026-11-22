package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// 1_700_000_000 is 22:13:20 in UTC, the zone these tests run in, so an accepted contract at BASE reads 22:13.
private const val BASE = 1_700_000_000L

private val STAGE_ORDER = listOf("Contract", "Build", "Review", "Release", "Report")

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicRecordTrackTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @Before
    fun useUtc() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun aBuildingEpicsTrackDrawsItsFiveStagesInOrderWithItsCheckNowAndAhead() {
        val stages = trackOf(
            detail(
                epic = epic(outcome = "built"),
                contract = contract(accepted = BASE),
                buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
            ),
        )

        drawTrack(stages)

        assertStageOrder()
        assertStage("Contract", "done", "✓ Contract", "accepted 22:13")
        assertStage("Build", "now", "Build", "1 of 2 tasks done")
        assertStage("Review", "ahead", "Review", "not reached")
        assertStage("Release", "ahead", "Release", "not reached")
        assertStage("Report", "ahead", "Report", "not reached")
        assertOneStageIsNow()
        composeRule.onNodeWithTag("track-stage:Build").assertHasNoClickAction()
    }

    @Test
    fun aPlanningEpicsTrackSaysTheContractIsAcceptedAndThePlannerIsOutlining() {
        val stages = trackOf(detail(epic = epic(outcome = "accepted"), contract = contract(accepted = 0L)))

        drawTrack(stages)

        assertStageOrder()
        assertStage("Contract", "done", "✓ Contract", "accepted")
        assertStage("Build", "now", "Build", "the planner is outlining tasks")
        assertStage("Review", "ahead", "Review", "not reached")
        assertStage("Release", "ahead", "Release", "not reached")
        assertStage("Report", "ahead", "Report", "not reached")
        assertOneStageIsNow()
    }

    @Test
    fun aReleasedEpicsTrackHasEveryStageDoneBeforeReportAndNoAheadStage() {
        val stages = trackOf(
            detail(
                epic = epic(outcome = "released"),
                contract = contract(accepted = BASE),
                buildTasks = listOf(buildTask("example-task-a", "integrated")),
            ),
        )

        drawTrack(stages)

        assertStageOrder()
        assertStage("Contract", "done", "✓ Contract", "accepted 22:13")
        assertStage("Build", "done", "✓ Build", "1 of 1 tasks done")
        assertStage("Review", "done", "✓ Review", "clean")
        assertStage("Release", "done", "✓ Release", "released")
        assertStage("Report", "now", "Report", "released")
        assertOneStageIsNow()
    }

    @Test
    fun aNowStageIsColouredByItsToneAndTheAheadStagesAreInk4() {
        val stages = trackOf(
            detail(
                epic = epic(outcome = "built"),
                contract = contract(accepted = BASE),
                buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
            ),
        )

        drawTrack(stages)

        assertEquals(listOf(OrreryTokens.Role.live, OrreryTokens.Role.live), coloursOf("Build"))
        assertEquals(listOf(OrreryTokens.Ink.four, OrreryTokens.Ink.four), coloursOf("Review"))
        assertEquals(listOf(OrreryTokens.Ink.four, OrreryTokens.Ink.four), coloursOf("Report"))
    }

    @Test
    fun aLimitedBuildIsGreyAndDoesNotSayItWaitsOnYou() {
        val stages = trackOf(
            detail(
                epic = epic(outcome = "built"),
                contract = contract(accepted = BASE),
                buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
                steps = listOf(step(limited = true)),
            ),
        )

        drawTrack(stages)

        composeRule.onNodeWithTag("track-stage:Build")
            .assertTextEquals("Build", "1 of 2 tasks done · waiting on a limit")
        assertEquals(listOf(OrreryTokens.Ink.three, OrreryTokens.Ink.three), coloursOf("Build"))
    }

    @Test
    fun everyWordToneDrawsItsNowStageInItsColour() {
        val tones = listOf(
            WordTone.LIVE to OrreryTokens.Role.live,
            WordTone.ATTENTION to OrreryTokens.Role.attention,
            WordTone.QUIET to OrreryTokens.Ink.three,
            WordTone.ENGINE to OrreryTokens.Role.engine,
            WordTone.OK to OrreryTokens.Role.ok,
            WordTone.BAD to OrreryTokens.Role.bad,
        )
        val stages = tones.map { (tone, _) -> Stage(tone.name, StageState.NOW, tone, tone.name.lowercase()) }

        drawTrack(stages)

        tones.forEach { (tone, colour) ->
            assertEquals(listOf(colour, colour), coloursOf(tone.name))
        }
    }

    private fun drawTrack(stages: List<Stage>) {
        composeRule.setContent {
            MaterialTheme { Track(stages) }
        }
    }

    private fun assertStageOrder() {
        val tags = composeRule.onNodeWithTag("track").fetchSemanticsNode().children
            .mapNotNull { it.config.getOrNull(SemanticsProperties.TestTag) }
        assertEquals(STAGE_ORDER.map { "track-stage:$it" }, tags)
    }

    private fun assertStage(name: String, state: String, label: String, words: String) {
        composeRule.onNodeWithTag("track-stage:$name")
            .assertTextEquals(label, words)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state))
    }

    private fun assertOneStageIsNow() {
        composeRule.onAllNodes(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "now"))
            .assertCountEquals(1)
    }

    private fun coloursOf(name: String): List<Color> =
        composeRule.onNodeWithTag("track-stage:$name").fetchSemanticsNode()
            .config[SemanticsProperties.Text].flatMap { text -> text.spanStyles.map { it.item.color } }

    private fun epic(outcome: String = "", source: String = "orreryd", phaseAt: Long = 0L) = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = outcome,
        changed = BASE,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = source,
        phaseAt = phaseAt,
    )

    // A build task at the place serve computed for it, done once integrated and open before.
    private fun buildTask(id: String, place: String) = EpicBuildTaskRow(
        id = id,
        title = "",
        status = if (place == "integrated") "done" else "open",
        stage = "",
        red = null,
        green = null,
        place = place,
        tone = when (place) {
            "integrated" -> "ok"
            "planned" -> "plan"
            else -> "run"
        },
    )

    private fun contract(accepted: Long) = EpicContractRow(
        title = "Example epic title",
        approved = "",
        intent = "",
        failures = emptyList(),
        care = "serious",
        additions = emptyList(),
        premises = emptyList(),
        exclusions = emptyList(),
        approach = "",
        alternatives = emptyList(),
        releaseSteps = emptyList(),
        releaseThen = "",
        checks = "",
        base = "",
        accepted = accepted,
    )

    private fun step(limited: Boolean = false) = StepRow(
        id = "example-step-1",
        task = "example-task-a",
        agent = "example-agent",
        end = "",
        status = "working",
        held = false,
        started = BASE,
        ended = 0L,
        doing = "Example doing.",
        checks = emptyList(),
        reason = "",
        limited = limited,
    )

    private fun detail(
        epic: EpicRow,
        contract: EpicContractRow? = null,
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
    ) = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        steps = steps,
        requirements = emptyList(),
        buildTasks = buildTasks,
        summary = "",
        contract = contract,
        questions = emptyList(),
        paused = false,
        parked = "",
        plannerParked = false,
    )
}
