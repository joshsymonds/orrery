package io.github.joshsymonds.orrery

import androidx.annotation.VisibleForTesting

/**
 * Starts work on its own plain thread: a notification action's work under goAsync, a push registration. Every such
 * thread in the app starts here, so the unit tests can count the work from the moment it is asked for until it ends,
 * and name it when a test ends with it still running.
 */
internal object BackgroundWork {
    /** How work named by its first argument starts. The app gives it a new thread; the unit tests count it too. */
    @VisibleForTesting
    internal var start: (String, Runnable) -> Unit = { name, work -> Thread(work, name).start() }

    /** Runs [work], called [name], on its own thread. */
    fun run(name: String, work: () -> Unit) {
        start(name, Runnable(work))
    }
}
