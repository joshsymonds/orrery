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
import io.github.joshsymonds.orrery.api.Quiescence
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestCoroutineScheduler
import org.junit.runner.Description
import org.junit.runners.model.Statement
import org.robolectric.Shadows.shadowOf

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

    /**
     * [base] with this drain registered while it runs, then the test's end: a test that leaves BackgroundWork
     * running fails, naming it (Quiescence.endTest). A failure of the test itself stays the failure reported.
     */
    fun around(base: Statement): Statement = object : Statement() {
        override fun evaluate() {
            IdlingRegistry.getInstance().register(this@MainThreadDrain)
            try {
                base.evaluate()
            } catch (failure: Throwable) {
                runCatching { Quiescence.endTest() }.exceptionOrNull()?.let(failure::addSuppressed)
                throw failure
            } finally {
                IdlingRegistry.getInstance().unregister(this@MainThreadDrain)
            }
            Quiescence.endTest()
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
    Quiescence.install()
    Quiescence.newTest()
    val scheduler = TestCoroutineScheduler()
    return MainThreadComposeRule(createComposeRule(StandardTestDispatcher(scheduler)), scheduler)
}

@OptIn(ExperimentalTestApi::class)
fun mainThreadEmptyComposeRule(): MainThreadEmptyComposeRule {
    Quiescence.install()
    Quiescence.newTest()
    val scheduler = TestCoroutineScheduler()
    return MainThreadEmptyComposeRule(createEmptyComposeRule(StandardTestDispatcher(scheduler)), scheduler)
}

inline fun <reified A : ComponentActivity> mainThreadAndroidComposeRule(): MainThreadAndroidComposeRule<A> =
    mainThreadAndroidComposeRule(A::class.java)

@OptIn(ExperimentalTestApi::class)
fun <A : ComponentActivity> mainThreadAndroidComposeRule(activityClass: Class<A>): MainThreadAndroidComposeRule<A> {
    Quiescence.install()
    Quiescence.newTest()
    val scheduler = TestCoroutineScheduler()
    return MainThreadAndroidComposeRule(
        createAndroidComposeRule(activityClass, StandardTestDispatcher(scheduler)),
        scheduler,
    )
}

/** How long a wait yields while real work is in flight, so the threads doing it can run. */
private const val CHECK_PAUSE_MILLIS = 10L

/**
 * How much virtual time a wait lets pass, with nothing in flight, before it fails. Virtual time moves only while no
 * real work is in flight, so this bounds how long the screen's own timers may take, not how loaded the machine is.
 * The longest timer a test waits through is five seconds (the quick keys' read window).
 */
private const val VIRTUAL_BUDGET_MILLIS = 30_000L

/*
 * Waits end when their condition holds, and fail as soon as nothing can make it hold. While real work is in flight
 * (Quiescence: a fake-server handler, an unanswered request, an unread answer, a client thread running) the wait
 * yields and the clock stands still, however long the work takes. With nothing in flight, the wait runs the main
 * thread's queue, then moves the clock a frame if it auto-advances, so the screen's timers fire; it fails once the
 * clock has moved [VIRTUAL_BUDGET_MILLIS], or at once when the clock is stopped, since then nothing at all can change.
 * No wait reads a wall clock.
 */

/** Waits for [condition], named [description] in the failure. */
fun ComposeTestRule.awaitUntil(description: String = "the awaited condition", condition: () -> Boolean) {
    val startedAt = mainClock.currentTime
    while (!condition()) {
        shadowOf(Looper.getMainLooper()).idle()
        if (Quiescence.inFlight() != null) {
            Thread.sleep(CHECK_PAUSE_MILLIS)
            continue
        }
        if (!mainClock.autoAdvance) {
            // The clock is stopped and nothing is in flight: run what the main thread has queued, then judge.
            waitForIdle()
            if (condition()) return
            if (Quiescence.inFlight() != null) continue
            throw AssertionError(
                "$description never held: the clock is stopped and nothing is in flight; ${Quiescence.serverState()}",
            )
        }
        val waited = mainClock.currentTime - startedAt
        if (waited >= VIRTUAL_BUDGET_MILLIS) {
            throw AssertionError(
                "$description never held through $waited ms of virtual time with nothing in flight; " +
                    Quiescence.serverState(),
            )
        }
        mainClock.advanceTimeByFrame()
    }
}

/** Waits until exactly one node matches [matcher]. */
fun ComposeTestRule.awaitExactlyOne(matcher: SemanticsMatcher) {
    awaitUntil("exactly one node matching ${matcher.description}") {
        onAllNodes(matcher).fetchSemanticsNodes().size == 1
    }
}

/** Waits until no node matches [matcher]. */
fun ComposeTestRule.awaitNone(matcher: SemanticsMatcher) {
    awaitUntil("no node matching ${matcher.description}") { onAllNodes(matcher).fetchSemanticsNodes().isEmpty() }
}

/**
 * Waits for [condition], letting the main thread run its queued continuations before each check, as the app's
 * main looper runs them between frames. Use it when the clock is paused and [condition] reads no Compose state (a
 * fake server's requests): the wait then runs a coroutine a tap launched before it judges.
 */
fun ComposeTestRule.awaitUntilAfterIdle(description: String = "the awaited condition", condition: () -> Boolean) {
    awaitUntil(description) {
        waitForIdle()
        condition()
    }
}
