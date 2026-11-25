package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextOverflow
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

/** The accounts sheet's root node: the scrim and the sheet over the inbox, opened by a tap on the usage band. */
internal const val USAGE_SHEET_TAG = "usage-sheet"

/** The sheet's close control, which dismisses it. */
internal const val USAGE_SHEET_CLOSE_TAG = "usage-sheet-close"

/** The sheet's link to the accounts page, "Accounts and routes". */
internal const val USAGE_SHEET_ACCOUNTS_TAG = "usage-sheet-accounts"

/** The tag of [account]'s card in the sheet, by the account's name. */
internal fun usageCardTag(account: String): String = "usage-card:$account"

/** The tag of the Use button on [account]'s card, by the account's name. */
internal fun usageUseTag(account: String): String = "usage-use:$account"

/** The tag of one window column in [account]'s card, by the window's [claim]. */
internal fun usageSheetWindowTag(account: String, claim: String): String = "usage-sheet-window:$account:$claim"

/**
 * The accounts sheet a tap on the usage band opens, as the phone sheet does: a scrim over the inbox, and the sheet on the
 * side's ground, its rounded top corners and a grab bar, under its head with the title and Close. Each account in [rows]
 * is a card: its name, a line of who uses it, its plan or its limit ("limited until HH:MM"), and how long ago it was read
 * at [now] in unix seconds; then three equal window columns, each with its value, its bar and the countdown to its reset.
 * Accounts and routes calls [onOpenAccounts]. Close, a tap on the scrim, and the back gesture call [onClose]. Each card's
 * header ends with what its row offers: a Use button calls [onUse] with the row, "in use" when every context names the
 * account, and nothing when no context names an account.
 */
@Composable
internal fun UsageSheet(
    rows: List<UsageRow>,
    now: Long,
    onClose: () -> Unit,
    onOpenAccounts: () -> Unit,
    onUse: (UsageRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    BackHandler(onBack = onClose)
    Column(modifier = modifier.fillMaxSize().testTag(USAGE_SHEET_TAG)) {
        Box(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .background(Color.Black.copy(alpha = OrreryTokens.Alpha.scrim))
                .clickable(onClick = onClose),
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    OrreryTokens.Surface.overlay,
                    RoundedCornerShape(topStart = OrreryTokens.Radius.three, topEnd = OrreryTokens.Radius.three),
                )
                .verticalScroll(rememberScrollState())
                .padding(bottom = OrreryTokens.Space.sixteen),
        ) {
            Box(
                modifier = Modifier
                    .align(Alignment.CenterHorizontally)
                    .padding(top = OrreryTokens.Space.eight)
                    .size(OrreryTokens.Size.controlDesk, OrreryTokens.Size.meterBarHeight)
                    .clip(CircleShape)
                    .background(OrreryTokens.Line.firm),
            )
            SheetHead(onClose)
            rows.forEach { row -> AccountCard(row, now, onUse) }
            TextButton(
                onClick = onOpenAccounts,
                modifier = Modifier
                    .padding(horizontal = OrreryTokens.Space.eight)
                    .testTag(USAGE_SHEET_ACCOUNTS_TAG),
            ) {
                Text("Accounts and routes")
            }
        }
    }
}

/** The sheet's head: its title, and Close at the end of it. */
@Composable
private fun SheetHead(onClose: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.eight, top = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Claude accounts",
            modifier = Modifier.weight(1f),
            style = OrreryTokens.Type.title,
            color = OrreryTokens.Ink.one,
        )
        TextButton(onClick = onClose, modifier = Modifier.testTag(USAGE_SHEET_CLOSE_TAG)) {
            Text("Close")
        }
    }
}

/**
 * The line under an account's name: its limit when it is limited ("limited until HH:MM"), else who uses it ("used by"),
 * else its plan; then how long ago it was read. A line with no part is left out, and an account never read says so.
 */
private fun cardLineOf(row: UsageRow, now: Long): String {
    val subject = when {
        row.limited -> row.liftsAt?.let { "limited until ${usageClockOf(it)}" } ?: "limited"
        row.usedBy.isNotEmpty() -> "used by ${row.usedBy.joinToString(", ")}"
        else -> row.account.plan
    }
    val read = row.readAt?.let { "read ${agoText(it, now)}" } ?: "no reading"
    return listOf(subject, read).filter { it.isNotEmpty() }.joinToString(" · ")
}

/** One account's card: its name and line, its Use or "in use" at the end of its header, and its three window columns. */
@Composable
private fun AccountCard(row: UsageRow, now: Long, onUse: (UsageRow) -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .clip(RoundedCornerShape(OrreryTokens.Radius.three))
            .background(OrreryTokens.Surface.well)
            .testTag(usageCardTag(row.account.name))
            .padding(OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    row.account.name,
                    modifier = Modifier.weight(1f, fill = false),
                    style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    color = OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (row.limited) LimitedMark()
            }
            when (row.useOffer) {
                UseOffer.IN_USE -> Text("in use", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                UseOffer.USE -> TextButton(
                    onClick = { onUse(row) },
                    modifier = Modifier.testTag(usageUseTag(row.account.name)),
                ) {
                    Text("Use")
                }
                null -> Unit
            }
        }
        Text(
            cardLineOf(row, now),
            style = OrreryTokens.Type.small,
            color = if (row.stale) OrreryTokens.Ink.two else OrreryTokens.Ink.three,
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
        ) {
            USAGE_COLUMNS.forEachIndexed { index, column ->
                SheetWindow(
                    account = row.account.name,
                    window = row.windows.getOrNull(index),
                    column = column,
                    now = now,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

/** One window column of a card: its head, its value, its bar, and the countdown to its reset at [now]. */
@Composable
private fun SheetWindow(
    account: String,
    window: UsageWindow?,
    column: UsageColumn,
    now: Long,
    modifier: Modifier,
) {
    val level = levelColorOf(window?.level)
    val countdown = window?.resetAt?.let { "in ${untilWords(it - now)}" } ?: ""
    Column(
        modifier = modifier.testTag(usageSheetWindowTag(account, column.claim)),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text(column.head, style = OrreryTokens.Type.label, color = column.hue, maxLines = 1)
        Text(
            windowValueOf(window),
            style = OrreryTokens.Type.body,
            color = level ?: OrreryTokens.Ink.one,
            maxLines = 1,
        )
        MeterBar(fraction = fillOf(window), hue = level ?: column.hue)
        Text(countdown, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, maxLines = 1)
    }
}

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
