package io.github.joshsymonds.orrery

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterResponse
import io.github.joshsymonds.orrery.push.PushRegistration
import io.github.joshsymonds.orrery.ui.PaneScreen
import java.util.Timer
import java.util.concurrent.CountDownLatch
import kotlin.concurrent.schedule
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicReference

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val PANE_ID = "%3"
private const val SLOW_ANSWER_MILLIS = 3_000L
private const val NOW = 1_700_000_000L
private const val LATE_START_MILLIS = 300L
private const val LATE_TEXT_MILLIS = 1_000L
private const val REGISTER_PATH = "/orrery.v1.Push/Register"

/**
 * The waits end when their condition holds and fail as soon as nothing can make it hold, with no clock deadline:
 * a node that never shows fails in seconds (it hung until Gradle's 20-minute test timeout before), and an answer
 * that is slow in real time still shows, however long it takes.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuiescentWaitTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun aNodeThatNeverShowsFailsOnceTheVirtualBudgetIsSpent() {
        composeRule.setContent { Text("synthetic shown") }

        val failure = assertThrows(AssertionError::class.java) {
            composeRule.awaitExactlyOne(hasText("synthetic never shown"))
        }

        assertTrue(failure.message, failure.message!!.contains("synthetic never shown"))
        assertTrue(failure.message, failure.message!!.contains("virtual time with nothing in flight"))
    }

    @Test
    fun aNodeThatNeverShowsFailsAtOnceWhenTheClockIsStopped() {
        composeRule.setContent { Text("synthetic shown") }
        composeRule.mainClock.autoAdvance = false

        val failure = assertThrows(AssertionError::class.java) {
            composeRule.awaitExactlyOne(hasText("synthetic never shown"))
        }

        assertTrue(failure.message, failure.message!!.contains("the clock is stopped and nothing is in flight"))
    }

    @Test
    fun aNodeAPollingScreenNeverShowsFailsAndNamesTheServersRequests() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, panes())
            server.route(STREAM_PATH) {
                Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
            }
            composeRule.setContent { MaterialTheme { PaneScreen(PANE_ID, OrreryClient(server.baseUrl)) } }

            val failure = assertThrows(AssertionError::class.java) {
                composeRule.awaitExactlyOne(hasTestTag("synthetic-never-shown"))
            }

            assertTrue(failure.message, failure.message!!.contains("synthetic-never-shown"))
            assertTrue(failure.message, failure.message!!.contains(PANES_PATH))
        }
    }

    @Test
    fun anAnswerSlowInRealTimeStillShowsWithTheClockStopped() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, panes(), delayMillis = SLOW_ANSWER_MILLIS)
            composeRule.mainClock.autoAdvance = false
            val label = AtomicReference<String?>()
            val client = OrreryClient(server.baseUrl)
            // With the clock stopped nothing recomposes, so the wait reads the state the answer sets, not the screen.
            composeRule.setContent {
                LaunchedEffect(Unit) { label.set(withContext(Dispatchers.IO) { client.panes().single().label }) }
            }

            composeRule.awaitUntilAfterIdle("the slow answer's label") { label.get() != null }
            assertEquals("synthetic-label", label.get())
        }
    }

    @Test
    fun anAnswerSlowInRealTimeStillShowsWhileTheClockRuns() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, panes(), delayMillis = SLOW_ANSWER_MILLIS)
            showFirstPaneLabel(server)

            composeRule.awaitExactlyOne(hasText("synthetic-label"))
        }
    }

    @Test
    fun aConditionOutsideComposeFailsOnceNothingIsInFlightAndWaitsOutASlowAnswer() {
        assertThrows(AssertionError::class.java) { Quiescence.awaitUntil("a condition that never holds") { false } }

        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, panes(), delayMillis = SLOW_ANSWER_MILLIS)
            val answer = AtomicReference<String?>()
            BackgroundWork.run("synthetic slow call") { answer.set(OrreryClient(server.baseUrl).panes().single().label) }

            Quiescence.awaitUntil("the slow answer") { answer.get() != null }
            assertEquals("synthetic-label", answer.get())
        }
    }

    @Test
    fun backgroundWorkWhoseThreadHasNotRunYetIsStillWaitedFor() {
        val previous = BackgroundWork.start
        // A timer starts the work's thread late, so when the wait first looks no thread has the work yet: the
        // order in which ReplyReceiverTest.blankReplySendsNothingAndShowsNotSent saw "nothing is in flight".
        val startLate: (Runnable) -> Unit = { work -> Timer(true).schedule(LATE_START_MILLIS) { Thread(work).start() } }
        val startUncountedLate: (String, Runnable) -> Unit = { _, work -> startLate(work) }
        try {
            // As before: the thread was started outside the count, so the wait judged too early.
            val uncounted = CountDownLatch(1)
            BackgroundWork.start = startUncountedLate
            BackgroundWork.run("synthetic late work") { uncounted.countDown() }
            assertThrows(AssertionError::class.java) {
                Quiescence.awaitUntil("the late-started work") { uncounted.count == 0L }
            }
            uncounted.await()

            // Now: BackgroundWork is counted from the moment it is asked for, so the wait holds until it ends.
            val counted = CountDownLatch(1)
            BackgroundWork.start = { name, work -> Quiescence.countedStart(name, work, startLate) }
            BackgroundWork.run("synthetic late work") { counted.countDown() }
            Quiescence.awaitUntil("the late-started work") { counted.count == 0L }
        } finally {
            BackgroundWork.start = previous
        }
    }

    @Test
    fun workAnEarlierTestLeftRunningDoesNotHoldThisTestsClock() {
        // Work that never ends, as OrreryPushServiceTest's registration did when its server closed before it took
        // the answer: it retried the closed server for the rest of the JVM.
        val release = CountDownLatch(1)
        BackgroundWork.run("synthetic endless work") { release.await() }
        try {
            assertEquals("the work counts while its own test runs", 1, Quiescence.backgroundWorkRunning())

            // The next test begins. Counted still, the work held every later wait whose screen needs the clock to
            // move: they yielded to it forever (PaneScreenStreamTest's silent streams, to the 20-minute timeout).
            Quiescence.newTest()
            assertEquals("an earlier test's work no longer counts", null, Quiescence.inFlight())
            composeRule.setContent {
                var shown by remember { mutableStateOf(false) }
                LaunchedEffect(Unit) {
                    delay(LATE_TEXT_MILLIS)
                    shown = true
                }
                if (shown) Text("synthetic shown a second later")
            }

            composeRule.awaitExactlyOne(hasText("synthetic shown a second later"))
        } finally {
            release.countDown()
        }
    }

    @Test
    fun aTestThatEndsWithItsWorkAsleepFailsNamingTheWork() {
        // Work that only the clock could end, with nothing in flight, as the push registration in its retry sleep.
        BackgroundWork.run("synthetic sleeping work") {
            try {
                Thread.sleep(Long.MAX_VALUE)
            } catch (e: InterruptedException) {
                // Let go by the test below.
            }
        }

        val failure = assertThrows(AssertionError::class.java) { Quiescence.endTest() }

        assertTrue(failure.message, failure.message!!.contains("\"synthetic sleeping work\""))
        Thread.getAllStackTraces().keys.single { it.name == "synthetic sleeping work" }.interrupt()
        Quiescence.awaitUntil("the sleeping work to end") { Quiescence.backgroundWorkRunning() == 0 }
    }

    @Test
    fun aTestEndWaitsForWorkThatFinishesLateOnAnAnswer() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, panes(), delayMillis = SLOW_ANSWER_MILLIS)
            val label = AtomicReference<String?>()
            BackgroundWork.run("synthetic late call") { label.set(OrreryClient(server.baseUrl).panes().single().label) }

            Quiescence.endTest()

            assertEquals("synthetic-label", label.get())
            assertEquals(0, Quiescence.backgroundWorkRunning())
        }
    }

    @Test
    fun aRefusedRegistrationLeftRetryingFailsTheTestEnd() {
        // OrreryPushServiceTest's leak, forced: the server refuses, so the registration sleeps before its retry.
        FakeOrreryServer().use { server ->
            server.fail(REGISTER_PATH, Code.UNAVAILABLE, "synthetic refusal")
            BackgroundWork.run("push registration") {
                PushRegistration(OrreryClient(server.baseUrl)).register("https://push.example.com/up", "key", "auth")
            }

            val failure = assertThrows(AssertionError::class.java) { Quiescence.endTest() }
            assertTrue(failure.message, failure.message!!.contains("\"push registration\""))

            // Accepted on its next try, the registration ends, and the server's close finds nothing left running.
            server.respond(REGISTER_PATH, RegisterResponse.getDefaultInstance())
            Quiescence.awaitUntil("the registration to be accepted") { Quiescence.backgroundWorkRunning() == 0 }
        }
    }

    private fun showFirstPaneLabel(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            var label by remember { mutableStateOf("") }
            LaunchedEffect(Unit) { label = withContext(Dispatchers.IO) { client.panes().single().label } }
            Text(label)
        }
    }

    private fun panes(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(0)
                .setCwd("/home/user/alpha")
                .setCommand("claude")
                .setState("waiting")
                .setSummary("Synthetic summary.")
                .setLabel("synthetic-label")
                .setChanged(NOW - 30)
                .setSeen(NOW - 10)
                .setQuestion("")
                .setQuestionHash(""),
        )
        .build()
}
