package io.github.joshsymonds.orrery.push

import android.content.Context
import androidx.core.content.edit

/**
 * This phone's push choices and records: which channels post, the address push was registered with, and whether no
 * distributor answered.
 */
class PushPreferences(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    fun posts(channelId: String): Boolean = preferences.getBoolean(postsKey(channelId), true)

    fun setPosts(channelId: String, on: Boolean) {
        preferences.edit { putBoolean(postsKey(channelId), on) }
    }

    /** The server address push was last registered with, empty when none has been. */
    var registeredAddress: String
        get() = preferences.getString(REGISTERED_ADDRESS_KEY, null) ?: ""
        set(value) {
            preferences.edit { putString(REGISTERED_ADDRESS_KEY, value) }
        }

    /** Whether the phone has no UnifiedPush distributor, recorded when setup found none to answer. */
    var distributorMissing: Boolean
        get() = preferences.getBoolean(DISTRIBUTOR_MISSING_KEY, false)
        set(value) {
            preferences.edit { putBoolean(DISTRIBUTOR_MISSING_KEY, value) }
        }

    private fun postsKey(channelId: String): String = "posts:$channelId"

    private companion object {
        const val PREFERENCES_NAME = "orrery-push"
        const val REGISTERED_ADDRESS_KEY = "registered-address"
        const val DISTRIBUTOR_MISSING_KEY = "distributor-missing"
    }
}
