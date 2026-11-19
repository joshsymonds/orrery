package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

// A stat's series colour, taken in order: the first stat is series one, and the sixth is series one again.
private val seriesColours = listOf(
    OrreryTokens.Series.one,
    OrreryTokens.Series.two,
    OrreryTokens.Series.three,
    OrreryTokens.Series.four,
    OrreryTokens.Series.five,
)

/**
 * The host's stats as meters, spread across the width. Each shows its uppercase label over its
 * value, and the value takes the level's colour at warn or crit. A stat with a percent also gets a
 * thin bar filled to that percent: in its series colour at ok, and in the level's colour at warn
 * or crit.
 */
@Composable
fun HostMeters(stats: List<HostStat>, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.fillMaxWidth().testTag("host-meters"),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        stats.forEachIndexed { index, stat ->
            Column(
                modifier = Modifier
                    .width(IntrinsicSize.Max)
                    .defaultMinSize(minWidth = OrreryTokens.Space.thirtyTwo + OrreryTokens.Space.two)
                    .testTag("meter:${stat.label}"),
            ) {
                Text(
                    text = stat.label.inLabelCase(),
                    style = OrreryTokens.Type.label,
                    color = OrreryTokens.Ink.three,
                    maxLines = 1,
                )
                Text(
                    text = stat.value,
                    style = OrreryTokens.Type.small,
                    color = valueColour(stat.level),
                    maxLines = 1,
                )
                stat.percent?.let { percent ->
                    Box(
                        modifier = Modifier
                            .padding(top = OrreryTokens.Space.two + OrreryTokens.Size.hairline)
                            .fillMaxWidth()
                            .height(OrreryTokens.Size.meterBarHeight)
                            .clip(RoundedCornerShape(OrreryTokens.Radius.round))
                            .background(OrreryTokens.Line.soft)
                            .testTag("meter-bar:${stat.label}"),
                    ) {
                        Box(
                            modifier = Modifier
                                .fillMaxWidth((percent / 100.0).coerceIn(0.0, 1.0).toFloat())
                                .fillMaxHeight()
                                .background(fillColour(index, stat.level))
                                .testTag("meter-fill:${stat.label}"),
                        )
                    }
                }
            }
        }
    }
}

// A warn or crit level takes the fill; any other level, which the server says is ok, keeps the stat's series colour.
private fun fillColour(index: Int, level: String): Color = when (level) {
    "warn" -> OrreryTokens.Level.warn
    "crit" -> OrreryTokens.Level.crit
    else -> seriesColours[index % seriesColours.size]
}

// A warn or crit level takes the value; any other level, which the server says is ok, keeps the ink.
private fun valueColour(level: String): Color = when (level) {
    "warn" -> OrreryTokens.Level.warn
    "crit" -> OrreryTokens.Level.crit
    else -> OrreryTokens.Ink.one
}
