package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The kind of thing a work link names: a design, an epic, or a contract. */
internal enum class WorkKind { DESIGN, EPIC, CONTRACT }

/**
 * One thing a conversation works on, as its title row names it: its [key] as the tree keys it, its [kind], its [id] and
 * the [name] the link shows.
 */
internal data class WorkLink(val key: String, val kind: WorkKind, val id: String, val name: String)

/** The [WorkLinks] lit value that names the "+N" link: the list of all of them is open. */
private const val ALL_LIT = "all"

/**
 * The work links of a conversation [conversationId], in order: each design the conversation works that is not a design
 * of one, then the contract of each design of one (as an epic link when that contract has an epic, else a contract
 * link), then each steered epic that no design of the conversation carries. An empty id names no conversation, so it
 * gives none.
 */
internal fun workLinksOf(conversationId: String, designs: List<DesignRow>, steered: List<EpicRow>): List<WorkLink> {
    if (conversationId.isEmpty()) return emptyList()
    val own = designs.filter { it.conversation == conversationId }
    val designLinks = own.filterNot { it.ofOne }.map { design ->
        WorkLink("design:${design.id}", WorkKind.DESIGN, design.id, designNameOf(design))
    }
    val ofOneLinks = own.filter { it.ofOne }.flatMap { design -> design.contracts.map(::contractLinkOf) }
    val carried = own.flatMap { design -> design.contracts.map { it.epic } }.toSet()
    val epicLinks = steered.filter { it.id !in carried }.map { epic ->
        WorkLink("epic:${epic.id}", WorkKind.EPIC, epic.id, epic.slug)
    }
    return designLinks + ofOneLinks + epicLinks
}

// A design of one's contract as a link: the epic it made when it has one, else the contract. Named as the inbox names a
// contract, by its id past the slash.
private fun contractLinkOf(contract: DesignContractRow): WorkLink {
    val name = contract.id.substringAfter('/')
    return if (contract.epic.isEmpty()) {
        WorkLink("contract:${contract.id}", WorkKind.CONTRACT, contract.id, name)
    } else {
        WorkLink("epic:${contract.epic}", WorkKind.EPIC, contract.epic, name)
    }
}

/**
 * A conversation's work links on its title row's project line, as round 8 draws them on touch: the first link's face and
 * its name, a plain link that calls [onOpen] with it, then "+N" for the rest, which calls [onAll]. [lit] is the key of
 * the link whose thing is open, or "all" while the list of all of them is open; that one sits on the raised ground and
 * is selected. It draws nothing for no links.
 */
@Composable
internal fun WorkLinks(links: List<WorkLink>, lit: String?, onOpen: (WorkLink) -> Unit, onAll: () -> Unit) {
    if (links.isNotEmpty()) {
        val first = links.first()
        Row(
            modifier = Modifier.testTag("work-links"),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            WorkLinkItem(first, isLit = lit == first.key, onClick = { onOpen(first) })
            if (links.size > 1) {
                WorkMoreItem(count = links.size - 1, isLit = lit == ALL_LIT, onClick = onAll)
            }
        }
    }
}

// One link: its face and its name in one row, tagged by its key, on the raised ground while it is lit.
@Composable
private fun WorkLinkItem(link: WorkLink, isLit: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier.chip("work-link:${link.key}", isLit, onClick),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        when (link.kind) {
            WorkKind.DESIGN -> GlyphIcon(Glyph.DESIGN, OrreryTokens.Ink.one)
            WorkKind.EPIC, WorkKind.CONTRACT -> ContractMark(link.key)
        }
        Text(
            link.name,
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.small,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

// The "+N" link for the links past the first, on the raised ground while the list of all of them is open.
@Composable
private fun WorkMoreItem(count: Int, isLit: Boolean, onClick: () -> Unit) {
    Text(
        "+$count",
        modifier = Modifier.chip("work-link-all", isLit, onClick),
        color = OrreryTokens.Ink.two,
        style = OrreryTokens.Type.small,
        maxLines = 1,
    )
}

// A link's tag, its selection, its tap and its padding, with the raised ground behind it while it is lit.
private fun Modifier.chip(tag: String, isLit: Boolean, onClick: () -> Unit): Modifier {
    val ground = Modifier.background(OrreryTokens.Surface.raised, RoundedCornerShape(OrreryTokens.Radius.one))
    return this
        .testTag(tag)
        .semantics { selected = isLit }
        .clickable(onClick = onClick)
        .padding(horizontal = OrreryTokens.Space.four, vertical = OrreryTokens.Space.two)
        .then(if (isLit) ground else Modifier)
}

/**
 * The sheet of a conversation's work when its links are tapped: each design as its block, then the loose epics, those
 * with no design, under a label. A tap on a design's kid calls [onOpen] with the kid's key. [onDismiss] closes it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun WorkSheet(
    designs: List<DesignBlock>,
    loose: List<Hung>,
    now: Long,
    onOpen: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            for (block in designs) {
                DesignBlockItem(block, now, onOpen, onLongPress = {})
            }
            if (loose.isNotEmpty()) {
                val label = if (loose.size == 1) "an epic with no design" else "epics with no design"
                Text(
                    label,
                    modifier = Modifier.testTag("work-sheet-loose"),
                    color = OrreryTokens.Ink.three,
                    style = OrreryTokens.Type.small,
                )
                for (hung in loose) {
                    HungRow(hung, now, onOpen, onLongPress = {})
                }
            }
        }
    }
}
