package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.PendingItem
import io.github.joshsymonds.orrery.api.QueuedItem
import io.github.joshsymonds.orrery.api.WaitingItem
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.io.IOException
import java.time.ZoneId
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val ESCAPE = "Escape"
private const val ABOUT_TAG = "dock-about"
private const val NOT_ABOUT_TAG = "dock-not-about"
private const val NOT_ABOUT_DESCRIPTION = "Not about this"
private const val WAITS_TAG = "dock-waits"
private const val STEP_MORE = "Stop this step…"
private const val SESSION_MORE = "Stop this session…"

/** The note when a session's Stop did not reach the session, as the Stop's outcome reads elsewhere in the app. */
private const val STOP_NOT_SENT = "not sent"

/** The states a session's Stop is offered in, the ones it is still working or waiting in. */
private val LIVE_STATES = setOf("working", "waiting")

/** The note when an interrupt did not reach the session or pane, in the web's words (web/src/app.tsx interruptFailedText). */
private const val INTERRUPT_FAILED = "could not interrupt"

/**
 * The dock under a conversation: the state line, the waits line, the tray of queued messages, the about line and the
 * box, on one box railed in the state's colour. Each control calls the server on the IO dispatcher, and an IOException
 * from a call is handled where the call is made. The texts a call hands back go into [draft] in send order; what a
 * control could not do is said in a note above the box, or on its row. [onSend] hears each tap on Send, before the
 * request goes out.
 */
@Composable
fun Dock(
    facts: DockFacts,
    nowSeconds: Long,
    client: OrreryClient,
    draft: DockDraft,
    send: (String) -> Unit,
    modifier: Modifier = Modifier,
    sessionId: String? = null,
    paneId: String? = null,
    queued: List<QueuedItem> = emptyList(),
    waiting: List<WaitingItem> = emptyList(),
    pending: List<PendingItem> = emptyList(),
    stepCalls: StepCalls? = null,
    about: String? = null,
    zone: ZoneId = ZoneId.systemDefault(),
    maxFieldHeight: Dp = Dp.Unspecified,
    onDraftPresent: (Boolean) -> Unit = {},
    onSend: () -> Unit = {},
    newPromptId: () -> String = { UUID.randomUUID().toString() },
) {
    val dockKey = sessionId ?: paneId ?: facts.name
    val scope = rememberCoroutineScope()
    val line = stateLine(facts, nowSeconds, zone)
    val rowNotes = remember(dockKey) { mutableStateMapOf<String, String>() }
    val hiddenRows = remember(dockKey) { mutableStateListOf<String>() }
    var note by remember(dockKey) { mutableStateOf<String?>(null) }
    var waitsHidden by remember(dockKey) { mutableStateOf<List<PendingItem>?>(null) }
    var aboutCleared by remember(dockKey, about) { mutableStateOf(false) }
    var sendNowInFlight by remember(dockKey) { mutableStateOf(false) }
    val currentPending by rememberUpdatedState(pending)

    val rows = when (facts.kind) {
        DockKind.PANE -> waiting.mapIndexed { index, item ->
            DockTrayRow(key = "pane-$index", text = item.text, cancelled = item.canceled, waits = "", promptId = "")
        }
        DockKind.SESSION -> queued
            .filter { (it.state == "queued" || it.state == "discarded") && it.promptId !in hiddenRows }
            .map { DockTrayRow(it.promptId, it.text, it.state == "discarded", it.waits, it.promptId) }
        DockKind.SHELL, DockKind.STEP -> emptyList()
    }
    val waits = if (waitsHidden == pending) "" else waitsLine(pending.map { it.waits }, facts.putOff)
    val edge = if (facts.step?.held == true) ComposeEdge.HELD else composeEdgeOf(facts.state)

    fun interrupt() {
        note = null
        when (facts.kind) {
            DockKind.SESSION -> sessionId?.let { id ->
                scope.launch {
                    val texts = callOrNull { client.interrupt(id) }
                    if (texts == null) {
                        note = INTERRUPT_FAILED
                    } else if (texts.isNotEmpty()) {
                        draft.append(texts)
                        note = DockLines.backInBox(texts.size)
                    }
                }
            }
            DockKind.PANE -> paneId?.let { id ->
                scope.launch {
                    if (callOrNull { client.sendKeys(id, listOf(ESCAPE), null) } == null) note = INTERRUPT_FAILED
                }
            }
            DockKind.STEP -> scope.launch { withContext(Dispatchers.IO) { stepCalls?.hold() } }
            DockKind.SHELL -> Unit
        }
    }

    fun stopSession(id: String) {
        note = null
        scope.launch {
            note = withContext(Dispatchers.IO) {
                try {
                    client.stop(id)
                    null
                } catch (error: OrreryCallException) {
                    if (error.code == Code.NOT_FOUND) null else STOP_NOT_SENT
                } catch (_: IOException) {
                    STOP_NOT_SENT
                }
            }
        }
    }

    fun resume(id: String) {
        note = null
        scope.launch {
            note = withContext(Dispatchers.IO) {
                try {
                    client.resume(id)
                    null
                } catch (error: OrreryCallException) {
                    DockLines.resumeRefused(error.message.orEmpty())
                } catch (_: IOException) {
                    DockLines.resumeUnknown
                }
            }
        }
    }

    fun sendNowTexts(id: String) {
        if (sendNowInFlight) return
        sendNowInFlight = true
        note = null
        scope.launch {
            try {
                val held = withContext(Dispatchers.IO) {
                    sendNow(
                        interrupt = { client.interrupt(id) },
                        say = { text -> client.say(id, text, newPromptId()) },
                    )
                }
                if (held.isNotEmpty()) {
                    draft.append(held)
                    note = DockLines.sendNowHeld(held.size)
                }
            } catch (_: OrreryCallException) {
                note = DockLines.sendRefused
            } catch (_: IOException) {
                note = DockLines.sendUnknown
            } finally {
                sendNowInFlight = false
            }
        }
    }

    fun takeBack(id: String, promptId: String) {
        scope.launch {
            val texts = callOrNull { client.withdraw(id, promptId) }
            when {
                texts == null -> rowNotes[promptId] = DockLines.takeBackFailed
                texts.isEmpty() -> rowNotes[promptId] = DockLines.alreadyTook
                else -> {
                    rowNotes.remove(promptId)
                    draft.append(texts)
                }
            }
        }
    }

    fun takeAllBack(id: String) {
        note = null
        scope.launch {
            val text = callOrNull { client.pullQueued(id) }
            if (text == null) {
                note = DockLines.takeAllBackFailed
            } else if (text.isNotEmpty()) {
                draft.append(listOf(text))
            }
        }
    }

    fun putBack(promptId: String) {
        val row = queued.firstOrNull { it.promptId == promptId } ?: return
        draft.append(listOf(row.text))
        hiddenRows.add(promptId)
    }

    val stop = facts.step?.let { step ->
        DockStop(STEP_MORE, step.label) { scope.launch { withContext(Dispatchers.IO) { stepCalls?.stop() } } }
    } ?: sessionId?.takeIf { facts.kind == DockKind.SESSION && facts.state in LIVE_STATES }?.let { id ->
        DockStop(SESSION_MORE, facts.name) { stopSession(id) }
    }

    val sendAndHideWaits: (String) -> Unit = { text ->
        send(text)
        waitsHidden = currentPending
    }

    val shape = RoundedCornerShape(OrreryTokens.Radius.three)
    Column(
        modifier = modifier
            .testTag("dock")
            .fillMaxWidth()
            .clip(shape)
            .background(OrreryTokens.Surface.well)
            .drawBehind {
                drawRect(
                    color = railColour(line.tone),
                    size = Size(OrreryTokens.Size.rail.toPx(), size.height),
                )
            }
            .border(OrreryTokens.Size.hairline, OrreryTokens.Line.firm, shape),
    ) {
        Column(modifier = Modifier.fillMaxWidth().padding(start = OrreryTokens.Size.rail)) {
            DockStateLine(
                line = line,
                onInterrupt = { interrupt() },
                onResume = { sessionId?.let { resume(it) } },
                stop = stop,
            )
            if (waits.isNotEmpty()) {
                DockDivider()
                Text(
                    text = waits,
                    modifier = Modifier
                        .testTag(WAITS_TAG)
                        .fillMaxWidth()
                        .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.four),
                    color = OrreryTokens.Role.engine,
                    style = OrreryTokens.Type.small,
                )
            }
            if (rows.isNotEmpty()) {
                DockDivider()
                DockTray(
                    pane = facts.kind == DockKind.PANE,
                    rows = rows,
                    working = facts.state == "working",
                    notes = rowNotes,
                    onTakeBack = { promptId -> sessionId?.let { takeBack(it, promptId) } },
                    onPutBack = { promptId -> putBack(promptId) },
                    onTakeAllBack = { paneId?.let { takeAllBack(it) } },
                    onSendNow = if (facts.kind == DockKind.SESSION && sessionId != null && !sendNowInFlight) {
                        { sendNowTexts(sessionId) }
                    } else {
                        null
                    },
                )
            }
            if (about != null && !aboutCleared) {
                DockDivider()
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(start = OrreryTokens.Space.twelve, end = OrreryTokens.Space.four),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text(
                        text = "about $about",
                        modifier = Modifier.weight(1f).testTag(ABOUT_TAG),
                        color = OrreryTokens.Ink.two,
                        style = OrreryTokens.Type.small,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    NotAboutControl(onClick = { aboutCleared = true })
                }
            }
            DockBox(
                draft = draft,
                client = client,
                paneId = paneId ?: sessionId ?: facts.name,
                edge = edge,
                maxFieldHeight = maxFieldHeight,
                hint = dockPlaceholder(facts),
                onDraftPresent = onDraftPresent,
                status = (note ?: stepCalls?.outcome)?.let(::noteLine),
                onSend = onSend,
                send = sendAndHideWaits,
            )
        }
    }
}

/**
 * The rail's colour: the colour the state line gives the state's word. Stopped and idle have no state colour, so their
 * rail is the firm line's.
 */
private fun railColour(tone: String): Color = when (tone) {
    "working" -> OrreryTokens.State.working
    "waiting" -> OrreryTokens.State.waiting
    "held" -> OrreryTokens.State.held
    "done" -> OrreryTokens.State.done
    "interrupted", "interrupting" -> OrreryTokens.State.interrupted
    "failed" -> OrreryTokens.State.failed
    else -> OrreryTokens.Line.firm
}

/** The hairline between two parts of the dock. */
@Composable
private fun DockDivider() {
    Box(modifier = Modifier.fillMaxWidth().height(OrreryTokens.Size.hairline).background(OrreryTokens.Line.soft))
}

private fun noteLine(text: String): @Composable () -> Unit = { DockNote(text) }

@Composable
private fun DockNote(text: String) {
    Text(
        text = text,
        modifier = Modifier.fillMaxWidth(),
        color = OrreryTokens.Ink.three,
        style = OrreryTokens.Type.small,
    )
}

/** The about line's control, an X drawn in the ink colour with no text: "Not about this". */
@Composable
private fun NotAboutControl(onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag(NOT_ABOUT_TAG)
            .semantics { contentDescription = NOT_ABOUT_DESCRIPTION }
            .size(OrreryTokens.Size.controlTouch)
            .clip(RoundedCornerShape(OrreryTokens.Radius.one))
            .clickable(role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.size(OrreryTokens.Size.icon)) {
            val stroke = OrreryTokens.Size.hairline.toPx()
            drawLine(
                color = OrreryTokens.Ink.two,
                start = Offset(0f, 0f),
                end = Offset(size.width, size.height),
                strokeWidth = stroke,
                cap = StrokeCap.Round,
            )
            drawLine(
                color = OrreryTokens.Ink.two,
                start = Offset(size.width, 0f),
                end = Offset(0f, size.height),
                strokeWidth = stroke,
                cap = StrokeCap.Round,
            )
        }
    }
}

/** Runs [call] on the IO dispatcher and returns its answer, or null when the call throws an IOException. */
private suspend fun <T : Any> callOrNull(call: () -> T): T? = withContext(Dispatchers.IO) {
    try {
        call()
    } catch (_: IOException) {
        null
    }
}
