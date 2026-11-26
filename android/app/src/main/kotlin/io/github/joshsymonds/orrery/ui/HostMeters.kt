package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/** Semantics key: the colour a meter's name, fill or value is drawn in, on that part's node. */
val MeterColourKey = SemanticsPropertyKey<Color>("MeterColour")

// A stat's series colour, taken in order: the first stat is series one, and the sixth is series one again.
private val seriesColours = listOf(
    OrreryTokens.Series.one,
    OrreryTokens.Series.two,
    OrreryTokens.Series.three,
    OrreryTokens.Series.four,
    OrreryTokens.Series.five,
)

// The narrowest a meter cell may be: two meter bars, as web's declared minimum.
private val MeterMinWidth: Dp = OrreryTokens.Size.meterBarWidth * 2

// The clear space at a cell's end, so one cell's bar does not run into the next cell's.
private val MeterGutter: Dp = OrreryTokens.Space.eight

/**
 * The host's stats as meters, as web's A4 band draws them, in a grid of equal cells. The column count is how many cells
 * of at least [MeterMinWidth] fit the band's width; every column is the same width, at most the meter cell, and the
 * cells wrap to further rows of those columns by width alone, so no value's text moves a cell. A cell narrower than the
 * meter cell line stacks its name, its value, then its bar across the cell; a wider cell keeps one line of name, bar
 * and value. A value is never ellipsized or cut: it may wrap inside its cell. A stat's hue is its series colour at ok
 * and its level's colour at warn or crit; the value keeps the ink at ok, takes the level's colour at warn or crit, and
 * is bold at crit.
 */
@Composable
fun HostMeters(stats: List<HostStat>, modifier: Modifier = Modifier) {
    BoxWithConstraints(modifier = modifier.fillMaxWidth().testTag("host-meters")) {
        val columns = (maxWidth / MeterMinWidth).toInt().coerceAtLeast(1)
        // Whole dp, so the columns still fit the band once each cell's width is rounded to pixels.
        val cellWidth = minOf(OrreryTokens.Layout.meterCell, (maxWidth / columns).value.toInt().dp)
        Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
            stats.withIndex().chunked(columns).forEach { row ->
                Row {
                    row.forEach { (index, stat) -> MeterCell(stat, index, cellWidth) }
                }
            }
        }
    }
}

/** One meter in its grid cell of [width]: stacked when the cell is under the meter cell line, one line otherwise. */
@Composable
private fun MeterCell(stat: HostStat, index: Int, width: Dp) {
    val hue = fillColour(index, stat.level)
    val ink = valueColour(stat.level)
    Box(modifier = Modifier.width(width).testTag("meter:${stat.label}")) {
        if (width < OrreryTokens.Layout.meterCellLine) {
            Column(
                modifier = Modifier.fillMaxWidth().padding(end = MeterGutter),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
            ) {
                MeterName(stat.label, hue)
                MeterValue(stat.value, stat.level, ink)
                stat.percent?.let { percent -> MeterBar(stat, percent, hue, Modifier.fillMaxWidth()) }
            }
        } else {
            Row(
                modifier = Modifier.fillMaxWidth().padding(end = MeterGutter),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                MeterName(stat.label, hue)
                stat.percent?.let { percent ->
                    MeterBar(stat, percent, hue, Modifier.width(OrreryTokens.Size.meterBarWidth))
                }
                MeterValue(stat.value, stat.level, ink, Modifier.weight(1f))
            }
        }
    }
}

/** A meter's name: its label in its hue, on one line. */
@Composable
private fun MeterName(label: String, hue: Color) {
    Text(
        text = label.inLabelCase(),
        modifier = Modifier.semantics { set(MeterColourKey, hue) },
        style = OrreryTokens.Type.label,
        color = hue,
        maxLines = 1,
    )
}

/** A meter's value, in its level's ink, which wraps within its cell rather than ellipsizing or cutting. */
@Composable
private fun MeterValue(value: String, level: String, ink: Color, modifier: Modifier = Modifier) {
    Text(
        text = value,
        modifier = modifier.semantics { set(MeterColourKey, ink) },
        style = if (level == "crit") {
            OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold)
        } else {
            OrreryTokens.Type.small
        },
        color = ink,
    )
}

/** A meter's bar, filled to its [percent] of the track, which is [modifier]'s width and the bar's height. */
@Composable
private fun MeterBar(stat: HostStat, percent: Double, hue: Color, modifier: Modifier) {
    Box(
        modifier = modifier
            .height(OrreryTokens.Size.meterBarHeight)
            .clip(RoundedCornerShape(OrreryTokens.Radius.round))
            .background(OrreryTokens.Line.soft)
            .testTag("meter-bar:${stat.label}"),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth((percent / 100.0).coerceIn(0.0, 1.0).toFloat())
                .fillMaxHeight()
                .clip(RoundedCornerShape(OrreryTokens.Radius.round))
                .background(hue)
                .testTag("meter-fill:${stat.label}")
                .semantics { set(MeterColourKey, hue) },
        )
    }
}

// A stat's hue, its name's and its fill's: a warn or crit level takes the level's colour; any other level, which the
// server says is ok, keeps the stat's series colour.
internal fun fillColour(index: Int, level: String): Color = when (level) {
    "warn" -> OrreryTokens.Level.warn
    "crit" -> OrreryTokens.Level.crit
    else -> seriesColours[index % seriesColours.size]
}

// A warn or crit level takes the value; any other level, which the server says is ok, keeps the ink.
internal fun valueColour(level: String): Color = when (level) {
    "warn" -> OrreryTokens.Level.warn
    "crit" -> OrreryTokens.Level.crit
    else -> OrreryTokens.Ink.one
}
