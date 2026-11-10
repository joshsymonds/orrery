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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.ui.theme.Mocha

/**
 * The host's stats as meters, spread across the width. Each shows its uppercase label over its
 * value. A stat with a percent also gets a thin bar filled to that percent, coloured by level.
 */
@Composable
fun HostMeters(stats: List<HostStat>, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.fillMaxWidth().testTag("host-meters"),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        stats.forEach { stat ->
            Column(
                modifier = Modifier
                    .width(IntrinsicSize.Max)
                    .defaultMinSize(minWidth = 34.dp)
                    .testTag("meter:${stat.label}"),
            ) {
                Text(
                    text = stat.label.uppercase(),
                    fontSize = 9.sp,
                    letterSpacing = 0.1.em,
                    color = Mocha.Overlay1,
                    maxLines = 1,
                )
                Text(
                    text = stat.value,
                    fontSize = 12.sp,
                    color = Mocha.Text,
                    maxLines = 1,
                )
                stat.percent?.let { percent ->
                    Box(
                        modifier = Modifier
                            .padding(top = 3.dp)
                            .fillMaxWidth()
                            .height(2.dp)
                            .clip(RoundedCornerShape(2.dp))
                            .background(Mocha.Surface0)
                            .testTag("meter-bar:${stat.label}"),
                    ) {
                        Box(
                            modifier = Modifier
                                .fillMaxWidth((percent / 100.0).coerceIn(0.0, 1.0).toFloat())
                                .fillMaxHeight()
                                .background(levelColour(stat.level))
                                .testTag("meter-fill:${stat.label}"),
                        )
                    }
                }
            }
        }
    }
}

private fun levelColour(level: String): Color = when (level) {
    "warn" -> Mocha.Peach
    "crit" -> Mocha.Red
    else -> Mocha.Lavender
}
