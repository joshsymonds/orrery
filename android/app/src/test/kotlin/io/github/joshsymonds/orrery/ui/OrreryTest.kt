package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.click
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.min
import kotlin.math.sin
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp-xxhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class OrreryTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val orrerySize = 400.dp

    // The animator duration scale is 0 for every test, so the glow holds still and the composition goes idle.
    @Before
    fun animatorDurationScaleIsZero() {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            0f,
        )
    }

    // Two running wedges, the first in implementing and the second in checking, for the taps and the glow.
    private val twoWedges = listOf(
        wedge("T1", WedgePlace.IMPLEMENTING, WedgeTone.RUN, "now ahead ahead ahead"),
        wedge("T2", WedgePlace.CHECKING, WedgeTone.RUN, "passed now ahead ahead"),
    )

    @Test
    fun theCanvasDescribesTheIntegratedCountEachWedgeAndEachArc() {
        val wedges = listOf(
            wedge("T1", WedgePlace.INTEGRATED, WedgeTone.OK, "passed passed passed passed"),
            wedge("T2", WedgePlace.IMPLEMENTING, WedgeTone.RUN, "now ahead ahead ahead"),
            wedge("T3", WedgePlace.IMPLEMENTING, WedgeTone.MINE, "now ahead ahead ahead"),
            wedge("T4", WedgePlace.IMPLEMENTING, WedgeTone.BAD, "now ahead ahead ahead"),
            wedge("T5", WedgePlace.INTEGRATING, WedgeTone.ACC, "passed passed passed now"),
            wedge("T6", WedgePlace.PLANNED, WedgeTone.PLAN, "ahead ahead ahead ahead"),
        )
        val arcs = listOf(
            RequirementArc("R1", 0.0, 2.0, proven = true),
            RequirementArc("R2", 2.0, 3.0, proven = false),
        )
        composeRule.setContent { OrreryUnderTest(wedges, arcs) }

        assertEquals(
            listOf(
                "1 of 6 integrated",
                "T1: integrated, integrated",
                "T2: implementing, running",
                "T3: implementing, waits on you",
                "T4: implementing, ended short",
                "T5: integrating, accepted",
                "T6: planned, planned",
                "R1 proven",
                "R2 open",
            ),
            contentDescription(),
        )
    }

    @Test
    fun exactlyOneSliceGlowsForEachRunningWedgeAndNoneForTheOtherTones() {
        val wedges = listOf(
            wedge("T1", WedgePlace.CHECKING, WedgeTone.RUN, "passed now ahead ahead"),
            wedge("T2", WedgePlace.IMPLEMENTING, WedgeTone.RUN, "now ahead ahead ahead"),
            wedge("T3", WedgePlace.IMPLEMENTING, WedgeTone.MINE, "now ahead ahead ahead"),
            wedge("T4", WedgePlace.IMPLEMENTING, WedgeTone.BAD, "now ahead ahead ahead"),
            wedge("T5", WedgePlace.INTEGRATING, WedgeTone.ACC, "passed passed passed now"),
            wedge("T6", WedgePlace.INTEGRATED, WedgeTone.OK, "passed passed passed passed"),
        )
        composeRule.setContent { OrreryUnderTest(wedges, emptyList()) }

        assertEquals("glows: T1 checking, T2 implementing", stateDescription())
    }

    @Test
    fun noRunningWedgeMeansNothingGlows() {
        val wedges = listOf(
            wedge("T1", WedgePlace.PLANNED, WedgeTone.PLAN, "ahead ahead ahead ahead"),
            wedge("T2", WedgePlace.IMPLEMENTING, WedgeTone.MINE, "now ahead ahead ahead"),
        )
        composeRule.setContent { OrreryUnderTest(wedges, emptyList()) }

        assertEquals("glows: none", stateDescription())
    }

    @Test
    fun aNowSliceDrawsInItsWedgesToneColour() {
        val wedges = listOf(
            wedge("T1", WedgePlace.IMPLEMENTING, WedgeTone.RUN, "now ahead ahead ahead"),
            wedge("T2", WedgePlace.CHECKING, WedgeTone.MINE, "passed now ahead ahead"),
            wedge("T3", WedgePlace.REVIEWING, WedgeTone.BAD, "passed passed now ahead"),
            wedge("T4", WedgePlace.INTEGRATING, WedgeTone.ACC, "passed passed passed now"),
        )
        composeRule.setContent { OrreryUnderTest(wedges, emptyList()) }

        // Each now slice is the cell on its phase's ring in its wedge.
        assertEquals("running", OrreryTokens.Tone.run, cellColour("T1", Phase.IMPLEMENTING))
        assertEquals("waits on you", OrreryTokens.Tone.mine, cellColour("T2", Phase.CHECKING))
        assertEquals("ended short", OrreryTokens.Tone.bad, cellColour("T3", Phase.REVIEWING))
        assertEquals("accepted", OrreryTokens.Tone.acc, cellColour("T4", Phase.INTEGRATING))
    }

    @Test
    fun aTapInAPhaseCellOpensThatTasksPhase() {
        val calls = mutableListOf<Pair<String, Phase>>()
        composeRule.setContent {
            OrreryUnderTest(twoWedges, emptyList(), onOpenPhase = { task, phase -> calls += task to phase })
        }

        tap(touch(RINGS[1], middle(0, 2)))
        Phase.entries.indices.forEach { phase -> tap(touch(RINGS[phase + 1], middle(1, 2))) }

        assertEquals(listOf("T1" to Phase.IMPLEMENTING) + Phase.entries.map { "T2" to it }, calls)
    }

    @Test
    fun aTapOnTheRimOpensNothing() {
        val calls = mutableListOf<Pair<String, Phase>>()
        composeRule.setContent {
            OrreryUnderTest(twoWedges, emptyList(), onOpenPhase = { task, phase -> calls += task to phase })
        }

        tap(touch(RINGS[0], middle(0, 2)))
        tap(touch(RINGS[0], middle(1, 2)))

        assertEquals(emptyList<Pair<String, Phase>>(), calls)
    }

    @Test
    fun atAnimatorScaleZeroTheGlowShowsAndHoldsInsteadOfBreathing() {
        composeRule.mainClock.autoAdvance = false
        composeRule.setContent { OrreryUnderTest(twoWedges, emptyList()) }

        assertEquals("glows: T1 implementing, T2 checking", stateDescription())
        assertEquals(false, glowBreathes())
    }

    @Test
    fun withAnimationsOnTheGlowBreathes() {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            1f,
        )
        composeRule.mainClock.autoAdvance = false
        composeRule.setContent { OrreryUnderTest(twoWedges, emptyList()) }

        assertEquals(true, glowBreathes())
    }

    @Composable
    private fun OrreryUnderTest(
        wedges: List<Wedge>,
        arcs: List<RequirementArc>,
        onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
    ) {
        Orrery(wedges, arcs, modifier = Modifier.size(orrerySize), onOpenPhase = onOpenPhase)
    }

    // A wedge of [task] at [place] and [tone], whose four phases are [states] in order: each passed, now or ahead.
    private fun wedge(task: String, place: WedgePlace, tone: WedgeTone, states: String): Wedge = Wedge(
        task = task,
        title = task,
        requirements = emptyList(),
        place = place,
        tone = tone,
        slices = Phase.entries.zip(states.split(" ")) { phase, state ->
            PhaseSlice(phase, SliceState.valueOf(state.uppercase()), summary = "", since = 0L, seconds = 0L)
        },
    )

    private fun tap(position: Offset) {
        composeRule.onNodeWithTag("orrery").performTouchInput { click(position) }
    }

    private fun contentDescription(): List<String>? =
        composeRule.onNodeWithTag("orrery").fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.ContentDescription)

    private fun stateDescription(): String? =
        composeRule.onNodeWithTag("orrery").fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.StateDescription)

    // The point [radius] units from the chart's centre at [degrees] clockwise from 12 o'clock, in the coordinates a touch
    // on the chart is sent in.
    private fun touch(radius: Float, degrees: Float): Offset {
        val size = composeRule.onNodeWithTag("orrery").fetchSemanticsNode().size
        val unit = min(size.width, size.height) / BOX
        val angle = degrees * PI.toFloat() / 180f
        return Offset(size.width / 2f + radius * unit * sin(angle), size.height / 2f - radius * unit * cos(angle))
    }

    // The colour the chart fills [task]'s cell on [phase]'s ring with, as the chart's node carries its cells.
    private fun cellColour(task: String, phase: Phase): Color =
        composeRule.onNodeWithTag("orrery").fetchSemanticsNode().config[OrreryCellsKey]
            .single { it.task == task && it.ring == phase.ordinal + 1 }
            .colour

    private fun glowBreathes(): Boolean =
        composeRule.onNodeWithTag("orrery").fetchSemanticsNode().config[OrreryGlowBreathesKey]

    // The middle angle of slice [index] of [count], from the spec: 8 degrees at the start and 344 degrees shared out.
    private fun middle(index: Int, count: Int): Float = START + (index + 0.5f) * SWEEP / count

    private companion object {
        const val BOX = 412f
        const val START = 8f
        const val SWEEP = 344f

        // The ring centre radii in units, the rim first and then one ring per phase inward.
        val RINGS = listOf(168f, 151f, 134f, 117f, 100f)
    }
}
