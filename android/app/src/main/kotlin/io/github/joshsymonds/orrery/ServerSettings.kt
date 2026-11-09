package io.github.joshsymonds.orrery

import android.content.Context

class ServerSettings(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    val serverUrl: String
        get() = preferences.getString(SERVER_URL_KEY, DEFAULT_SERVER_URL) ?: DEFAULT_SERVER_URL

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

    companion object {
        const val DEFAULT_SERVER_URL = "https://vermissian.tail82223.ts.net:8490"

        private const val PREFERENCES_NAME = "orrery-settings"
        private const val SERVER_URL_KEY = "server-url"
    }
}
