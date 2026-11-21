package io.github.joshsymonds.orrery.push

import android.content.Context
import java.util.UUID
import org.json.JSONArray
import org.json.JSONObject

/** One shade reply the receiver did not see land, kept on this phone under its own entry id. */
data class KeptReply(val id: String, val text: String)

/**
 * The shade replies this phone keeps, by host and conversation. A conversation is a pane or a session, named by its
 * kind ("pane" or "session") and its id. Entries for different hosts, kinds or ids never mix.
 */
class KeptReplies(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)

    /** Appends one reply to the conversation's entries, and returns whether the phone stored it. */
    fun keep(host: String, kind: String, id: String, text: String): Boolean = synchronized(LOCK) {
        val entries = entries(host, kind, id)
        entries.put(JSONObject().put(ENTRY_ID, UUID.randomUUID().toString()).put(ENTRY_TEXT, text))
        preferences.edit().putString(keyOf(host, kind, id), entries.toString()).commit()
    }

    /** The replies kept for the conversation, in the order kept. */
    fun kept(host: String, kind: String, id: String): List<KeptReply> {
        val entries = entries(host, kind, id)
        return List(entries.length()) { index ->
            val entry = entries.getJSONObject(index)
            KeptReply(entry.getString(ENTRY_ID), entry.getString(ENTRY_TEXT))
        }
    }

    private fun entries(host: String, kind: String, id: String): JSONArray {
        val stored = preferences.getString(keyOf(host, kind, id), null) ?: return JSONArray()
        return JSONArray(stored)
    }

    private companion object {
        const val PREFERENCES_NAME = "orrery-kept-replies"
        const val ENTRY_ID = "id"
        const val ENTRY_TEXT = "text"
        val LOCK = Any()

        /** A JSON array of the three parts, so no host, kind or id can run into another's key. */
        fun keyOf(host: String, kind: String, id: String): String = JSONArray(listOf(host, kind, id)).toString()
    }
}
