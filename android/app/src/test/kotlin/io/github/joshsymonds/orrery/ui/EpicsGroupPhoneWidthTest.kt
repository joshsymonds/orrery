package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasText
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTheme
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val NOW = 1_700_000_000L
private const val REPO = "pane-tree-wireframe-0a1b2c3"
private const val SLUG = "orrery-style-a2b-glance"
private const val ID = "$REPO/$SLUG"

/**
 * Home's epic lines at a phone's width, with long real content: a slug, its repository, a two-digit count, and an
 * ending's outcome and time, in the app's own monospace type. Each word stays whole on its own line rather than one
 * squeezed to a sliver beside the others.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w411dp-h891dp")
// Real text measurement, so a word squeezed by its neighbours shows as more than one line.
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class EpicsGroupPhoneWidthTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun aLiveLineKeepsItsSlugRepositoryAndCountWhole() {
        val live = epic(live = true, outcome = "", phaseAt = NOW - 2_940L)
        val proven = List(12) { requirement("R$it", "green") }
        val open = List(2) { requirement("R${12 + it}", "red") }
        val detail = EpicDetail(
            epic = live,
            tasks = emptyList(),
            slices = emptyList(),
            steps = emptyList(),
            requirements = proven + open,
            buildTasks = listOf(EpicBuildTaskRow(id = "T1", title = "", status = "open", stage = "", red = null, green = null)),
            questions = listOf(EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = NOW - 120L)),
        )
        setGroup(listOf(live), mapOf(ID to detail))

        assertEquals("the slug's lines", 1, lineCountOf(SLUG))
        assertEquals("the repository's lines", 1, lineCountOf(REPO))
        assertEquals("the count's lines", 1, lineCountOf("12 of 14 proven"))
    }

    @Test
    fun aJustEndedLineKeepsItsSlugRepositoryAndEndingWhole() {
        setGroup(listOf(epic(live = false, outcome = "released", phaseAt = NOW - 720L)), emptyMap())

        assertEquals("the slug's lines", 1, lineCountOf(SLUG))
        assertEquals("the repository's lines", 1, lineCountOf(REPO))
        assertEquals("the ending's lines", 1, lineCountOf("released · 12 min ago"))
    }

    private fun setGroup(epics: List<EpicRow>, details: Map<String, EpicDetail>) {
        composeRule.setContent {
            OrreryTheme { EpicsGroup(epics = epics, details = details, now = NOW, onOpenEpic = {}) }
        }
    }

    private fun epic(live: Boolean, outcome: String, phaseAt: Long): EpicRow = EpicRow(
        id = ID,
        repository = REPO,
        slug = SLUG,
        outcome = outcome,
        changed = NOW,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = "orreryd",
        phaseAt = phaseAt,
    )

    private fun requirement(id: String, phase: String) = EpicRequirementRow(
        id = id,
        text = "",
        evidence = "",
        kind = "check",
        runs = listOf(
            EvidenceRunRow(
                id = "run-$id",
                phase = phase,
                source = "captured",
                state = "ended",
                exit = 0,
                timedOut = false,
                notCaptured = "",
                verdict = "",
                judgeReason = "",
                passed = true,
                revision = "",
                created = 0L,
                ended = 0L,
                logBytes = 0L,
                stderrBytes = 0L,
                artifacts = emptyList(),
            ),
        ),
    )

    /** The number of lines the text node reading exactly [text] lays out on. */
    private fun lineCountOf(text: String): Int {
        val node = composeRule.onNode(hasText(text), useUnmergedTree = true).fetchSemanticsNode()
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action) {
            "no text layout on that node"
        }.invoke(layouts)
        return layouts.single().lineCount
    }
}
