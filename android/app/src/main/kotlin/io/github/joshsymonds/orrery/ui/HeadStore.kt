package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

/** How the conversation header shows on a phone: all of it, the folded line while reading older lines, or nothing. */
enum class HeadFold { OPEN, READING, HIDDEN }

/**
 * The header's fold. The owner's hide wins; otherwise the header folds to its line while the reader is away from the
 * newest line and the owner has not tapped it open since.
 */
fun headFold(hidden: Boolean, atNewest: Boolean, tapped: Boolean): HeadFold =
    when {
        hidden -> HeadFold.HIDDEN
        !atNewest && !tapped -> HeadFold.READING
        else -> HeadFold.OPEN
    }

/** The owner's choice to hide the header, kept on this device across conversations and launches. */
class HeadStore(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    var hidden: Boolean
        get() = preferences.getBoolean(HIDDEN_KEY, false)
        set(value) {
            preferences.edit().putBoolean(HIDDEN_KEY, value).apply()
        }

    private companion object {
        const val PREFERENCES_NAME = "orrery-settings"
        const val HIDDEN_KEY = "head-hidden"
    }
}

/** The hidden choice as Compose state, read from [HeadStore] once and written through to it on every change. */
@Composable
fun rememberHeadHidden(): MutableState<Boolean> {
    val context = LocalContext.current
    return remember(context) {
        val store = HeadStore(context)
        WrittenThroughState(mutableStateOf(store.hidden), store)
    }
}

private class WrittenThroughState(
    private val state: MutableState<Boolean>,
    private val store: HeadStore,
) : MutableState<Boolean> by state {
    override var value: Boolean
        get() = state.value
        set(value) {
            state.value = value
            store.hidden = value
        }

    override fun component1(): Boolean = value

    override fun component2(): (Boolean) -> Unit = { value = it }
}
