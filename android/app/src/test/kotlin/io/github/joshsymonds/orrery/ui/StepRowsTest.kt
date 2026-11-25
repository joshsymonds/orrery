package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The running steps of an epic as rows, both in the inbox and on the epic page's Now, from synthetic steps at a synthetic
// clock. Each step is drawn twice in one composition: "step:<id>" is its inbox row, "step-row:<id>" its Now row. Queries
// are scoped to one row and read the unmerged tree, since a clickable row merges its children.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class StepRowsTest {
    private companion object {
        const val NOW = 100_000L
        const val EPIC_ID = "lighthouse/rota-export"
        const val CONVO_KEY = "session:s1"
        const val REPO = "/home/user/lighthouse"
        const val AGENT = "builder"
        const val PURPOSE = "Refuse an export over an existing file"
        const val DOING = "Writing the refusal test."
        const val SENTENCE = "$PURPOSE running: $DOING"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun paneOf() = Pane(
        id = "s1",
        host = "laptop",
        session = "",
        window = 0,
        pane = 0,
        cwd = REPO,
        command = "",
        state = "working",
        summary = "",
        label = "Lamp",
        changed = NOW - 120,
        seen = 0L,
        question = "",
        questionHash = "",
        newestReply = 0L,
        headless = true,
        orreryId = "conv-1",
    )

    private fun step(
        id: String,
        purpose: String = "",
        task: String = "",
        role: String = "",
        agent: String = "",
        doing: String = "Example doing.",
    ) = StepRow(
        id = id,
        task = task,
        agent = agent,
        end = "",
        status = "working",
        held = false,
        started = 0L,
        ended = 0L,
        doing = doing,
        checks = emptyList(),
        reason = "",
        role = role,
        purpose = purpose,
    )

    private fun epic(steps: List<StepRow>) = EpicRow(
        id = EPIC_ID,
        repository = REPO,
        slug = "rota-export",
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = steps,
        endedSteps = 0,
        phaseAt = NOW - 49 * 60,
    )

    // A history line for one step, as the epic's Just happened draws it. Time 0 draws no clock, so the line's text is
    // its sentence alone.
    private fun stepEntry(row: StepRow) = EpicHistoryRow(
        time = 0L,
        cursor = row.id,
        kind = "step",
        sentence = SENTENCE,
        step = row,
    )

    // The steps in one composition: the epic's inbox row under its conversation, then the epic's Now rows, then the
    // epic's Just happened lines when there are any.
    private fun setRows(steps: List<StepRow>, history: List<EpicHistoryRow> = emptyList()) {
        val epic = epic(steps)
        val detail = EpicDetail(
            epic = epic,
            steps = steps,
            questions = emptyList(),
        )
        val convo = Convo(
            key = CONVO_KEY,
            pane = paneOf(),
            hung = listOf(EpicNode(epic, detail, contract = null)),
            folded = emptyList(),
        )
        composeRule.setContent {
            MaterialTheme {
                Column {
                    ConvoRowItem(convo = convo, now = NOW, foldOpen = false, onFold = {}, onOpen = {}, onLongPress = {})
                    StepRows(lines = stepLinesOf(detail), tasks = emptyList(), now = NOW)
                    if (history.isNotEmpty()) {
                        JustHappened(entries = history, steps = steps, onOpenStep = {})
                    }
                }
            }
        }
    }

    private fun countIn(row: String, matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher and hasAnyAncestor(hasTestTag(row)), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countAnywhere(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun topOf(row: String, text: String): Float =
        composeRule.onNode(hasText(text) and hasAnyAncestor(hasTestTag(row)), useUnmergedTree = true)
            .fetchSemanticsNode().positionInRoot.y

    @Test
    fun theStepRowsReadPurposeThenDoingInTheInboxAndOnNow() {
        setRows(listOf(step("s1", purpose = "Example purpose.", task = "example-task", doing = "Example doing.")))

        for (row in listOf("step:s1", "step-row:s1")) {
            assertEquals("$row shows its purpose", 1, countIn(row, hasText("Example purpose.")))
            assertEquals("$row shows its doing", 1, countIn(row, hasText("Example doing.")))
            assertEquals("$row leads with the purpose, not the task", 0, countIn(row, hasText("example-task")))
            assertTrue(
                "$row reads its purpose above its doing",
                topOf(row, "Example purpose.") < topOf(row, "Example doing."),
            )
        }
    }

    @Test
    fun theStepRowsReadTheTaskThenTheRoleWhenAStepHasNoPurpose() {
        setRows(
            listOf(
                step("s1", task = "example-task", role = "example-role"),
                step("s2", role = "example-role"),
            ),
        )

        for (row in listOf("step:s1", "step-row:s1")) {
            assertEquals("$row reads its task", 1, countIn(row, hasText("example-task")))
            assertEquals("$row reads its task over its role", 0, countIn(row, hasText("example-role")))
        }
        for (row in listOf("step:s2", "step-row:s2")) {
            assertEquals("$row reads its role when it has no task", 1, countIn(row, hasText("example-role")))
        }
    }

    @Test
    fun theStepRowsNameNoAgentInTheInboxOrOnNowWhateverTheStep() {
        setRows(
            listOf(
                step("s1", task = "example-task", agent = "example-agent"),
                step("s2", role = "example-role", agent = "example-agent"),
                step("s3", agent = "example-agent"),
            ),
        )

        for (row in listOf("step:s1", "step:s2", "step:s3", "step-row:s1", "step-row:s2", "step-row:s3")) {
            assertEquals("$row names no agent", 0, countIn(row, hasText("example-agent")))
        }
    }

    @Test
    fun theStepRowsDrawPipsOnlyForAStepWithATaskInTheInboxAndOnNow() {
        setRows(
            listOf(
                step("s1", purpose = "Example purpose.", task = "example-task"),
                step("s2", purpose = "Example purpose two.", role = "example-role"),
            ),
        )

        for (row in listOf("step:s1", "step-row:s1")) {
            assertEquals("$row draws the pips of its task", 1, countIn(row, hasTestTag("pips")))
        }
        for (row in listOf("step:s2", "step-row:s2")) {
            assertEquals("$row draws no pips without a task", 0, countIn(row, hasTestTag("pips")))
        }
    }

    @Test
    fun theStepRowsHistoryLineReadsPurposeThenDoing() {
        val running = step("s1", purpose = PURPOSE, agent = AGENT, doing = DOING)
        setRows(listOf(running), history = listOf(stepEntry(running)))

        composeRule.onNodeWithTag("happened:0").assertTextEquals("", SENTENCE)
        val drawn = composeRule.onNodeWithTag("happened:0").fetchSemanticsNode().config[SemanticsProperties.Text]
            .joinToString("") { it.text }
        val purposeAt = drawn.indexOf(PURPOSE)
        assertTrue("the line reads its purpose before its doing", purposeAt in 0 until drawn.indexOf(DOING))
    }

    @Test
    fun theStepRowsShowTheAgentOnlyInTheStepDetail() {
        val running = step("s1", purpose = PURPOSE, agent = AGENT, doing = DOING)
        setRows(listOf(running), history = listOf(stepEntry(running)))

        assertEquals("no line names the agent before the tap", 0, countAnywhere(hasText(AGENT, substring = true)))
        assertEquals("no detail shows the agent before the tap", 0, countAnywhere(hasTestTag("happened-agent:0")))

        composeRule.onNodeWithTag("happened:0").performClick()

        composeRule.onNodeWithTag("happened-agent:0", useUnmergedTree = true).assertTextEquals("agent: $AGENT")
        for (row in listOf("step:s1", "step-row:s1")) {
            assertEquals("$row names no agent", 0, countIn(row, hasText(AGENT, substring = true)))
        }
    }
}
