package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
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
import io.github.joshsymonds.orrery.api.ContractCallResult
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractVersionRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.time.Instant
import java.time.ZoneId

private const val NEWER_NOTE = "A newer version came in. Read it before you accept."
private const val NO_LIVE_LINE = "Its conversation has ended: you can accept it or set it aside, but not change it."
private const val CONTRACT_UNAVAILABLE = "contract unavailable"

/** The contract was read by Contracts.Get but is gone when a write reads it again. */
private class ContractGone : Exception()

/**
 * A contract's record and whether an epic has its id. The epic is looked up for an accepted contract only, the one
 * state that offers Set aside when no epic has the id; for any other state [hasEpic] is false and unused.
 */
data class LoadedRecord(val record: ContractRecordRow, val hasEpic: Boolean)

/** Reads the record of [id] with its epic's presence, or null when Contracts.Get has no contract for [id]. */
internal fun readRecord(client: OrreryClient, id: String): LoadedRecord? {
    val record = client.contract(id) ?: return null
    val hasEpic = record.contract.state == "accepted" && client.epic(id) != null
    return LoadedRecord(record, hasEpic)
}

/** The words a failure shows: its own message, or the contract's unavailable words when it has none. */
private fun failureWords(message: String?): String = if (message.isNullOrEmpty()) CONTRACT_UNAVAILABLE else message

/**
 * A contract's record, as the contract document with its versions and its command bar. [loaded] is what [readRecord] read
 * for [id]; [onLoaded] is handed each record the screen should draw from now on. Accept re-reads the contract at the
 * press: it sends one Accept while the contract is still proposed at the version drawn, and otherwise sends nothing and
 * draws the newer answer with its note. Set aside and Reopen each send one call, and every call is refused or done in
 * place, with its reason shown on a refusal. [live] is whether the conversation that wrote the contract still runs: while
 * it does, a proposed contract's newest version takes About controls on its rows. [onTalk], when given, draws Talk about it.
 */
@Composable
fun ContractRecord(
    client: OrreryClient,
    id: String,
    loaded: LoadedRecord,
    onLoaded: (LoadedRecord) -> Unit,
    live: Boolean,
    writer: (String) -> String,
    onAbout: ((RowAbout) -> Unit)?,
    onTalk: (() -> Unit)?,
    modifier: Modifier = Modifier,
) {
    val record = loaded.record
    var read by remember(id) { mutableStateOf(emptySet<String>()) }
    var drawnVersion by remember(id) { mutableStateOf<Long?>(null) }
    var sending by remember(id) { mutableStateOf(false) }
    var refusal by remember(id) { mutableStateOf("") }
    var note by remember(id) { mutableStateOf("") }
    var versionsOpen by remember(id) { mutableStateOf(false) }
    var setAsideOpen by remember(id) { mutableStateOf(false) }
    var words by remember(id) { mutableStateOf("") }
    val scope = rememberCoroutineScope()

    val newest = newestVersion(record) ?: return
    val drawn = record.versions.firstOrNull { it.version == drawnVersion } ?: newest
    val isNewest = drawn.version == newest.version
    val proposed = record.contract.state == "proposed"
    val accepted = record.contract.state == "accepted"
    val key = readKey(id, drawn.version)
    val left = leftOf(openDecisions(drawn.decisions), record.contract.care, shown = key in read)
    val acceptOk = acceptable(record, drawn.version, read, sending)
    val canSetAside = isNewest && (proposed || (accepted && !loaded.hasEpic))
    val talking = if (live && proposed && isNewest) onAbout else null
    val markRead: (() -> Unit)? = if (proposed && left.unread) ({ read = read + key }) else null
    val versionsLabel = if (record.versions.size == 1) "1 version" else "${record.versions.size} versions"

    // send runs one call the viewer made. It takes the press before it launches, so a second press before the first is
    // drawn sends nothing. Nothing shows done until the call answers, and a failure's own message shows instead.
    fun send(call: suspend () -> Unit) {
        if (sending) return
        sending = true
        refusal = ""
        note = ""
        scope.launch {
            try {
                call()
            } catch (_: ContractGone) {
                refusal = CONTRACT_UNAVAILABLE
            } catch (error: IOException) {
                refusal = failureWords(error.message)
            } finally {
                sending = false
            }
        }
    }

    // reload draws the contract again at its newest version, once a write has changed it.
    suspend fun reload() {
        onLoaded(withContext(Dispatchers.IO) { readRecord(client, id) } ?: throw ContractGone())
        drawnVersion = null
    }

    fun accept() = send {
        val reread = withContext(Dispatchers.IO) { readRecord(client, id) } ?: throw ContractGone()
        when (acceptStep(drawn.version, reread.record)) {
            is AcceptStep.Redraw -> {
                onLoaded(reread)
                drawnVersion = null
                note = NEWER_NOTE
            }
            AcceptStep.Send -> when (val result = withContext(Dispatchers.IO) { client.acceptContract(id) }) {
                is ContractCallResult.Refused -> refusal = failureWords(result.reason)
                is ContractCallResult.Done -> reload()
            }
        }
    }

    fun setAside() = send {
        when (val result = withContext(Dispatchers.IO) { client.setAsideContract(id, words) }) {
            is ContractCallResult.Refused -> refusal = failureWords(result.reason)
            is ContractCallResult.Done -> {
                setAsideOpen = false
                words = ""
                reload()
            }
        }
    }

    fun reopen() = send {
        when (val result = withContext(Dispatchers.IO) { client.reopenContract(id) }) {
            is ContractCallResult.Refused -> refusal = failureWords(result.reason)
            is ContractCallResult.Done -> reload()
        }
    }

    Column(modifier = modifier.testTag("contract-record")) {
        if (!isNewest) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    "v${drawn.version} of ${newest.version} · not the newest",
                    style = OrreryTokens.Type.small,
                    modifier = Modifier.testTag("contract-older"),
                )
                TextButton(onClick = { drawnVersion = null }, modifier = Modifier.testTag("contract-read-newest")) {
                    Text("Read the newest", style = OrreryTokens.Type.small)
                }
            }
        }

        ContractBody(
            contract = drawn.contract,
            requirements = drawn.requirements,
            modifier = Modifier.weight(1f),
            decisions = drawn.decisions,
            record = recordLine(record, ZoneId.systemDefault()),
            marks = DocMarks(
                concerned = concernedRows(drawn.decisions),
                unread = proposed && left.unread,
                changedRows = drawn.changedRows.toSet(),
                changedSections = drawn.changedSections.toSet(),
                version = drawn.version,
            ),
            attending = proposed && isNewest,
            onAbout = talking,
            onIrreversibleShown = markRead,
            facts = {
                item {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                    ) {
                        Text(
                            "written by ${writer(record.versions.first().writer)}",
                            style = OrreryTokens.Type.small,
                            color = OrreryTokens.Ink.three,
                        )
                        TextButton(
                            onClick = { versionsOpen = !versionsOpen },
                            modifier = Modifier.testTag("contract-versions-toggle"),
                        ) {
                            Text(versionsLabel, style = OrreryTokens.Type.small)
                        }
                    }
                }
                if (versionsOpen) {
                    items(record.versions.reversed()) { version ->
                        VersionRow(
                            version = version,
                            newest = version.version == newest.version,
                            writer = writer,
                            onOpen = { drawnVersion = version.version },
                        )
                    }
                }
            },
        )

        Text(
            if (proposed) footText(left) else recordLine(record, ZoneId.systemDefault()),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.two,
            modifier = Modifier.testTag("contract-foot"),
        )
        FlowRow(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            if (onTalk != null) {
                OutlinedButton(onClick = onTalk, modifier = Modifier.testTag("contract-talk-about")) {
                    Text("Talk about it", style = OrreryTokens.Type.small)
                }
            }
            if (proposed) {
                Button(onClick = { accept() }, enabled = acceptOk, modifier = Modifier.testTag("contract-accept")) {
                    Text("Accept", style = OrreryTokens.Type.small)
                }
            }
            if (canSetAside && !setAsideOpen) {
                OutlinedButton(
                    onClick = { setAsideOpen = true },
                    enabled = !sending,
                    modifier = Modifier.testTag("contract-set-aside"),
                ) {
                    Text("Set aside…", style = OrreryTokens.Type.small)
                }
            }
            if (setAsideOpen) {
                Field(
                    value = words,
                    onValueChange = { words = it },
                    label = "Why you set it aside",
                    modifier = Modifier.testTag("contract-set-aside-words"),
                )
                Button(
                    onClick = { setAside() },
                    enabled = !sending && words.isNotBlank(),
                    modifier = Modifier.testTag("contract-set-aside-confirm"),
                ) {
                    Text("Set aside", style = OrreryTokens.Type.small)
                }
                OutlinedButton(
                    onClick = {
                        setAsideOpen = false
                        words = ""
                    },
                    enabled = !sending,
                    modifier = Modifier.testTag("contract-set-aside-cancel"),
                ) {
                    Text("Cancel", style = OrreryTokens.Type.small)
                }
            }
            if (isNewest && record.contract.state == "set_aside") {
                OutlinedButton(
                    onClick = { reopen() },
                    enabled = !sending,
                    modifier = Modifier.testTag("contract-reopen"),
                ) {
                    Text("Reopen", style = OrreryTokens.Type.small)
                }
            }
        }
        if (refusal.isNotEmpty()) {
            InlineNote(refusal, NoteTone.Error, Modifier.testTag("contract-refusal"))
        }
        if (note.isNotEmpty()) {
            InlineNote(note, NoteTone.Quiet, Modifier.testTag("contract-newer"))
        }
        if (!live && proposed) {
            InlineNote(NO_LIVE_LINE, NoteTone.Quiet, Modifier.testTag("contract-no-live"))
        }
    }
}

/** One version in the versions list: its number, its time, its writer, what it changed and its words. */
@Composable
private fun VersionRow(
    version: ContractVersionRow,
    newest: Boolean,
    writer: (String) -> String,
    onOpen: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .testTag("contract-version:${version.version}"),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Chip("v${version.version}", if (newest) ChipTone.New else ChipTone.Neutral)
            Text(
                Instant.ofEpochSecond(version.time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
            Text(writer(version.writer), style = OrreryTokens.Type.small)
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            version.changedRows.forEach { row -> IdChip(row) }
            Text(version.words, style = OrreryTokens.Type.small)
        }
    }
}
