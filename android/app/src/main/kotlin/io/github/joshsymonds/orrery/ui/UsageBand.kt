package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.time.Instant
import java.time.ZoneId
import java.util.Locale

/** The usage band's root node: the table under the inbox head, which a tap on opens the accounts sheet. */
internal const val USAGE_BAND_TAG = "usage-band"

/** The tag of [account]'s row in the band, by the account's name. */
internal fun usageRowTag(account: String): String = "usage-row:$account"

/** One window column of the band and the sheet: the [claim] it draws, the [head] it is headed by and its series [hue]. */
internal data class UsageColumn(val claim: String, val head: String, val hue: Color)

/** The window columns in the order the band and the sheet draw them: 5h, 7d, and Fable, which is the 7d_oi claim. */
internal val USAGE_COLUMNS: List<UsageColumn> = listOf(
    UsageColumn(claim = "5h", head = "5h", hue = OrreryTokens.Series.one),
    UsageColumn(claim = "7d", head = "7d", hue = OrreryTokens.Series.two),
    UsageColumn(claim = "7d_oi", head = "Fable", hue = OrreryTokens.Series.three),
)

/** The width of each window column: the meter bar's width, so a value lines up over its bar. */
private val WINDOW_WIDTH: Dp = OrreryTokens.Size.meterBarWidth

/** The width of the last column, which holds a reset, a lift or an age. */
private val LAST_WIDTH: Dp = OrreryTokens.Space.sixteen * 4

/** The head of the last column. */
private const val LAST_HEAD = "resets"

/** A dollar amount in nano US dollars, as "$47.43", with thousands grouped: "$1,287.00". */
internal fun usageDollars(nano: Long): String = String.format(Locale.US, "\$%,.2f", nano / 1_000_000_000.0)

/** The band's spend line: what the seats billed over the week, and what they cost at list price. */
internal fun billedLine(totals: SpendTotals): String =
    "Billed 7d ${usageDollars(totals.billed)} list ${usageDollars(totals.list)}"

/** The time from now until [seconds], as its largest whole unit: "3d", "1h", or "12m", never under a minute. */
internal fun untilWords(seconds: Long): String = when {
    seconds >= SECONDS_PER_DAY -> "${seconds / SECONDS_PER_DAY}d"
    seconds >= SECONDS_PER_HOUR -> "${seconds / SECONDS_PER_HOUR}h"
    else -> "${maxOf(seconds, SECONDS_PER_MINUTE) / SECONDS_PER_MINUTE}m"
}

/** The clock time of [at], in unix seconds, in the device zone, as HH:mm. */
internal fun usageClockOf(at: Long): String = Instant.ofEpochSecond(at).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)

/** A row's last column: the time until a 7d reset, until a limited account lifts, or how long ago a stale read was taken. */
internal fun lastWordsOf(last: LastColumn?, now: Long): String = when (last) {
    null -> ""
    is LastColumn.Lift -> last.at?.let { untilWords(it - now) } ?: "limited"
    is LastColumn.Age -> agoText(now - last.seconds, now)
    is LastColumn.Reset -> untilWords(last.at - now)
}

/** The text of a window's value: its rounded percent, or a dash when the reading gives none. */
internal fun windowValueOf(window: UsageWindow?): String = window?.percent?.let { "$it%" } ?: "—"

/** The level colour of a window, when it is warn or crit; null for ok, which draws in the series colour. */
internal fun levelColorOf(level: UsageLevel?): Color? = when (level) {
    UsageLevel.WARN -> OrreryTokens.Level.warn
    UsageLevel.CRIT -> OrreryTokens.Level.crit
    UsageLevel.OK, null -> null
}

/** The fraction of a meter bar that a window's percent fills, from none to all. */
internal fun fillOf(window: UsageWindow?): Float = ((window?.percent ?: 0) / PERCENT).coerceIn(0f, 1f)

private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L
private const val SECONDS_PER_DAY = 86_400L
private const val PERCENT = 100f

/**
 * The usage band under the inbox head: a table on the well, with the heads 5h, 7d, Fable and resets, one row for each
 * account in [rows], and the spend line when [spend] is known. Each window is a [WINDOW_WIDTH] column with its value
 * right-aligned over a bar that fills the column; the account in use has a filled dot, a limited account a limited mark,
 * and its windows are crit. The last column is the 7d reset, the lift of a limited account, or the age of a stale read,
 * all read at [now] in unix seconds. A tap anywhere on the band calls [onOpen].
 */
@Composable
internal fun UsageBand(
    rows: List<UsageRow>,
    spend: SpendTotals?,
    now: Long,
    onOpen: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .clip(RoundedCornerShape(OrreryTokens.Radius.three))
            .background(OrreryTokens.Surface.well)
            .testTag(USAGE_BAND_TAG)
            .clickable(onClick = onOpen)
            .padding(OrreryTokens.Space.eight),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        BandHeads()
        rows.forEach { row -> BandRow(row, now) }
        spend?.let { totals ->
            Text(
                billedLine(totals),
                modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.four),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
        }
    }
}

/** The band's heads: the account name's head, the three window heads in their series colours, and resets. */
@Composable
private fun BandHeads() {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            "Claude",
            modifier = Modifier.weight(1f),
            style = OrreryTokens.Type.label,
            color = OrreryTokens.Ink.three,
        )
        USAGE_COLUMNS.forEach { column ->
            Box(modifier = Modifier.width(WINDOW_WIDTH), contentAlignment = Alignment.CenterEnd) {
                Text(column.head, style = OrreryTokens.Type.label, color = column.hue, maxLines = 1)
            }
        }
        Box(modifier = Modifier.width(LAST_WIDTH), contentAlignment = Alignment.CenterEnd) {
            Text(LAST_HEAD, style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three, maxLines = 1)
        }
    }
}

/** One account's row: its dot, name and limited mark, its three windows, and its last column. */
@Composable
private fun BandRow(row: UsageRow, now: Long) {
    Row(
        modifier = Modifier.fillMaxWidth().testTag(usageRowTag(row.account.name)),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Row(
            modifier = Modifier.weight(1f),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            AccountDot(inUse = row.inUse)
            Text(
                row.account.name,
                modifier = Modifier.weight(1f, fill = false),
                style = OrreryTokens.Type.small.copy(
                    fontWeight = if (row.inUse) OrreryTokens.Font.Weight.bold else OrreryTokens.Font.Weight.regular,
                ),
                color = if (row.inUse) OrreryTokens.Ink.one else OrreryTokens.Ink.two,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (row.limited) LimitedMark()
        }
        USAGE_COLUMNS.forEachIndexed { index, column ->
            WindowCell(window = row.windows.getOrNull(index), column = column)
        }
        Box(modifier = Modifier.width(LAST_WIDTH), contentAlignment = Alignment.CenterEnd) {
            Text(
                lastWordsOf(row.last, now),
                style = OrreryTokens.Type.small,
                color = if (row.stale) OrreryTokens.Ink.two else OrreryTokens.Ink.three,
                maxLines = 1,
            )
        }
    }
}

/** The dot before an account's name: filled in the primary colour when the account is in use, else a ring. */
@Composable
private fun AccountDot(inUse: Boolean) {
    val description = if (inUse) "in use" else "not in use"
    val shape = CircleShape
    val dot = Modifier.size(OrreryTokens.Size.dot).semantics { contentDescription = description }
    if (inUse) {
        Box(modifier = dot.clip(shape).background(OrreryTokens.Role.primary))
    } else {
        Box(modifier = dot.border(OrreryTokens.Size.hairline, OrreryTokens.Line.glyph, shape))
    }
}

/** The limited mark after a limited account's name: two bars, as the wireframe's pause glyph draws them. */
@Composable
internal fun LimitedMark() {
    Canvas(Modifier.size(OrreryTokens.Space.twelve).semantics { contentDescription = "limited" }) {
        val unit = size.width / MARK_GRID
        val stroke = MARK_STROKE * unit
        val color = OrreryTokens.State.limited
        drawLine(color, Offset(6f * unit, 4.5f * unit), Offset(6f * unit, 11.5f * unit), stroke, StrokeCap.Round)
        drawLine(color, Offset(10f * unit, 4.5f * unit), Offset(10f * unit, 11.5f * unit), stroke, StrokeCap.Round)
    }
}

private const val MARK_GRID = 16f
private const val MARK_STROKE = 1.5f

/** One window column of a band row: its value right-aligned over a bar that fills the column. */
@Composable
private fun WindowCell(window: UsageWindow?, column: UsageColumn) {
    val level = levelColorOf(window?.level)
    Column(
        modifier = Modifier.width(WINDOW_WIDTH),
        horizontalAlignment = Alignment.End,
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        Text(
            windowValueOf(window),
            style = OrreryTokens.Type.small,
            color = level ?: OrreryTokens.Ink.one,
            maxLines = 1,
        )
        MeterBar(fraction = fillOf(window), hue = level ?: column.hue)
    }
}

/** A meter bar [OrreryTokens.Size.meterBarHeight] tall on the soft line colour, filled to [fraction] in [hue]. */
@Composable
internal fun MeterBar(fraction: Float, hue: Color, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(OrreryTokens.Size.meterBarHeight)
            .clip(CircleShape)
            .background(OrreryTokens.Line.soft),
    ) {
        Box(
            modifier = Modifier
                .fillMaxHeight()
                .fillMaxWidth(fraction)
                .background(hue),
        )
    }
}
