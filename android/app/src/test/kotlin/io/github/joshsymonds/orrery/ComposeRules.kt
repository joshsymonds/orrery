package io.github.joshsymonds.orrery

import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.AndroidComposeTestRule
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.ComposeTestRule
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.espresso.IdlingRegistry
import androidx.test.espresso.IdlingResource
import androidx.test.ext.junit.rules.ActivityScenarioRule
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestCoroutineScheduler
import org.junit.runner.Description
import org.junit.runners.model.Statement

/*
 * Compose test rules whose compositions, effects and coroutines run on the main thread only.
 *
 * Without an effect context the rules run composition on an UnconfinedTestDispatcher, which resumes a
 * coroutine on whatever thread completed its suspension. Code after `withContext(Dispatchers.IO)`, a
 * stream's onDelta, and every snapshotFlow collector woken by an apply notification sent from that
 * thread then run on an IO or OkHttp thread, concurrently with the main thread's composition and
 * layout. The app never does this: AndroidUiDispatcher posts every continuation to the main thread.
 * ConversationViewThreadTest forces the failure it caused.
 *
 * These rules use a StandardTestDispatcher instead, which queues each continuation on the test
 * scheduler. The main test clock runs that queue on the main thread as it advances, and [MainThreadDrain]
 * runs what is due now each time Espresso checks for idle, so waitForIdle still sees an effect's work
 * finish.
 */

/**
 * Runs the scheduler's due continuations whenever Espresso (Robolectric's main-looper idle) asks whether
 * the app is idle. The rule's own registerIdlingResource never reaches Espresso on Robolectric, so this is
 * registered with [IdlingRegistry] for the test's duration.
 */
private class MainThreadDrain(private val scheduler: TestCoroutineScheduler) : IdlingResource {
    override fun getName(): String = "orrery main-thread compose drain"

    override fun isIdleNow(): Boolean {
        if (Looper.myLooper() == Looper.getMainLooper()) {
            scheduler.runCurrent()
        }
        return true
    }

    override fun registerIdleTransitionCallback(callback: IdlingResource.ResourceCallback?) = Unit

    /** [base] with this drain registered while it runs. */
    fun around(base: Statement): Statement = object : Statement() {
        override fun evaluate() {
            IdlingRegistry.getInstance().register(this@MainThreadDrain)
            try {
                base.evaluate()
            } finally {
                IdlingRegistry.getInstance().unregister(this@MainThreadDrain)
            }
        }
    }
}

/** A compose rule over [inner] whose composition runs on [scheduler] and drains on the main thread. */
class MainThreadComposeRule internal constructor(
    private val inner: ComposeContentTestRule,
    scheduler: TestCoroutineScheduler,
) : ComposeContentTestRule by inner {
    private val drain = MainThreadDrain(scheduler)

    override fun apply(base: Statement, description: Description): Statement =
        inner.apply(drain.around(base), description)
}

/** [MainThreadComposeRule] without a host: the test launches the activity that sets the content. */
class MainThreadEmptyComposeRule internal constructor(
    private val inner: ComposeTestRule,
    scheduler: TestCoroutineScheduler,
) : ComposeTestRule by inner {
    private val drain = MainThreadDrain(scheduler)

    override fun apply(base: Statement, description: Description): Statement =
        inner.apply(drain.around(base), description)
}

/** [MainThreadComposeRule] hosted in an activity of class [A], which [activity] and [activityRule] reach. */
class MainThreadAndroidComposeRule<A : ComponentActivity> internal constructor(
    private val inner: AndroidComposeTestRule<ActivityScenarioRule<A>, A>,
    scheduler: TestCoroutineScheduler,
) : ComposeContentTestRule by inner {
    private val drain = MainThreadDrain(scheduler)

    val activity: A get() = inner.activity

    val activityRule: ActivityScenarioRule<A> get() = inner.activityRule

    override fun apply(base: Statement, description: Description): Statement =
        inner.apply(drain.around(base), description)
}

@OptIn(ExperimentalTestApi::class)
fun mainThreadComposeRule(): MainThreadComposeRule {
    val scheduler = TestCoroutineScheduler()
    return MainThreadComposeRule(createComposeRule(StandardTestDispatcher(scheduler)), scheduler)
}

@OptIn(ExperimentalTestApi::class)
fun mainThreadEmptyComposeRule(): MainThreadEmptyComposeRule {
    val scheduler = TestCoroutineScheduler()
    return MainThreadEmptyComposeRule(createEmptyComposeRule(StandardTestDispatcher(scheduler)), scheduler)
}

inline fun <reified A : ComponentActivity> mainThreadAndroidComposeRule(): MainThreadAndroidComposeRule<A> =
    mainThreadAndroidComposeRule(A::class.java)

@OptIn(ExperimentalTestApi::class)
fun <A : ComponentActivity> mainThreadAndroidComposeRule(activityClass: Class<A>): MainThreadAndroidComposeRule<A> {
    val scheduler = TestCoroutineScheduler()
    return MainThreadAndroidComposeRule(
        createAndroidComposeRule(activityClass, StandardTestDispatcher(scheduler)),
        scheduler,
    )
}

/** How long a wait yields between checks so the threads doing the real work can run, as Compose's waitUntil does. */
private const val CHECK_PAUSE_MILLIS = 10L

/*
 * Waits for work on other threads: a fake server's round trip, an IO dispatcher's call, OkHttp's stream. No
 * test clock moves that work, so a wall-clock deadline per wait only measures how loaded the machine is (a 5 s
 * deadline timed out PaneScreenTest's first render at load 80). These waits have no deadline of their own; the
 * test task's timeout in build.gradle.kts bounds a wait that never ends. Each check advances the main clock by a
 * frame while it auto-advances, as waitUntil does, so a wait on virtual time still ends.
 */

/** Waits for [condition], with no deadline of its own. */
fun ComposeTestRule.awaitUntil(condition: () -> Boolean) {
    while (!condition()) {
        if (mainClock.autoAdvance) mainClock.advanceTimeByFrame()
        Thread.sleep(CHECK_PAUSE_MILLIS)
    }
}

/** Waits until exactly one node matches [matcher], with no deadline of its own. */
fun ComposeTestRule.awaitExactlyOne(matcher: SemanticsMatcher) {
    awaitUntil { onAllNodes(matcher).fetchSemanticsNodes().size == 1 }
}

/** Waits until no node matches [matcher], with no deadline of its own. */
fun ComposeTestRule.awaitNone(matcher: SemanticsMatcher) {
    awaitUntil { onAllNodes(matcher).fetchSemanticsNodes().isEmpty() }
}

/**
 * Waits for [condition], letting the main thread run its queued continuations before each check, as the app's
 * main looper runs them between frames, with no deadline of its own. Use it when the clock is paused and
 * [condition] reads no Compose state (a fake server's requests): a plain wait then neither advances the clock nor
 * idles the main thread, so a coroutine a tap launched would never start.
 */
fun ComposeTestRule.awaitUntilAfterIdle(condition: () -> Boolean) {
    awaitUntil {
        waitForIdle()
        condition()
    }
}
