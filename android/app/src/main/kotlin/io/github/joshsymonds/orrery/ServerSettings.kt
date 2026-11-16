package io.github.joshsymonds.orrery

import android.content.Context

/**
 * The orreryd server address the app talks to: the one saved in settings, else [defaultUrl], the address the build
 * was given (empty when it was given none). With neither, [hasServer] is false and the app asks for the server.
 */
class ServerSettings(context: Context, private val defaultUrl: String = BuildConfig.DEFAULT_SERVER_URL) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    val serverUrl: String
        get() = preferences.getString(SERVER_URL_KEY, null) ?: defaultUrl

    /** Whether there is an address to talk to, saved or built in. */
    val hasServer: Boolean
        get() = serverUrl.isNotEmpty()

    fun saveServerUrl(value: String) {
        val url = value.trim()
        val editor = preferences.edit()
        if (url.isEmpty()) {
            editor.remove(SERVER_URL_KEY)
        } else {
            editor.putString(SERVER_URL_KEY, url)
        }
        editor.apply()
    }

    private companion object {
        const val PREFERENCES_NAME = "orrery-settings"
        const val SERVER_URL_KEY = "server-url"
    }
}
