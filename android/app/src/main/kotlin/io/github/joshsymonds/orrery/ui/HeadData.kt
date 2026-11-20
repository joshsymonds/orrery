package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** The epics a conversation steers, in listing order, and the Get detail of each one that has a detail. */
data class HeadEpics(val steered: List<EpicRow>, val details: Map<String, EpicDetail>)

/**
 * The head's read for one refresh: the listing's epics that [conversationId] steers, each with its Get detail. An empty
 * id steers nothing, so it makes no call. Null when a call fails, so the head keeps what it showed.
 */
suspend fun fetchHeadEpics(client: OrreryClient, conversationId: String): HeadEpics? {
    if (conversationId.isEmpty()) return HeadEpics(emptyList(), emptyMap())
    return withContext(Dispatchers.IO) {
        try {
            val steered = steeredEpics(conversationId, client.epics())
            val details = steered.mapNotNull { epic -> client.epic(epic.id)?.let { epic.id to it } }.toMap()
            HeadEpics(steered, details)
        } catch (e: IOException) {
            null
        }
    }
}
