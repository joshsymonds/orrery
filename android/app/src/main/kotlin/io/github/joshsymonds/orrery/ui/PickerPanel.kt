package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.PickRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.SocketTimeoutException
import java.util.UUID

private enum class PickerWork { IDLE, ASKING, RUNNING, STARTING }

/**
 * A pane or fresh row's start form: the folder it starts in, the first message it sends, and the session id that every
 * Start of this form sends, so a retry or an edit of the message starts one session.
 */
private data class StartForm(val dir: String, val message: String, val id: String)

/** The word each row's kind leads with: the action the row takes. */
private val KIND_WORDS = mapOf(
    "jump" to "go to",
    "resume" to "resume",
    "continue" to "continue",
    "pane" to "start in",
    "fresh" to "start in",
)

private const val PLACEHOLDER = "A directory, a session, or what you want to start"
private const val HINT = "Name a directory to start a session there, or say a few words about a session to find it."
private const val CHIP = "starts with your words"
private const val ASK_TIMED_OUT = "The search did not finish: the server did not answer in 90 seconds."
private const val ASK_UNAVAILABLE = "The search did not finish: the server could not answer."
private const val ASK_NOT_SET_UP = "New is not set up on this server."
private const val ASK_FAILED = "The search did not finish."
private const val RUN_STALE = "That result has gone stale. Ask again."
private const val RUN_EXPIRED = "It did not open: the result expired, or tmux is not running on the server."
private const val RUN_UNAVAILABLE = "It did not open: the server could not run it."
private const val RUN_FAILED = "It did not open."
private const val START_FAILED = "The session did not start."

/** The sentence a failed Ask shows, chosen by the call's failure. The server's own text is never shown. */
internal fun askSentence(error: IOException): String {
    if (error is NoAnswerException && error.cause is SocketTimeoutException) return ASK_TIMED_OUT
    return when ((error as? OrreryCallException)?.code) {
        Code.DEADLINE_EXCEEDED -> ASK_TIMED_OUT
        Code.UNAVAILABLE -> ASK_UNAVAILABLE
        Code.NOT_FOUND -> ASK_NOT_SET_UP
        else -> ASK_FAILED
    }
}

/** The sentence a failed Run shows, chosen by the call's failure. The server's own text is never shown. */
internal fun runSentence(error: IOException): String = when ((error as? OrreryCallException)?.code) {
    Code.NOT_FOUND -> RUN_STALE
    Code.FAILED_PRECONDITION -> RUN_EXPIRED
    Code.UNAVAILABLE -> RUN_UNAVAILABLE
    else -> RUN_FAILED
}

/** A new session id: 32 lowercase hex digits. */
private fun newSessionId(): String = UUID.randomUUID().toString().replace("-", "")

/**
 * The picker, a bottom sheet titled New session: text in a field, asked of the server when its IME Search action runs,
 * and the rows it answers, or a line saying nothing was found. Ask sends the non-blank text and shows "asking…" until
 * rows or a failure arrive. Tapping a row runs exactly that row and reports the pane it opened through [onOpened].
 * Tapping a pane or fresh row opens a start form for its full folder instead, which replaces the rows until its
 * "Other results" returns to them: its first message is the asked text when the row starts Claude with it, and empty
 * otherwise. A [startIn] that is not blank opens the sheet on the start form for that directory instead, with no rows.
 * Start sends that form and reports the session it answers through [onStarted]. A failure shows one fixed
 * sentence and leaves the sheet usable with what was typed. Dismissing the sheet calls [onClose].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PickerPanel(
    client: OrreryClient,
    onOpened: (String) -> Unit,
    onStarted: (String) -> Unit,
    onClose: () -> Unit,
    startIn: String? = null,
) {
    var text by remember { mutableStateOf("") }
    var askedText by remember { mutableStateOf("") }
    var rows by remember { mutableStateOf<List<PickRow>?>(null) }
    var work by remember { mutableStateOf(PickerWork.IDLE) }
    var failure by remember { mutableStateOf<String?>(null) }
    var form by remember {
        mutableStateOf(startIn?.takeIf { it.isNotBlank() }?.let { StartForm(dir = it, message = "", id = newSessionId()) })
    }
    val scope = rememberCoroutineScope()
    val canAsk = text.isNotBlank() && work == PickerWork.IDLE

    fun ask() {
        val asked = text
        askedText = asked
        work = PickerWork.ASKING
        failure = null
        rows = null
        scope.launch {
            var answer: List<PickRow>? = null
            try {
                answer = withContext(Dispatchers.IO) { client.ask(asked) }
            } catch (error: IOException) {
                failure = askSentence(error)
            }
            rows = answer
            work = PickerWork.IDLE
        }
    }

    fun run(row: PickRow) {
        work = PickerWork.RUNNING
        failure = null
        scope.launch {
            var pane: String? = null
            try {
                pane = withContext(Dispatchers.IO) { client.run(row.token) }
            } catch (error: IOException) {
                failure = runSentence(error)
            }
            work = PickerWork.IDLE
            pane?.let(onOpened)
        }
    }

    fun open(row: PickRow) {
        if (row.kind == "pane" || row.kind == "fresh") {
            failure = null
            form = StartForm(dir = row.dir, message = if (row.prompt) askedText else "", id = newSessionId())
        } else {
            run(row)
        }
    }

    fun start(current: StartForm) {
        work = PickerWork.STARTING
        failure = null
        scope.launch {
            var answer: String? = null
            try {
                answer = withContext(Dispatchers.IO) { client.start(current.dir, current.message, current.id) }
            } catch (error: IOException) {
                failure = START_FAILED
            }
            work = PickerWork.IDLE
            answer?.let { sessionId ->
                form = null
                onStarted(sessionId)
            }
        }
    }

    ModalBottomSheet(onDismissRequest = onClose) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text("New session", style = OrreryTokens.Type.title, modifier = Modifier.testTag("picker-title"))
            OutlinedTextField(
                value = text,
                onValueChange = { text = it },
                modifier = Modifier.fillMaxWidth().testTag("picker-text"),
                placeholder = { Text(PLACEHOLDER) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { if (canAsk) ask() }),
            )
            if (rows == null && work == PickerWork.IDLE && failure == null) {
                Text(HINT, style = OrreryTokens.Type.small, modifier = Modifier.testTag("picker-hint"))
            }
            when (work) {
                PickerWork.ASKING -> Text("asking…", modifier = Modifier.testTag("picker-working"))
                PickerWork.RUNNING -> Text("working...", modifier = Modifier.testTag("picker-working"))
                PickerWork.IDLE, PickerWork.STARTING -> Unit
            }
            failure?.let { reason ->
                Text(reason, modifier = Modifier.testTag("picker-error"), color = OrreryTokens.Role.bad)
            }
            form?.let { current ->
                Column(
                    modifier = Modifier.fillMaxWidth().testTag("start-form"),
                    verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text("Directory", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                    Text(
                        current.dir,
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Role.place,
                        modifier = Modifier.testTag("start-dir"),
                    )
                    Text("First message", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                    OutlinedTextField(
                        value = current.message,
                        onValueChange = { form = current.copy(message = it) },
                        modifier = Modifier.fillMaxWidth().testTag("start-message"),
                    )
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        TextButton(
                            onClick = { form = null },
                            enabled = work != PickerWork.STARTING,
                            modifier = Modifier.testTag("start-back"),
                        ) {
                            Text("Other results")
                        }
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                        ) {
                            if (work == PickerWork.STARTING) {
                                Text("starting…", style = OrreryTokens.Type.small, modifier = Modifier.testTag("start-starting"))
                            }
                            Button(
                                onClick = { start(current) },
                                enabled = current.message.isNotBlank() && work == PickerWork.IDLE,
                                modifier = Modifier.testTag("start-submit"),
                            ) {
                                Text("Start the session")
                            }
                        }
                    }
                }
            }
            if (form == null) {
                rows?.let { answered ->
                    if (answered.isEmpty()) {
                        Text(
                            "Nothing found for “$askedText”. Try a directory name.",
                            style = OrreryTokens.Type.small,
                            modifier = Modifier.testTag("picker-none"),
                        )
                    }
                    answered.forEachIndexed { index, row ->
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .testTag("picker-row:$index")
                                .clickable(enabled = work == PickerWork.IDLE) { open(row) }
                                .padding(vertical = OrreryTokens.Space.four + OrreryTokens.Space.two),
                        ) {
                            Text(KIND_WORDS[row.kind] ?: row.kind, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                            Text(row.dir, style = OrreryTokens.Type.small, color = OrreryTokens.Role.place)
                            if (row.label.isNotBlank()) {
                                Text(row.label, style = OrreryTokens.Type.body, color = OrreryTokens.Ink.one)
                            }
                            if (row.note.isNotBlank()) {
                                Text(row.note, style = OrreryTokens.Type.body, color = OrreryTokens.Ink.one)
                            }
                            if (row.prompt) {
                                Text(CHIP, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                            }
                        }
                    }
                }
            }
        }
    }
}
