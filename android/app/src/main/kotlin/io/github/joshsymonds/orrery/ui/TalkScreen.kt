package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.UUID

private val TALK_CLOCK: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

/** A session that touched the epic: its id, the pane it runs in ("" when none), its label, its newest call, and when. */
data class TalkSession(val id: String, val pane: String, val label: String, val call: String, val at: Long)

enum class TalkKind { PANE, SESSION }

/** What opening a Talk row opens: a pane by its id, or a session by its id, under [label]. */
data class TalkTarget(val kind: TalkKind, val id: String, val label: String)

/** The first message of a Talk session about [epic]. */
fun talkPrompt(epic: String): String =
    "This session is about orrery epic $epic. Read it with epic_get, then wait for my words."

/** The pane a session runs in when it has one, else the session itself. */
fun talkTarget(session: TalkSession): TalkTarget =
    if (session.pane.isNotEmpty()) {
        TalkTarget(TalkKind.PANE, session.pane, talkLabel(session))
    } else {
        TalkTarget(TalkKind.SESSION, session.id, talkLabel(session))
    }

/** A session's row text: its label, its newest call, and the local clock time it happened. */
fun talkLine(session: TalkSession): String {
    val time = Instant.ofEpochSecond(session.at).atZone(ZoneId.systemDefault()).format(TALK_CLOCK)
    return "${talkLabel(session)} · ${session.call} · $time"
}

private fun talkLabel(session: TalkSession): String = session.label.ifBlank { session.id.take(8) }

/** A new session id: 32 lowercase hex digits. */
private fun newSessionId(): String = UUID.randomUUID().toString().replace("-", "")

/**
 * The epic's Talk list: one row per session in [sessions], each showing its [talkLine]; a tap opens its [talkTarget]
 * through [onOpen]. With a [repository], a "New session in" row starts a session there with [talkPrompt] for
 * [epicId], and opens the session it answers through [onOpen]. The row is disabled while its start runs. A refused
 * start shows its reason and opens nothing; a retry sends the same session id, so it starts nothing new.
 */
@Composable
fun TalkScreen(
    epicId: String,
    sessions: List<TalkSession>,
    repository: String,
    client: OrreryClient,
    onOpen: (TalkTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    var starting by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    var pendingId by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val name = repository.trimEnd('/').substringAfterLast('/')

    fun start() {
        val id = pendingId ?: newSessionId().also { pendingId = it }
        starting = true
        failure = null
        scope.launch {
            var answer: String? = null
            try {
                answer = withContext(Dispatchers.IO) { client.start(repository, talkPrompt(epicId), id) }
            } catch (error: IOException) {
                failure = error.message ?: "request failed"
            }
            starting = false
            answer?.let { sessionId ->
                pendingId = null
                onOpen(TalkTarget(TalkKind.SESSION, sessionId, name))
            }
        }
    }

    Column(
        modifier = modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        if (repository.isNotBlank()) {
            Button(
                onClick = { start() },
                enabled = !starting,
                modifier = Modifier.fillMaxWidth().testTag("talk-new"),
            ) {
                Text("New session in $name")
            }
        }
        failure?.let { reason ->
            Text(reason, modifier = Modifier.testTag("talk-error"), color = Mocha.Red)
        }
        sessions.forEach { session ->
            Text(
                talkLine(session),
                style = MaterialTheme.typography.bodyLarge,
                color = Mocha.Text,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("talk-session")
                    .clickable { onOpen(talkTarget(session)) }
                    .padding(vertical = 6.dp),
            )
        }
    }
}
