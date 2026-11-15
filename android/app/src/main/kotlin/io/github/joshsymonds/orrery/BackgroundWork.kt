package io.github.joshsymonds.orrery

import androidx.annotation.VisibleForTesting

/**
 * Starts work on its own plain thread: a notification action's work under goAsync, a push registration. Every such
 * thread in the app starts here, so the unit tests can count the work from the moment it is asked for until it ends.
 */
internal object BackgroundWork {
    /** How work starts. The app gives it a new thread; the unit tests count it as in flight until it returns. */
    @VisibleForTesting
    internal var start: (Runnable) -> Unit = { work -> Thread(work).start() }

    /** Runs [work] on its own thread. */
    fun run(work: () -> Unit) {
        start(Runnable(work))
    }
}
