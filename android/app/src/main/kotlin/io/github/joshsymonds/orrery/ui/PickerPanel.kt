package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.PickRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.util.UUID

private enum class PickerWork { IDLE, ASKING, RUNNING, STARTING }

/** A pane or fresh row's start form: the folder it starts in and the first message it sends. */
private data class StartForm(val dir: String, val message: String)

/**
 * The last Start sent: its folder, its message, and the session id it sent with them. A Start of the same folder and
 * message sends that id again, so a retry after a refusal or a dropped answer starts nothing new.
 */
private data class PendingStart(val dir: String, val message: String, val id: String)

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

/** The message a failed call shows, or a fixed phrase when the failure carries none. */
private fun Exception.reason(): String = message ?: "request failed"

/** A new session id: 32 lowercase hex digits. */
private fun newSessionId(): String = UUID.randomUUID().toString().replace("-", "")

/**
 * The picker, a bottom sheet titled New session: text in a field, asked of the server when its IME Search action runs,
 * and the rows it answers. Ask sends the non-blank text and shows "asking…" until rows or a failure arrive. Tapping a
 * row runs exactly that row and reports the pane it opened through [onOpened]. Tapping a pane or fresh row opens a
 * start form for its full folder instead: its first message is the asked text when the row starts Claude with it, and
 * empty otherwise. Start sends that form and reports the session it answers through [onStarted]. A failure shows its
 * reason and leaves the sheet usable. Dismissing the sheet calls [onClose].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PickerPanel(
    client: OrreryClient,
    onOpened: (String) -> Unit,
    onStarted: (String) -> Unit,
    onClose: () -> Unit,
) {
    var text by remember { mutableStateOf("") }
    var askedText by remember { mutableStateOf("") }
    var rows by remember { mutableStateOf<List<PickRow>?>(null) }
    var work by remember { mutableStateOf(PickerWork.IDLE) }
    var failure by remember { mutableStateOf<String?>(null) }
    var form by remember { mutableStateOf<StartForm?>(null) }
    var pending by remember { mutableStateOf<PendingStart?>(null) }
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
                failure = error.reason()
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
                failure = error.reason()
            }
            work = PickerWork.IDLE
            pane?.let(onOpened)
        }
    }

    fun open(row: PickRow) {
        if (row.kind == "pane" || row.kind == "fresh") {
            failure = null
            form = StartForm(dir = row.dir, message = if (row.prompt) askedText else "")
        } else {
            run(row)
        }
    }

    fun start(current: StartForm) {
        val id = pending?.takeIf { it.dir == current.dir && it.message == current.message }?.id ?: newSessionId()
        pending = PendingStart(current.dir, current.message, id)
        work = PickerWork.STARTING
        failure = null
        scope.launch {
            var answer: String? = null
            try {
                answer = withContext(Dispatchers.IO) { client.start(current.dir, current.message, id) }
            } catch (error: IOException) {
                failure = error.reason()
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
            if (work != PickerWork.IDLE) {
                val label = if (work == PickerWork.ASKING) "asking…" else "working..."
                Text(label, modifier = Modifier.testTag("picker-working"))
            }
            failure?.let { reason ->
                Text(reason, modifier = Modifier.testTag("picker-error"), color = OrreryTokens.Role.bad)
            }
            form?.let { current ->
                Column(
                    modifier = Modifier.fillMaxWidth().testTag("start-form"),
                    verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text(
                        current.dir,
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Role.place,
                        modifier = Modifier.testTag("start-dir"),
                    )
                    OutlinedTextField(
                        value = current.message,
                        onValueChange = { form = current.copy(message = it) },
                        modifier = Modifier.fillMaxWidth().testTag("start-message"),
                    )
                    Button(
                        onClick = { start(current) },
                        enabled = current.message.isNotBlank() && work == PickerWork.IDLE,
                        modifier = Modifier.testTag("start-submit"),
                    ) {
                        Text("Start")
                    }
                }
            }
            rows?.forEachIndexed { index, row ->
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
