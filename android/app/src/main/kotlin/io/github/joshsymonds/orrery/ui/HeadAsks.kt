package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane

// The status of a background task still going.
private const val RUNNING = "running"

/** The live epics that the conversation [conversationId] steers, in listing order. An empty id steers nothing. */
fun steeredEpics(conversationId: String, epics: List<EpicRow>): List<EpicRow> {
    if (conversationId.isEmpty()) return emptyList()
    return epics.filter { it.live && it.steering == conversationId }
}

/**
 * One ask line per epic with needs, read over that epic's needs alone, in list order. The line's text is the need's
 * text less its slug, and its way is the one [askOf] gives.
 */
fun askLines(epics: List<EpicRow>, details: Map<String, EpicDetail>): List<AskLine> =
    epics.mapNotNull { epic ->
        val (need, way) = askOf(epicNeedsOf(listOf(epic), details)) ?: return@mapNotNull null
        AskLine(epic.id, epic.slug, withoutSlug(need.text, epic.slug), way)
    }

/** The need an epic's line shows and its way: a park decides, else an open question answers, else the first need is open. */
private fun askOf(needs: List<Need>): Pair<Need, AskWay>? {
    val park = needs.firstOrNull { it.kind == NeedKind.PARK }
    if (park != null) return park to AskWay.DECIDE
    val question = needs.firstOrNull { it.kind == NeedKind.QUESTION }
    if (question != null) return question to AskWay.ANSWER
    return needs.firstOrNull()?.let { it to AskWay.OPEN }
}

private fun withoutSlug(text: String, slug: String): String = when {
    text.startsWith("$slug: ") -> text.removePrefix("$slug: ")
    else -> text.removePrefix("$slug ")
}

/** True when the pane asks its owner: it waits on you, per [waitsOnYou]. */
fun ownAsks(pane: Pane?): Boolean = pane != null && waitsOnYou(pane)

/**
 * The words of the folded line: nothing for no ask, the name for one (the conversation's own name, else the epic's
 * slug), and the count for more. [ownName] is null when the conversation's own question does not ask.
 */
fun askingWords(ownName: String?, lines: List<AskLine>): String {
    val count = lines.size + if (ownName != null) 1 else 0
    return when {
        count == 0 -> ""
        count > 1 -> "$count ask you"
        ownName != null -> "$ownName asks"
        else -> "${lines[0].slug} asks"
    }
}

/**
 * The badges of the folded line, in order: one per steered epic labelled with its slug's first part and marked when it
 * has an ask line, then the running agents and the running shells when there are any.
 */
fun headBadges(epics: List<EpicRow>, lines: List<AskLine>, background: List<BackgroundTask>): List<HeadBadge> {
    val asking = lines.map { it.epicId }.toSet()
    val epicBadges = epics.map { HeadBadge(it.id, it.slug.substringBefore('-'), 0, it.id in asking) }
    val agents = background.count { it.type == "agent" && it.status == RUNNING }
    val shells = background.count { it.type == "shell" && it.status == RUNNING }
    return epicBadges + listOfNotNull(
        agents.takeIf { it > 0 }?.let { HeadBadge("agents", "agents", it, asks = false) },
        shells.takeIf { it > 0 }?.let { HeadBadge("running", "running", it, asks = false) },
    )
}
