package io.github.joshsymonds.orrery.ui

import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry

/** A lifecycle the test moves by hand, as the activity's would move. */
internal class HandLifecycleOwner : LifecycleOwner {
    private val registry = LifecycleRegistry(this)
    override val lifecycle: LifecycleRegistry get() = registry
}
