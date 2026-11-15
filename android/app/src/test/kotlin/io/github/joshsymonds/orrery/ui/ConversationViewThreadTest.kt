package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

private const val ENTRY_COUNT = 40
private const val HELD_MEASURE_MILLIS = 500L
private const val LATCH_SECONDS = 5L

/**
 * A composition coroutine resumed from an IO thread while the main thread is measuring a view
 * traversal (a resize, as the keyboard's insets cause; a measure inside a test frame defers every
 * continuation to the main thread, so it is not exposed).
 *
 * With the test rules' default UnconfinedTestDispatcher the continuation ran on the IO thread and sent
 * the pending snapshot changes from there, so ConversationView's viewport follower scrolled, and so
 * measured, from the IO thread in the middle of the main thread's measure:
 * IllegalArgumentException "performMeasureAndLayout called during measure layout", the failure
 * MainActivityInsetsTest hit. [mainThreadAndroidComposeRule] keeps every continuation on the main
 * thread, as the app's AndroidUiDispatcher does.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
class ConversationViewThreadTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    @Test
    fun aResizeIsFollowedOnTheMainThreadWhileAnIoThreadResumesACoroutine() {
        val entries = (1..ENTRY_COUNT).map { ConvoEntry.Reply("synthetic reply $it") }
        val holdNextMeasure = AtomicBoolean(false)
        val measuring = CountDownLatch(1)
        lateinit var scope: CoroutineScope
        composeRule.setContent {
            scope = rememberCoroutineScope()
            MaterialTheme {
                // Measures the conversation, then, once armed, holds the main thread inside this measure.
                Layout(content = { ConversationView(entries = entries) }) { measurables, constraints ->
                    val conversation = measurables.single().measure(constraints)
                    if (holdNextMeasure.getAndSet(false)) {
                        measuring.countDown()
                        Thread.sleep(HELD_MEASURE_MILLIS)
                    }
                    layout(constraints.maxWidth, constraints.maxHeight) { conversation.place(0, 0) }
                }
            }
        }
        composeRule.onNodeWithText("synthetic reply $ENTRY_COUNT").assertIsDisplayed()

        val resumed = CountDownLatch(1)
        composeRule.runOnUiThread {
            scope.launch {
                withContext(Dispatchers.IO) { measuring.await(LATCH_SECONDS, TimeUnit.SECONDS) }
                resumed.countDown()
            }
            holdNextMeasure.set(true)
            val host = composeHost()
            host.layoutParams = host.layoutParams.apply { height = host.height / 2 }
        }
        composeRule.waitForIdle()

        assertEquals("the resize never measured", 0L, measuring.count)
        composeRule.waitUntil(LATCH_SECONDS * 1_000) { resumed.count == 0L }
        composeRule.onNodeWithText("synthetic reply $ENTRY_COUNT").assertIsDisplayed()
    }

    private fun composeHost(): View =
        composeRule.activity.findViewById<ViewGroup>(android.R.id.content).getChildAt(0)
}
