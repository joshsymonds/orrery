package io.github.joshsymonds.orrery.push

import android.util.Log
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException

private const val TAG = "PushRegistration"

/**
 * Registers a UnifiedPush endpoint with the orrery server, retrying with doubling delays until the server accepts.
 * Each attempt reads the address saved at that moment, so a loop started before the address was saved, or while it
 * changed, reaches the current one. The address the server accepted is recorded in [preferences]. The delay function
 * is injectable so tests run the retry loop without waiting.
 */
class PushRegistration(
    private val settings: ServerSettings,
    private val preferences: PushPreferences,
    private val sleep: (millis: Long) -> Unit = { Thread.sleep(it) },
) {
    fun register(endpoint: String, p256dh: String, auth: String) {
        var delayMillis = INITIAL_DELAY_MS
        while (true) {
            val address = settings.serverUrl
            try {
                OrreryClient(address).registerPush(endpoint, p256dh, auth)
                preferences.registeredAddress = address
                return
            } catch (e: IOException) {
                Log.w(TAG, "push registration failed; retrying in ${delayMillis}ms", e)
            }
            sleep(delayMillis)
            delayMillis = minOf(delayMillis * 2, MAX_DELAY_MS)
        }
    }

    companion object {
        internal const val INITIAL_DELAY_MS = 1_000L
        internal const val MAX_DELAY_MS = 60_000L
    }
}
