package io.github.joshsymonds.orrery.ui

import android.os.Looper
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.ObserverHandle
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.awaitUntilAfterIdle
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val KEYS_PATH = "/orrery.v1.Panes/SendKeys"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val FLASH_MILLIS = 300L
private const val WINDOW_MILLIS = 5_000L
private const val CHANGED_AT = 1_000L
private const val LATER = 2_000L
/** The screen's read-window state class, named because it is private to QuickKeys.kt. */
private const val READ_WINDOW_CLASS = "ReadWindow"
private const val NOTHING_CHANGED = "Nothing changed."
private const val NOT_ANSWERED = "the server did not answer."
private const val REFUSED = "the server refused it."

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuickKeysTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val haptic = RecordingHaptic()

    /**
     * How many read windows the screen has opened: each is a ReadWindow written to the screen's state on the main
     * thread, in the same step that starts the window's five-second close. [awaitWindowOpened] waits on it.
     */
    private val windowsOpened = AtomicInteger()
    private var countWindows: ObserverHandle? = null

    @Before
    fun countOpenedWindows() {
        val main = Looper.getMainLooper()
        countWindows = Snapshot.registerGlobalWriteObserver { state ->
            if (Looper.myLooper() == main && (state as? State<*>)?.value?.javaClass?.simpleName == READ_WINDOW_CLASS) {
                windowsOpened.incrementAndGet()
            }
        }
    }

    @After
    fun stopCountingWindows() {
        countWindows?.dispose()
    }

    /**
     * Waits until the screen has opened [count] read windows. With the clock stopped, the window's close then runs
     * from the time the test advances from. The send's answer comes back on another thread, so the wait has no deadline.
     */
    private fun awaitWindowOpened(count: Int) {
        composeRule.awaitUntilAfterIdle { windowsOpened.get() >= count }
    }

    private val labelToKey = listOf(
        "Esc" to "Escape",
        "Ctrl-C" to "C-c",
        "Enter" to "Enter",
        "←" to "Left",
        "↑" to "Up",
        "↓" to "Down",
        "→" to "Right",
    )

    @Test
    fun rowLabelsAreTheSevenKeysInOrder() {
        FakeOrreryServer().use { server ->
            setQuickKeys(server)
            val labels = composeRule.onAllNodes(hasClickAction()).fetchSemanticsNodes().map { node ->
                node.config[SemanticsProperties.Text].joinToString("") { it.text }
            }
            assertEquals(labelToKey.map { it.first }, labels)
        }
    }

    @Test
    fun eachButtonPostsItsKeyWithoutExpectQuestion() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            labelToKey.forEachIndexed { index, (label, key) ->
                composeRule.onNodeWithText(label).performScrollTo().performClick()
                composeRule.awaitUntil { server.requests.size == index + 1 }

                val request = server.requests[index]
                assertEquals("POST", request.method)
                assertEquals(KEYS_PATH, request.rawPath)
                val decoded = SendKeysRequest.parseFrom(request.body)
                assertEquals("keys for $label", listOf(key), decoded.keysList)
                assertFalse("expect_question for $label", decoded.hasExpectQuestion())
            }
        }
    }

    @Test
    fun refusedKeyShowsItsNoteInErrorToneWithNoOkNote() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)

            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            awaitNote(NoteTone.Error, notSentNote("Enter", REFUSED))
            assertNoOkNote("a key not sent")
        }
    }

    @Test
    fun promptChangedShowsItsKeyRefusedNote() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.ABORTED, "question refused")
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            awaitNote(NoteTone.Error, notSentNote("Esc", REFUSED))
        }
    }

    @Test
    fun unansweredKeyShowsDidNotAnswerNote() {
        FakeOrreryServer().use { server ->
            server.drop(KEYS_PATH)
            setQuickKeys(server)

            composeRule.onNodeWithText("Ctrl-C").performClick()
            awaitNote(NoteTone.Error, notSentNote("Ctrl-C", NOT_ANSWERED))
            assertNoOkNote("a key not sent")
        }
    }

    @Test
    fun unavailableAnswerSaysDidNotAnswer() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.UNAVAILABLE, "orreryd is not taking keys")
            setQuickKeys(server)

            composeRule.onNodeWithText("Ctrl-C").performClick()
            awaitNote(NoteTone.Error, notSentNote("Ctrl-C", NOT_ANSWERED))
        }
    }

    @Test
    fun laterRefusedSendReplacesTheErrorNote() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            awaitNote(NoteTone.Error, notSentNote("Esc", REFUSED))
            composeRule.onNodeWithText("Ctrl-C").performClick()
            awaitNote(NoteTone.Error, notSentNote("Ctrl-C", REFUSED))
        }
    }

    @Test
    fun errorNoteStaysAcrossALaterSendInFlightAndLeavesWhenAKeyLands() {
        FakeOrreryServer().use { server ->
            // The Enter send is held until the test releases it, so the note is judged while that send is in flight.
            val release = CountDownLatch(1)
            server.route(KEYS_PATH) { request ->
                if (SendKeysRequest.parseFrom(request.body).keysList == listOf("Escape")) {
                    Reply.Failure(Code.INTERNAL_ERROR, "boom", 0L)
                } else {
                    release.await()
                    Reply.Message(SendKeysResponse.getDefaultInstance(), 0L)
                }
            }
            try {
                setQuickKeys(server)
                composeRule.onNodeWithText("Esc").performClick()
                awaitNote(NoteTone.Error, notSentNote("Esc", REFUSED))

                composeRule.onNodeWithText("Enter").performScrollTo().performClick()
                composeRule.awaitUntil("the Enter send to reach the server") { keyRequests(server) == 2 }
                composeRule.waitForIdle()
                assertEquals(listOf(notSentNote("Esc", REFUSED)), shownNotes(NoteTone.Error))

                release.countDown()
                composeRule.awaitUntil("the Esc note to leave once Enter lands") {
                    shownNotes(NoteTone.Error).isEmpty()
                }
                assertNoOkNote("the Enter that landed")
            } finally {
                release.countDown()
            }
        }
    }

    @Test
    fun tapRecordsOneLongPressHaptic() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.awaitUntil { server.requests.size == 1 }

            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun twoTapsRecordTwoHaptics() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.awaitUntil { server.requests.size == 1 }
            composeRule.onNodeWithText("Esc").performClick()
            composeRule.awaitUntil { server.requests.size == 2 }

            assertEquals(listOf(HapticFeedbackType.LongPress, HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun tappedKeyFlashesForThreeHundredMillisecondsAndNoOtherKey() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()

            assertTrue("Esc flashes right after the tap", flashing("Esc"))
            labelToKey.drop(1).forEach { (label, _) ->
                assertFalse("$label does not flash", flashing(label))
            }
            composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
            assertTrue("Esc still flashes at 299 ms", flashing("Esc"))
            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            assertFalse("Esc stops flashing at 300 ms", flashing("Esc"))
        }
    }

    @Test
    fun secondTapRestartsTheFlash() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.mainClock.advanceTimeBy(200, ignoreFrameDuration = true)
            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()

            composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
            assertTrue("second tap restarts the flash", flashing("Esc"))
            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            assertFalse("flash ends 300 ms after the second tap", flashing("Esc"))
        }
    }

    @Test
    fun failedSendStillRecordsHapticAndFlashes() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            composeRule.awaitUntilAfterIdle { server.requests.size == 1 }

            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
            assertTrue("Enter flashes although the send failed", flashing("Enter"))

            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Error, notSentNote("Enter", REFUSED))
            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun escSentThenStateMovingToDoneReadsSentAndThePaneNow() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.awaitUntil { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.state = "done" }
            awaitNote(NoteTone.Ok, sentNote("Esc", "The pane is now done."))
            assertEquals("the change shows the result without a read", 0, panesRequests(server))
        }
    }

    @Test
    fun aChangedTimeInsideTheWindowReadsThePaneWorking() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.awaitUntil { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.changed = LATER }
            awaitNote(NoteTone.Ok, sentNote("Esc", "The pane is now working."))
        }
    }

    @Test
    fun emptyStateInsideTheWindowReadsThePaneEnded() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Ctrl-C").performClick()
            composeRule.waitForIdle()
            composeRule.awaitUntil { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.state = "" }
            awaitNote(NoteTone.Ok, sentNote("Ctrl-C", "The pane is now ended."))
        }
    }

    @Test
    fun refusedSendShowsItsNoteAndOpensNoWindow() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            awaitNote(NoteTone.Error, notSentNote("Esc", REFUSED))

            composeRule.mainClock.autoAdvance = false
            composeRule.runOnIdle { shown.state = "done" }
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.waitForIdle()
            assertEquals("a refused send opens no window, so no read", 0, panesRequests(server))
            assertNoOkNote("a refused send")
            assertEquals("the failure stays shown", listOf(notSentNote("Esc", REFUSED)), shownNotes(NoteTone.Error))
        }
    }

    @Test
    fun ctrlCWithNoChangeReadsNothingChangedOnlyAfterFiveSeconds() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Ctrl-C").performClick()
            awaitWindowOpened(1)

            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS - 1, ignoreFrameDuration = true)
            settleReads()
            assertEquals("no read before 5 s", 0, panesRequests(server))
            assertTrue("no result before 5 s", shownNotes(NoteTone.Ok).isEmpty())

            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Ok, sentNote("Ctrl-C", NOTHING_CHANGED))
            assertEquals("one read at 5 s", 1, panesRequests(server))
        }
    }

    @Test
    fun unchangedReadAfterWindowWithoutInsideChangeReadsNothingChanged() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)

            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Ok, sentNote("Esc", NOTHING_CHANGED))
        }
    }

    @Test
    fun readFindingTheStateMovedReadsItsNowText() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "done", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)

            // The shown pane is still "working"; the read finds the pane "done".
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Ok, sentNote("Esc", "The pane is now done."))
        }
    }

    @Test
    fun failedPaneReadShowsNothing() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            server.fail(PANES_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)

            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.mainClock.autoAdvance = true
            composeRule.awaitUntil { panesRequests(server) == 1 }
            settleReads()
            assertNoOkNote("a failed read")
            assertTrue("a failed read is not a send failure", shownNotes(NoteTone.Error).isEmpty())
        }
    }

    @Test
    fun secondEscAtThreeSecondsRestartsTheWindow() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)

            composeRule.mainClock.advanceTimeBy(3_000, ignoreFrameDuration = true)
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(2)
            assertEquals(2, keyRequests(server))

            composeRule.mainClock.advanceTimeBy(2_000, ignoreFrameDuration = true)
            // The first window's close fires at exactly 5 s; its read starts on the frame after.
            composeRule.mainClock.advanceTimeByFrame()
            settleReads()
            assertEquals("the first window is gone at 5 s", 0, panesRequests(server))
            assertTrue("no result at 5 s", shownNotes(NoteTone.Ok).isEmpty())

            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Ok, sentNote("Esc", NOTHING_CHANGED))
            assertEquals("one read, at 8 s", 1, panesRequests(server))
        }
    }

    @Test
    fun aTapClearsAShownResult() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.mainClock.autoAdvance = true
            awaitNote(NoteTone.Ok, sentNote("Esc", NOTHING_CHANGED))

            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            composeRule.awaitUntil { keyRequests(server) == 2 }
            assertNoOkNote("the tap")
        }
    }

    @Test
    fun aTapCancelsAnOpenWindow() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            setQuickKeys(server)

            // The clock is still from before the tap, so the window opens at the time the test advances from.
            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("Esc").performClick()
            awaitWindowOpened(1)
            composeRule.mainClock.advanceTimeBy(3_000, ignoreFrameDuration = true)

            composeRule.mainClock.autoAdvance = true
            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            composeRule.awaitUntil { keyRequests(server) == 2 }
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            settleReads()
            assertEquals("the cancelled window never reads", 0, panesRequests(server))
            assertNoOkNote("a cancelled window")
        }
    }

    @Test
    fun otherKeysNeverShowAResult() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            listOf("Enter", "←", "↑", "↓", "→").forEachIndexed { index, label ->
                composeRule.onNodeWithText(label).performScrollTo().performClick()
                composeRule.waitForIdle()
                composeRule.awaitUntil { keyRequests(server) == index + 1 }
            }
            composeRule.runOnIdle { shown.state = "done" }
            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS * 2, ignoreFrameDuration = true)
            composeRule.waitForIdle()

            assertEquals("no key opens a window", 0, panesRequests(server))
            assertNoOkNote("the other keys")
            assertTrue("the other keys show no error note", shownNotes(NoteTone.Error).isEmpty())
        }
    }

    @Test
    fun keysFoldAwayWhileTheDraftHasTextAndReturnWhenItClears() {
        FakeOrreryServer().use { server ->
            val shown = ShownPane().apply { draftPresent = true }
            setQuickKeys(server, shown)
            composeRule.waitForIdle()
            assertTrue("no key shows while the draft has text", keyLabelsShown().isEmpty())

            composeRule.runOnIdle { shown.draftPresent = false }
            composeRule.awaitExactlyOne(hasText("Esc"))

            composeRule.runOnIdle { shown.draftPresent = true }
            composeRule.awaitNone(hasText("Esc"))
        }
    }

    private fun servePane(server: FakeOrreryServer, state: String, changed: Long) {
        server.respond(
            PANES_PATH,
            PanesListResponse.newBuilder()
                .addPanes(PaneProto.newBuilder().setId(PANE_ID).setState(state).setChanged(changed))
                .build(),
        )
    }

    private fun keyRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == KEYS_PATH }

    private fun panesRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == PANES_PATH }

    /**
     * Waits until the reads and answers in flight have finished, so a negative check on reads or on what shows does
     * not pass before they have run. Only work already started on another thread is waited on; with the clock
     * stopped, a read the clock has not reached is not started, and the check stands.
     */
    private fun settleReads() {
        composeRule.waitForIdle()
        composeRule.awaitUntil("the reads in flight to finish") { Quiescence.inFlight() == null }
        composeRule.waitForIdle()
    }

    /** The text of each shown note of [tone], in order. Each note is an InlineNote, so its tone is on its text node. */
    private fun shownNotes(tone: NoteTone): List<String> =
        composeRule.onAllNodes(SemanticsMatcher.expectValue(NoteToneKey, tone)).fetchSemanticsNodes().map { node ->
            node.config[SemanticsProperties.Text].joinToString("") { it.text }
        }

    /** Waits until exactly one note of [tone] shows, reading [text]. */
    private fun awaitNote(tone: NoteTone, text: String) {
        composeRule.awaitUntil("the $tone note \"$text\"") { shownNotes(tone) == listOf(text) }
    }

    private fun assertNoOkNote(context: String) {
        assertTrue("$context shows no ok note", shownNotes(NoteTone.Ok).isEmpty())
    }

    /** The labels of the quick keys on screen. */
    private fun keyLabelsShown(): List<String> =
        labelToKey.map { it.first }.filter { label -> composeRule.onAllNodes(hasText(label)).fetchSemanticsNodes().isNotEmpty() }

    private fun flashing(label: String): Boolean =
        composeRule.onNode(hasText(label) and hasClickAction()).fetchSemanticsNode()
            .config.getOrElse(Flashing) { false }

    private fun setQuickKeys(server: FakeOrreryServer, shown: ShownPane = ShownPane()) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalHapticFeedback provides haptic) {
                QuickKeys(
                    paneId = PANE_ID,
                    client = client,
                    state = shown.state,
                    changed = shown.changed,
                    draftPresent = shown.draftPresent,
                )
            }
        }
    }

    /** The pane's state and changed time, and whether the draft has text, as the screen shows them; a test moves them. */
    private class ShownPane {
        var state by mutableStateOf("working")
        var changed by mutableLongStateOf(CHANGED_AT)
        var draftPresent by mutableStateOf(false)
    }

    private class RecordingHaptic : HapticFeedback {
        val performed = mutableListOf<HapticFeedbackType>()

        override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) {
            performed += hapticFeedbackType
        }
    }
}

/** The note a sent Esc or Ctrl-C shows: its label, then what the pane did. */
private fun sentNote(label: String, words: String): String = "$label sent. $words"

/** The note a key the server did not land shows: its label, then why. */
private fun notSentNote(label: String, why: String): String = "$label was not sent: $why"
