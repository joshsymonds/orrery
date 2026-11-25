package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The tab's share of the row at most: its name is cut with an ellipsis past it, so the count beside it has room. */
private const val TAB_SHARE = 0.6f

/** Where a child's mark begins: under the first letter of the design's name, past the tab's start and the mark's cell. */
private val NAME_AT: Dp = OrreryTokens.Space.four + OrreryTokens.Size.gauge + OrreryTokens.Space.eight

/**
 * A design in the inbox's tree, as round 8 draws it (D13, D17, D18): a block a step in from its conversation's, on the
 * well's ground. Its tab is the design's mark and its name in bold on the side's ground, with the count of its contracts
 * beside it; its page is the same ground under the tab, its state first, then its [block] kids: each a row under the
 * name, in regular weight. A tap or a long press on a kid behaves as it does in the conversation. The tab is not a
 * control.
 */
@Composable
internal fun DesignBlockItem(
    block: DesignBlock,
    now: Long,
    onOpen: (String) -> Unit,
    onLongPress: (SheetRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    val design = block.design
    Column(
        modifier = modifier
            .testTag("design:${design.id}")
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.twelve, top = OrreryTokens.Space.two, bottom = OrreryTokens.Space.two)
            .background(OrreryTokens.Surface.well, RoundedCornerShape(OrreryTokens.Radius.two))
            .padding(OrreryTokens.Space.four),
    ) {
        DesignTab(design)
        Column(
            modifier = Modifier
                .testTag("design-well:${design.id}")
                .fillMaxWidth()
                .background(
                    OrreryTokens.Surface.side,
                    RoundedCornerShape(bottomStart = OrreryTokens.Radius.two, bottomEnd = OrreryTokens.Radius.two),
                )
                .padding(bottom = OrreryTokens.Space.four),
        ) {
            Text(
                designStateLineOf(design),
                modifier = Modifier.padding(
                    start = NAME_AT,
                    top = OrreryTokens.Space.two,
                    bottom = OrreryTokens.Space.four,
                ),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
            for (kid in block.kids) {
                HungRow(kid, now, onOpen, onLongPress, modifier = Modifier.padding(start = NAME_AT), inDesign = true)
            }
        }
    }
}

// The tab and the count beside it, on the block's ground. The tab is as wide as its name, up to TAB_SHARE of the row.
@Composable
private fun DesignTab(design: DesignRow) {
    BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
        val tabMost = maxWidth * TAB_SHARE
        Row(verticalAlignment = Alignment.CenterVertically) {
            Row(
                modifier = Modifier
                    .testTag("design-tab:${design.id}")
                    .widthIn(max = tabMost)
                    .background(
                        OrreryTokens.Surface.side,
                        RoundedCornerShape(topStart = OrreryTokens.Radius.two, topEnd = OrreryTokens.Radius.two),
                    )
                    .padding(
                        start = OrreryTokens.Space.four,
                        top = OrreryTokens.Space.four,
                        end = OrreryTokens.Space.twelve,
                        bottom = OrreryTokens.Space.four,
                    ),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Box(modifier = Modifier.size(OrreryTokens.Size.gauge), contentAlignment = Alignment.Center) {
                    GlyphIcon(Glyph.DESIGN, OrreryTokens.Ink.one)
                }
                Text(
                    designNameOf(design),
                    style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    color = OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                designCountOf(design),
                modifier = Modifier.padding(start = OrreryTokens.Space.twelve, end = OrreryTokens.Space.eight),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                maxLines = 1,
            )
        }
    }
}
