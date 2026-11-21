package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.FlowRow
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
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
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
 * The machine band, the one strip a screen draws the host's machine in: a full-width band of its own under the title
 * row, on surface.side, the inbox's ground, one step deeper than the goal's well, with an edge above and below it. It
 * leads with the host's label, the host icon and its name, then the host's stats as [HostMeters], as web's A4 band
 * draws them. A host other than the serving one is labelled with its [otherHost] name and says it has no meters. A
 * host read with no stats draws its label alone. Null [stats], a host not read, with no other host draws nothing, not
 * even the band.
 */
@Composable
fun MachineBand(host: String, stats: List<HostStat>?, otherHost: String?) {
    if (otherHost == null && stats == null) return
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("machine-band")
            .background(OrreryTokens.Surface.side)
            .drawBehind {
                val edge = OrreryTokens.Size.hairline.toPx()
                drawLine(OrreryTokens.Line.soft, Offset(0f, edge / 2), Offset(size.width, edge / 2), edge)
                drawLine(
                    OrreryTokens.Line.soft,
                    Offset(0f, size.height - edge / 2),
                    Offset(size.width, size.height - edge / 2),
                    edge,
                )
            }
            .padding(horizontal = OrreryTokens.Layout.pagePadPhone, vertical = OrreryTokens.Space.eight),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
    ) {
        HostLabel(otherHost ?: host)
        if (otherHost != null) {
            Text(
                text = "on another host: no meters",
                modifier = Modifier.weight(1f).testTag("head-other-host"),
                color = OrreryTokens.Ink.four,
                style = OrreryTokens.Type.small,
            )
        } else if (!stats.isNullOrEmpty()) {
            Box(modifier = Modifier.weight(1f).testTag("head-meters")) {
                HostMeters(stats)
            }
        }
    }
}

/** The machine band's label: the host icon in the faint ink, then the host's name in bold. */
@Composable
private fun HostLabel(name: String) {
    Row(
        modifier = Modifier.testTag("machine-host").semantics(mergeDescendants = true) {},
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        BlockGlyph(BlockIcon.Host, OrreryTokens.Ink.four)
        Text(
            text = name,
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
    }
}

/**
 * The host's stats as meters, as web's A4 band draws them: each on one line, its uppercase name in its hue, a
 * bar filled to its percent when it has one, then its value, and the meters wrap by whole meters. A stat's hue is its
 * series colour at ok and its level's colour at warn or crit; the value keeps the ink at ok, takes the level's colour
 * at warn or crit, and is bold at crit.
 */
@Composable
fun HostMeters(stats: List<HostStat>, modifier: Modifier = Modifier) {
    FlowRow(
        modifier = modifier.fillMaxWidth().testTag("host-meters"),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        stats.forEachIndexed { index, stat ->
            val hue = fillColour(index, stat.level)
            Row(
                modifier = Modifier.testTag("meter:${stat.label}"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(text = stat.label.inLabelCase(), style = OrreryTokens.Type.label, color = hue, maxLines = 1)
                stat.percent?.let { percent ->
                    Box(
                        modifier = Modifier
                            .width(OrreryTokens.Size.meterBarWidth)
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
                                .testTag("meter-fill:${stat.label}"),
                        )
                    }
                }
                Text(
                    text = stat.value,
                    style = if (stat.level == "crit") {
                        OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold)
                    } else {
                        OrreryTokens.Type.small
                    },
                    color = valueColour(stat.level),
                    maxLines = 1,
                )
            }
        }
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
