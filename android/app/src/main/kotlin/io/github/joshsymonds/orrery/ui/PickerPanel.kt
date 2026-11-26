package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.ImeAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.HostStatus
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StartProposal
import io.github.joshsymonds.orrery.api.reachableOf
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.SocketTimeoutException
import java.util.UUID

private enum class StarterWork { IDLE, PROPOSING, STARTING }

private const val PLACEHOLDER = "What should it do? Say it in your words"
private const val HINT = "Press ⏎ and Orrery proposes where to run it."
private const val PROPOSING_TEXT = "Finding where to run it…"
private const val NONE_FIT = "No reachable host has a folder that fits this. Choose a host and type a folder."
private const val FOLDER_PLACEHOLDER = "A folder on that host"
private const val ASK_TIMED_OUT = "The search did not finish: the server did not answer in 90 seconds."
private const val ASK_UNAVAILABLE = "The search did not finish: the server could not answer."
private const val ASK_NOT_SET_UP = "New is not set up on this server."
private const val ASK_FAILED = "The search did not finish."
private const val START_FAILED = "The session did not start."

/** The sentence a failed proposal shows, chosen by the call's failure. The server's own text is never shown. */
internal fun askSentence(error: IOException): String {
    if (error is NoAnswerException && error.cause is SocketTimeoutException) return ASK_TIMED_OUT
    return when ((error as? OrreryCallException)?.code) {
        Code.DEADLINE_EXCEEDED -> ASK_TIMED_OUT
        Code.UNAVAILABLE -> ASK_UNAVAILABLE
        Code.NOT_FOUND -> ASK_NOT_SET_UP
        else -> ASK_FAILED
    }
}

/** A new session id: 32 lowercase hex digits. */
private fun newSessionId(): String = UUID.randomUUID().toString().replace("-", "")

/** The host status read, or null when the call fails or no status is served, so the Starter keeps what it showed. */
private fun statusOrNull(client: OrreryClient): HostStatus? =
    try {
        client.host()
    } catch (error: IOException) {
        null
    }

/** The names of the hosts whose status says they answer, in the server's order. */
private fun reachableNames(status: HostStatus?): List<String> =
    status?.hosts.orEmpty().filter { it.reachable }.map { it.name }

/**
 * The Starter, a bottom sheet titled Start a session, with the prompt first. Nothing is proposed until the owner
 * submits the prompt: then [OrreryClient.propose] answers the host and folder proposals, and "Finding where to run it…"
 * shows until it does. The proposals on reachable hosts show as a list under the prompt, the top one preselected and setting
 * the place below, and another can be picked. The place is a host, chosen from the reachable hosts that
 * [OrreryClient.host] lists, and a folder, from a proposal or typed. Typing either closes the list until a proposal is
 * picked again. A failed proposal shows one fixed sentence, keeps the prompt, and offers Try again and a typed place.
 * Start sends the prompt, the folder and the host to [OrreryClient.start] under the one session id this sheet keeps,
 * so a retry starts one session, and reports the session through [onStarted]. A [startIn] that is not blank opens the sheet
 * with that folder typed, on [startHost] or else the serving host. A failure shows one fixed sentence and leaves the
 * sheet usable with what was typed. Dismissing the sheet calls [onClose].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PickerPanel(
    client: OrreryClient,
    onStarted: (String) -> Unit,
    onClose: () -> Unit,
    startIn: String? = null,
    startHost: String? = null,
) {
    val typedAtOpen = startIn?.takeIf { it.isNotBlank() }
    var text by remember { mutableStateOf("") }
    var work by remember { mutableStateOf(StarterWork.IDLE) }
    var proposals by remember { mutableStateOf<List<StartProposal>?>(null) }
    var listOpen by remember { mutableStateOf(false) }
    var picked by remember { mutableStateOf<Int?>(null) }
    var host by remember { mutableStateOf(startHost.orEmpty()) }
    var folder by remember { mutableStateOf(typedAtOpen.orEmpty()) }
    var status by remember { mutableStateOf<HostStatus?>(null) }
    var hostMenu by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    var askFailed by remember { mutableStateOf(false) }
    val id = remember { newSessionId() }
    val scope = rememberCoroutineScope()
    val reachable = reachableNames(status)
    val canPropose = text.isNotBlank() && work == StarterWork.IDLE
    val canStart = text.isNotBlank() && folder.isNotBlank() && work == StarterWork.IDLE
    val showPlace = work != StarterWork.PROPOSING && (proposals != null || askFailed || typedAtOpen != null)

    LaunchedEffect(client) {
        val read = withContext(Dispatchers.IO) { statusOrNull(client) }
        status = read
        if (host.isEmpty() && read != null) host = read.host
    }

    fun propose() {
        val asked = text
        work = StarterWork.PROPOSING
        failure = null
        askFailed = false
        proposals = null
        listOpen = false
        scope.launch {
            val answered = try {
                withContext(Dispatchers.IO) { statusOrNull(client) to client.propose(asked) }
            } catch (error: IOException) {
                failure = askSentence(error)
                askFailed = true
                null
            }
            work = StarterWork.IDLE
            if (answered != null) {
                val (read, found) = answered
                status = read ?: status
                val fitting = found.filter { reachableOf(status?.hosts.orEmpty(), it.host) }
                proposals = fitting
                listOpen = fitting.isNotEmpty()
                val top = fitting.firstOrNull()
                picked = if (top != null) 0 else null
                if (top != null) {
                    host = top.host
                    folder = top.dir
                }
            }
        }
    }

    fun start() {
        val where = host
        val dir = folder
        val message = text
        work = StarterWork.STARTING
        failure = null
        askFailed = false
        scope.launch {
            val answer = try {
                withContext(Dispatchers.IO) { client.start(dir, message, id, where) }
            } catch (error: IOException) {
                failure = START_FAILED
                null
            }
            work = StarterWork.IDLE
            answer?.let(onStarted)
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
            Text("Start a session", style = OrreryTokens.Type.title, modifier = Modifier.testTag("picker-title"))
            OutlinedTextField(
                value = text,
                onValueChange = { text = it },
                modifier = Modifier.fillMaxWidth().testTag("picker-text"),
                placeholder = { Text(PLACEHOLDER) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { if (canPropose) propose() }),
            )
            if (proposals == null && !askFailed && work == StarterWork.IDLE && typedAtOpen == null) {
                Text(HINT, style = OrreryTokens.Type.small, modifier = Modifier.testTag("picker-hint"))
            }
            if (work == StarterWork.PROPOSING) {
                Text(PROPOSING_TEXT, modifier = Modifier.testTag("picker-working"))
            }
            failure?.let { reason ->
                Text(reason, modifier = Modifier.testTag("picker-error"), color = OrreryTokens.Role.bad)
            }
            proposals?.let { found ->
                if (found.isEmpty()) {
                    Text(NONE_FIT, style = OrreryTokens.Type.small, modifier = Modifier.testTag("picker-none"))
                } else {
                    TextButton(
                        onClick = { listOpen = !listOpen },
                        enabled = work == StarterWork.IDLE,
                        modifier = Modifier.testTag("picker-proposals"),
                    ) {
                        Text("Proposals (${found.size})")
                    }
                    if (listOpen) {
                        Column(modifier = Modifier.fillMaxWidth().testTag("picker-proposal-list")) {
                            found.forEachIndexed { index, proposal ->
                                Column(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .testTag("picker-proposal:$index")
                                        .selectable(
                                            selected = picked == index,
                                            enabled = work == StarterWork.IDLE,
                                            role = Role.RadioButton,
                                        ) {
                                            picked = index
                                            host = proposal.host
                                            folder = proposal.dir
                                            listOpen = false
                                        }
                                        .padding(vertical = OrreryTokens.Space.four + OrreryTokens.Space.two),
                                ) {
                                    Text(proposal.dir, style = OrreryTokens.Type.small, color = OrreryTokens.Role.place)
                                    HostLabel(proposal.host)
                                }
                            }
                        }
                    }
                }
            }
            if (showPlace) {
                Column(
                    modifier = Modifier.fillMaxWidth().testTag("start-form"),
                    verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    if (reachable.isNotEmpty()) {
                        Text("Host", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                        Box {
                            TextButton(
                                onClick = { hostMenu = true },
                                enabled = work == StarterWork.IDLE,
                                modifier = Modifier.testTag("picker-host"),
                            ) {
                                if (host.isNotEmpty()) HostLabel(host) else Text("Choose a host")
                            }
                            DropdownMenu(expanded = hostMenu, onDismissRequest = { hostMenu = false }) {
                                reachable.forEach { name ->
                                    DropdownMenuItem(
                                        text = { HostLabel(name) },
                                        onClick = {
                                            host = name
                                            picked = null
                                            listOpen = false
                                            hostMenu = false
                                        },
                                        modifier = Modifier.testTag("picker-host-option:$name"),
                                    )
                                }
                            }
                        }
                    }
                    Text("Folder", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                    OutlinedTextField(
                        value = folder,
                        onValueChange = {
                            folder = it
                            picked = null
                            listOpen = false
                        },
                        modifier = Modifier.fillMaxWidth().testTag("start-dir"),
                        placeholder = { Text(FOLDER_PLACEHOLDER) },
                        singleLine = true,
                        enabled = work != StarterWork.STARTING,
                    )
                    if (picked == null && folder.isNotBlank()) {
                        Text(
                            "The folder is checked on ${host.ifEmpty { "the server" }} when the session starts.",
                            style = OrreryTokens.Type.small,
                            modifier = Modifier.testTag("picker-checked"),
                        )
                    }
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight, Alignment.End),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        if (askFailed) {
                            TextButton(
                                onClick = { propose() },
                                enabled = canPropose,
                                modifier = Modifier.testTag("picker-retry"),
                            ) {
                                Text("Try again")
                            }
                        }
                        if (work == StarterWork.STARTING) {
                            Text(
                                "starting…",
                                style = OrreryTokens.Type.small,
                                modifier = Modifier.testTag("start-starting"),
                            )
                        }
                        Button(
                            onClick = { start() },
                            enabled = canStart,
                            modifier = Modifier.testTag("start-submit"),
                        ) {
                            Text("Start the session")
                        }
                    }
                }
            }
        }
    }
}
