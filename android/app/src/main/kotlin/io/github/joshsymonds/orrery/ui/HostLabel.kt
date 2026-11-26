package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Semantics key: the colour an unreachable host's dashed ring is drawn in, on the ring's node. */
val HostRingColourKey = SemanticsPropertyKey<Color>("HostRingColour")

/**
 * A host's label: the host icon in the faint ink, then the host's [name] in bold, on one line, its node tagged
 * "host-label". [modifier] is applied to that node ahead of its own tag, so a caller's test tag names the label's node
 * instead. When [unreachable], the word "unreachable" follows it, with a dashed hollow ring before the word, both in the
 * grey ink, so a host that did not answer reads as such.
 */
@Composable
internal fun HostLabel(name: String, unreachable: Boolean = false, modifier: Modifier = Modifier) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Row(
            modifier = modifier.then(Modifier.testTag("host-label").semantics(mergeDescendants = true) {}),
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
        if (unreachable) {
            Row(
                modifier = Modifier.testTag("host-unreachable").semantics(mergeDescendants = true) {},
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                UnreachableRing()
                Text(
                    text = "unreachable",
                    color = OrreryTokens.Ink.three,
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                )
            }
        }
    }
}

/** The dashed hollow ring that marks an unreachable host, in the grey ink, on the icon's size. */
@Composable
private fun UnreachableRing() {
    val ring = OrreryTokens.Ink.three
    Canvas(modifier = Modifier.size(OrreryTokens.Size.icon).semantics { set(HostRingColourKey, ring) }) {
        val width = (OrreryTokens.Size.hairline * 2).toPx()
        val dash = (OrreryTokens.Size.hairline * 3).toPx()
        drawCircle(
            color = ring,
            radius = size.minDimension / 2f - width / 2f,
            style = Stroke(width = width, pathEffect = PathEffect.dashPathEffect(floatArrayOf(dash, dash))),
        )
    }
}
