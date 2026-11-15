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
import io.github.joshsymonds.orrery.ui.PaneScreen
import kotlinx.coroutines.Dispatchers
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
import kotlin.concurrent.thread

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val PANE_ID = "%3"
private const val SLOW_ANSWER_MILLIS = 3_000L
private const val NOW = 1_700_000_000L

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
            thread { answer.set(OrreryClient(server.baseUrl).panes().single().label) }

            Quiescence.awaitUntil("the slow answer") { answer.get() != null }
            assertEquals("synthetic-label", answer.get())
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
