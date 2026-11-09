package io.github.joshsymonds.orrery.push

import android.util.Log
import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException

private const val TAG = "PushRegistration"

/**
 * Registers a UnifiedPush endpoint with the orrery server, retrying with
 * doubling delays until the server accepts. The delay function is injectable so
 * tests run the retry loop without waiting.
 */
class PushRegistration(
    private val client: OrreryClient,
    private val sleep: (millis: Long) -> Unit = { Thread.sleep(it) },
) {
    fun register(endpoint: String, p256dh: String, auth: String) {
        var delayMillis = INITIAL_DELAY_MS
        while (true) {
            try {
                client.registerPush(endpoint, p256dh, auth)
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
