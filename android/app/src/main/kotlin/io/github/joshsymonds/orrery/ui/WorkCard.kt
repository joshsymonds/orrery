package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The tone of a card: attention for what waits on you, live for what is building, none for the rest. */
internal enum class CardTone { NONE, LIVE, ATTENTION }

/** One part of a card's track: done, the part it is in now, or still ahead. */
internal enum class TrackState { DONE, NOW, AHEAD }

/**
 * What a WorkCard says of a [Hung] at a time: its item [key], its [slug], its [gauge] (null for a contract), its [tone],
 * its [state] words, its five-part [track] (contract, planning, building, releasing, report), its [now] line, its [asks]
 * with the [act] that answers them (both empty when nothing asks), and its [count].
 */
internal data class WorkCardWords(
    val key: String,
    val slug: String,
    val gauge: GaugeShare?,
    val tone: CardTone,
    val state: String,
    val track: List<TrackState>,
    val now: String,
    val asks: String,
    val act: String,
    val count: String,
)

private const val CONTRACT_PROPOSED = "proposed"
private const val WAITS_ON_ACCEPT = "waits for you to accept"
private const val READ_AND_DECIDE = "Read and decide"
private const val ANSWER = "Answer"
private const val DECIDE = "Decide"
private const val OPEN = "Open"
private val TRACK_PARTS = listOf("contract", "planning", "building", "releasing", "report")

/**
 * What a card says of [hung] at [now], in unix seconds. A proposed contract, and an epic whose contract is proposed,
 * draw as the contract; every other epic draws as its row says it, with its track from its phase.
 */
internal fun workCardOf(hung: Hung, now: Long): WorkCardWords = when (hung) {
    is ContractNode -> contractCardOf(hung.key, hung.contract)
    is EpicNode -> hung.contract?.takeIf { it.state == CONTRACT_PROPOSED }?.let { contractCardOf(hung.key, it) }
        ?: epicCardOf(hung, now)
}

private fun contractCardOf(key: String, contract: ContractRow): WorkCardWords {
    val waiting = contract.state == CONTRACT_PROPOSED
    return WorkCardWords(
        key = key,
        slug = contract.id.substringAfterLast('/'),
        gauge = null,
        tone = if (waiting) CardTone.ATTENTION else CardTone.NONE,
        state = if (contract.care.isEmpty()) contract.state else "${contract.care} care",
        track = trackAt(0),
        now = contract.title,
        asks = when {
            !waiting -> ""
            contract.openDecisions > 0 -> decisionsWords(contract.openDecisions)
            else -> WAITS_ON_ACCEPT
        },
        act = if (waiting) READ_AND_DECIDE else "",
        count = "v${contract.version}",
    )
}

private fun decisionsWords(open: Long): String =
    if (open == 1L) "1 decision left to answer" else "$open decisions left to answer"

// An epic that is live and unarchived draws its tone from its first need: attention with that need's ask, else live.
// Any other epic has no tone and asks nothing. Its track is the part its phase is in, the contract always done before it.
private fun epicCardOf(node: EpicNode, now: Long): WorkCardWords {
    val epic = node.epic
    val detail = node.detail
    val words = epicRowWords(node, now)
    val active = epic.live && !epic.archived
    val need = detail?.takeIf { active }?.let { epicNeedsOf(listOf(epic), mapOf(epic.id to it)).firstOrNull() }
    val questionId = need?.takeIf { it.kind == NeedKind.QUESTION }?.question
    val questionText = detail?.questions?.firstOrNull { it.id == questionId }?.text.orEmpty()
    val part = detail?.let { trackPartOf(epic.outcome, it) }
    return WorkCardWords(
        key = node.key,
        slug = words.name,
        gauge = words.gauge,
        tone = when {
            !active -> CardTone.NONE
            need != null -> CardTone.ATTENTION
            else -> CardTone.LIVE
        },
        state = words.words,
        track = part?.let { trackAt(TRACK_PARTS.indexOf(it)) } ?: trackAt(1, hasNow = false),
        now = detail?.let { nowSentence(wedgesOf(it)) }.orEmpty(),
        asks = when {
            need == null -> ""
            questionId != null -> questionText
            else -> words.blocker
        },
        act = when {
            need == null -> ""
            questionId != null -> ANSWER
            else -> DECIDE
        },
        count = words.count,
    )
}

// The track with its part at [current] now, the parts before it done and the parts after it ahead. With no part now
// ([hasNow] false) the part at [current] is ahead too: an epic whose phase the track does not name has its contract
// done and nothing now.
private fun trackAt(current: Int, hasNow: Boolean = true): List<TrackState> = TRACK_PARTS.indices.map { index ->
    when {
        index < current -> TrackState.DONE
        index == current && hasNow -> TrackState.NOW
        else -> TrackState.AHEAD
    }
}

/**
 * A WorkCard of the project page: its gauge and slug with its state, its track, its now line, its asks with the act that
 * answers them, and its count with Open. A tap on the card, on its act or on Open calls [onOpen] with the item key.
 */
@Composable
internal fun WorkCard(card: WorkCardWords, onOpen: (String) -> Unit, modifier: Modifier = Modifier) {
    val tone = toneColorOf(card.tone)
    Column(
        modifier = modifier
            .testTag("card:${card.key}")
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.three))
            .background(OrreryTokens.Surface.well)
            .clickable { onOpen(card.key) }
            .padding(OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            card.gauge?.let { GaugeMark(card.key, it) }
            Text(
                card.slug,
                modifier = Modifier.weight(1f),
                style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                color = OrreryTokens.Ink.one,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (card.state.isNotEmpty()) {
                Text(card.state, style = OrreryTokens.Type.small, color = tone, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        TrackBar(card.track, tone)
        if (card.now.isNotEmpty()) {
            Text(card.now, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        }
        if (card.asks.isNotEmpty()) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    card.asks,
                    modifier = Modifier.weight(1f),
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Role.attention,
                )
                Button(onClick = { onOpen(card.key) }, modifier = Modifier.testTag("card-act:${card.key}")) {
                    Text(card.act)
                }
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(card.count, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            TextButton(onClick = { onOpen(card.key) }, modifier = Modifier.testTag("card-open:${card.key}")) {
                Text(OPEN)
            }
        }
    }
}

@Composable
private fun TrackBar(track: List<TrackState>, tone: Color) {
    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two)) {
        track.forEach { state ->
            Box(
                modifier = Modifier
                    .weight(1f)
                    .height(OrreryTokens.Size.meterBarHeight)
                    .background(trackColorOf(state, tone), RoundedCornerShape(OrreryTokens.Radius.one)),
            )
        }
    }
}

private fun toneColorOf(tone: CardTone): Color = when (tone) {
    CardTone.ATTENTION -> OrreryTokens.Role.attention
    CardTone.LIVE -> OrreryTokens.Role.live
    CardTone.NONE -> OrreryTokens.Ink.three
}

private fun trackColorOf(state: TrackState, tone: Color): Color = when (state) {
    TrackState.DONE -> OrreryTokens.Ink.three
    TrackState.NOW -> tone
    TrackState.AHEAD -> OrreryTokens.Line.soft
}
