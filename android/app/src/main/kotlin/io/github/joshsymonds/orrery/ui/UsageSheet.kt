package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.UsageTotal
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.io.IOException
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.withContext

/** The state a conversation is in while a turn runs: a turn ends when the state moves from it to another. */
private const val WORKING = "working"

/** The order the sheet draws the four token kinds in, as the web panel does: input, cache write, cache read, output. */
private val KIND_ORDER = listOf(KindName.INPUT, KindName.CACHE_WRITE, KindName.CACHE_READ, KindName.OUTPUT)

/**
 * A session's usage read, which the head's readout and the Usage sheet both show. [totals] is the last read's answer:
 * null until a read answers, and null again when a read fails, so a failed read draws no readout. [open] is whether the
 * Usage sheet is open. [rememberSessionUsage] makes the reads.
 */
internal class SessionUsage {
    var totals: List<UsageTotal>? by mutableStateOf(null)
    var open: Boolean by mutableStateOf(false)

    /** Each request for a read. Requests made while a read is in flight coalesce into one more read after it. */
    internal val requests = Channel<Unit>(Channel.CONFLATED)

    /** The conversation's state at the last check, or null before the session is known. */
    internal var lastState: String? = null

    /**
     * The list cost the head's readout draws, in nano US dollars: the sum of the read's totals. Null when there is no
     * usage to draw, which is when the read has no totals or none has answered.
     */
    val readout: Long?
        get() = totals?.takeIf { it.isNotEmpty() }?.let { sessionSum(it).list }
}

/**
 * The usage of [session], read by the screen that shows it: once when the screen opens on the session, and again each
 * time the conversation's [state] moves from working to another state, so a turn's spend is in the readout once the
 * turn has ended. An empty [session] makes no read.
 */
@Composable
internal fun rememberSessionUsage(client: OrreryClient, session: String, state: String): SessionUsage {
    val usage = remember(client, session) { SessionUsage() }
    LaunchedEffect(usage) {
        while (true) {
            usage.requests.receive()
            usage.totals = readSessionUsage(client, session)
        }
    }
    LaunchedEffect(usage, state) {
        if (session.isEmpty()) return@LaunchedEffect
        val last = usage.lastState
        if (last == null || (last == WORKING && state != WORKING)) usage.requests.trySend(Unit)
        usage.lastState = state
    }
    return usage
}

/** One read of [session]'s usage on the IO dispatcher: its totals, or null when the read fails. */
private suspend fun readSessionUsage(client: OrreryClient, session: String): List<UsageTotal>? =
    withContext(Dispatchers.IO) {
        try {
            client.sessionUsage(session)
        } catch (e: IOException) {
            null
        }
    }

/**
 * Draws the Usage sheet while it is open, over the read's totals. A read that leaves no readout closes it, so the sheet
 * never shows a read the head does not.
 */
@Composable
internal fun SessionUsage.SheetIfOpen() {
    val shown = totals?.takeIf { it.isNotEmpty() }
    LaunchedEffect(shown == null) {
        if (shown == null) open = false
    }
    if (open && shown != null) {
        UsageSheet(totals = shown, onDismiss = { open = false })
    }
}

/**
 * The Usage sheet: a conversation's usage from [totals], as the web's Usage panel draws it. It reads the list total in
 * dollars, its requests, and what it was billed, with " · subscription" after the billed when that is 0 and the list
 * cost is above 0. Its four token kinds follow, each with its tokens and, when its dollars are known, its dollars. Its
 * model-and-agent rows come largest first, each with billed beside list and a bar of its share of the list cost.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsageSheet(totals: List<UsageTotal>, onDismiss: () -> Unit) {
    val sum = sessionSum(totals)
    val subscription = sum.billed == 0L && sum.list > 0L
    val kinds = kindsView(totals).kinds.sortedBy { KIND_ORDER.indexOf(it.kind) }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            Text(
                text = "Usage".inLabelCase(),
                style = OrreryTokens.Type.label,
                color = OrreryTokens.Ink.two,
                modifier = Modifier.testTag("usage-head"),
            )
            Text(
                text = dollars(sum.list),
                style = OrreryTokens.Type.title,
                color = OrreryTokens.Ink.one,
                modifier = Modifier.testTag("usage-list"),
            )
            Text(
                text = "at list price · ${"%,d".format(Locale.US, sum.requests)} requests",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("usage-requests"),
            )
            Text(
                text = "billed ${dollars(sum.billed)}" + if (subscription) " · subscription" else "",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("usage-billed"),
            )
            kinds.forEach { KindLine(it) }
            Text(
                text = "by model and agent".inLabelCase(),
                style = OrreryTokens.Type.label,
                color = OrreryTokens.Ink.two,
                modifier = Modifier.padding(top = OrreryTokens.Space.eight).testTag("usage-rows-head"),
            )
            modelRows(totals).forEachIndexed { index, row -> ModelLine(index, row) }
        }
    }
}

/** One token kind: its label, its tokens, and its dollars when they are known. */
@Composable
private fun KindLine(kind: KindFigure) {
    Row(
        Modifier
            .fillMaxWidth()
            .testTag("usage-kind:${kind.label}")
            .semantics(mergeDescendants = true) {},
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(text = kind.label, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        Text(text = tokens(kind.tokens), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        kind.dollars?.let { Text(text = dollars(it), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one) }
    }
}

/** One model-and-agent row: its model and agent, then a bar of its share, its billed and its list cost. */
@Composable
private fun ModelLine(index: Int, row: ModelRow) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.four)
            .testTag("usage-row:$index")
            .semantics(mergeDescendants = true) {},
    ) {
        Text(
            text = buildAnnotatedString {
                append(row.model)
                append(" ")
                withStyle(SpanStyle(color = OrreryTokens.Ink.three)) { append(row.agent) }
            },
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.one,
        )
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            ShareBar(share = row.share, modifier = Modifier.weight(1f))
            Text(
                text = "billed ${dollars(row.billed)}",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
            Text(text = dollars(row.list), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        }
    }
}

/** A row's share of the list cost, from 0 to 1, drawn as a bar filled to that share of its width. */
@Composable
private fun ShareBar(share: Double, modifier: Modifier) {
    Box(
        modifier
            .height(OrreryTokens.Space.four)
            .background(OrreryTokens.Surface.raised)
            .semantics { this[SemanticsProperties.ProgressBarRangeInfo] = ProgressBarRangeInfo(share.toFloat(), 0f..1f) },
    ) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(share.toFloat()).background(OrreryTokens.Role.primary))
    }
}
