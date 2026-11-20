package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
class EpicCommandsTest {
    private companion object {
        const val SLUG = "example-epic"
        const val STEERING = "example-session"
        const val STOP_LINE = "Its running steps are stopped. Nothing is released."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The callbacks [EpicFoot] makes, each counted. */
    private class Calls {
        val talk = AtomicInteger()
        val pause = AtomicInteger()
        val resume = AtomicInteger()
        val stop = AtomicInteger()
    }

    /**
     * Taps [tag] through its click action. A touch injected into the sheet's window does not reach the sheet's content
     * under Robolectric, so a tap inside a sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Waits until the sheet tagged [tag] has left the tree: a closed sheet animates out before it is removed. */
    private fun awaitGone(tag: String) {
        composeRule.awaitUntil { composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isEmpty() }
    }

    private fun setFoot(
        steering: String = STEERING,
        canPause: Boolean = true,
        canResume: Boolean = true,
        canStop: Boolean = true,
        calls: Calls = Calls(),
    ) {
        composeRule.setContent {
            MaterialTheme {
                EpicFoot(
                    slug = SLUG,
                    steering = steering,
                    onTalk = { calls.talk.incrementAndGet() },
                    canPause = canPause,
                    canResume = canResume,
                    canStop = canStop,
                    onPause = { calls.pause.incrementAndGet() },
                    onResume = { calls.resume.incrementAndGet() },
                    onStop = { calls.stop.incrementAndGet() },
                )
            }
        }
    }

    @Test
    fun epicCommandsTalkIsShownWhenASteererIsKnown() {
        setFoot(steering = STEERING)

        composeRule.onNodeWithTag("epic-talk-bar")
            .assertTextEquals("Talk to the session that steers it")
    }

    @Test
    fun epicCommandsTalkIsNotShownWithoutASteerer() {
        setFoot(steering = "")

        composeRule.onAllNodesWithTag("epic-talk-bar").assertCountEquals(0)
    }

    @Test
    fun epicCommandsMoreIsNotDrawnWhenNoCommandIsAllowed() {
        setFoot(canPause = false, canResume = false, canStop = false)

        composeRule.onAllNodesWithTag("epic-more").assertCountEquals(0)
    }

    @Test
    fun epicCommandsMoreIsDrawnWhenACommandIsAllowed() {
        setFoot(canPause = false, canResume = false, canStop = true)

        composeRule.onNodeWithTag("epic-more").assertTextEquals("More")
    }

    @Test
    fun epicCommandsMoreOpensASheetTitledWithTheSlugListingTheAllowedCommands() {
        setFoot(canPause = true, canResume = false, canStop = true)

        tap("epic-more")

        composeRule.onNodeWithTag("epic-more-title").assertTextEquals(SLUG)
        composeRule.onNodeWithTag("epic-sheet-pause").assertTextEquals("Pause")
        composeRule.onAllNodesWithTag("epic-sheet-resume").assertCountEquals(0)
        composeRule.onNodeWithTag("epic-sheet-stop").assertTextEquals("Stop…")
    }

    @Test
    fun epicCommandsResumeIsListedOnlyWhenAllowed() {
        setFoot(canPause = false, canResume = true, canStop = false)

        tap("epic-more")

        composeRule.onNodeWithTag("epic-sheet-resume").assertTextEquals("Resume")
        composeRule.onAllNodesWithTag("epic-sheet-pause").assertCountEquals(0)
        composeRule.onAllNodesWithTag("epic-sheet-stop").assertCountEquals(0)
    }

    @Test
    fun epicCommandsPauseCallsOnceAndClosesTheSheet() {
        val calls = Calls()
        setFoot(calls = calls)
        tap("epic-more")

        tap("epic-sheet-pause")

        assertEquals(1, calls.pause.get())
        assertEquals(0, calls.resume.get())
        assertEquals(0, calls.stop.get())
        awaitGone("epic-more-title")
    }

    @Test
    fun epicCommandsResumeCallsOnceAndClosesTheSheet() {
        val calls = Calls()
        setFoot(canPause = false, canResume = true, canStop = false, calls = calls)
        tap("epic-more")

        tap("epic-sheet-resume")

        assertEquals(1, calls.resume.get())
        assertEquals(0, calls.pause.get())
        assertEquals(0, calls.stop.get())
        awaitGone("epic-more-title")
    }

    @Test
    fun epicCommandsStopOpensTheConfirmWithItsExactWordsAndSendsNothingYet() {
        val calls = Calls()
        setFoot(calls = calls)
        tap("epic-more")

        tap("epic-sheet-stop")

        awaitGone("epic-more-title")
        composeRule.onNodeWithTag("epic-stop-title").assertTextEquals("Stop $SLUG?")
        composeRule.onAllNodesWithText(STOP_LINE).assertCountEquals(1)
        composeRule.onNodeWithTag("epic-stop-confirm").assertTextEquals("Stop $SLUG")
        composeRule.onNodeWithTag("epic-stop-keep").assertTextEquals("Keep it running")
        assertEquals(0, calls.stop.get())
    }

    @Test
    fun epicCommandsKeepItRunningClosesTheConfirmAndSendsNothing() {
        val calls = Calls()
        setFoot(calls = calls)
        tap("epic-more")
        tap("epic-sheet-stop")

        tap("epic-stop-keep")

        assertEquals(0, calls.stop.get())
        awaitGone("epic-stop-title")
        composeRule.onAllNodesWithTag("epic-more").assertCountEquals(1)
    }

    @Test
    fun epicCommandsConfirmCallsOnStopExactlyOnce() {
        val calls = Calls()
        setFoot(calls = calls)
        tap("epic-more")
        tap("epic-sheet-stop")

        tap("epic-stop-confirm")

        assertEquals(1, calls.stop.get())
        assertEquals(0, calls.pause.get())
        assertEquals(0, calls.resume.get())
        awaitGone("epic-stop-title")
        awaitGone("epic-stop-confirm")
    }
}
