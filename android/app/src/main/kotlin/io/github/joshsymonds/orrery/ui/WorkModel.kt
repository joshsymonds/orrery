package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.BackgroundTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow

/** The groups of a conversation's right rail: the epics it steers, its running sub-agents, its running shells. */
enum class GroupKind { EPIC, AGENTS, RUNNING }

/** How a thing reads: ATTENTION when it asks the user, LIVE while it runs, QUIET otherwise. */
enum class ThingTone { ATTENTION, LIVE, QUIET }

/** One tile of a group: an epic the conversation steers, or the running tasks of one list. */
sealed interface RailThing {
    val key: String
    val tone: ThingTone
}

/**
 * An epic the conversation steers. [short] is its name in the strip, [state] is its first need's text, else its
 * state, and [detail] is its detail when it is held.
 */
data class EpicThing(
    val id: String,
    val slug: String,
    val short: String,
    val asks: Boolean,
    override val tone: ThingTone,
    val state: String,
    val detail: EpicDetail?,
) : RailThing {
    override val key: String get() = id
}

/** The running tasks of one list, [kind] being AGENTS or RUNNING. Its [count] is its number of tasks. */
data class ListThing(
    val kind: GroupKind,
    val tasks: List<BackgroundTaskRow>,
    override val tone: ThingTone,
) : RailThing {
    override val key: String get() = kind.name.lowercase()
    val count: Int get() = tasks.size
}

/** A group of the rail with its [label]. [many] is whether it holds more than three things. */
data class RailGroup(
    val kind: GroupKind,
    val label: String,
    val things: List<RailThing>,
    val many: Boolean,
)

// A group with more than this many things is marked many.
private const val MANY_THINGS = 3

// The conversation's right-rail groups, in order: the epics it steers, its running sub-agents and its running shells.
// An epic is steered when its record names the session, and an empty session steers none. A pane has no lists. A group
// with no thing is left out.
fun workGroupsOf(
    session: String,
    pane: Boolean,
    epics: List<EpicRow>,
    details: Map<String, EpicDetail>,
    background: List<BackgroundTaskRow>,
): List<RailGroup> {
    val groups = mutableListOf<RailGroup>()
    val epicThings = epicThingsOf(session, epics, details)
    if (epicThings.isNotEmpty()) {
        groups += groupOf(GroupKind.EPIC, if (epicThings.size == 1) "epic" else "epics", epicThings)
    }
    if (!pane) {
        val agents = runningOf(background, "agent")
        if (agents.isNotEmpty()) {
            groups += groupOf(GroupKind.AGENTS, "agents", listOf(ListThing(GroupKind.AGENTS, agents, ThingTone.LIVE)))
        }
        val shells = runningOf(background, "shell")
        if (shells.isNotEmpty()) {
            groups += groupOf(GroupKind.RUNNING, "running", listOf(ListThing(GroupKind.RUNNING, shells, ThingTone.QUIET)))
        }
    }
    return groups
}

// The epics the session steers: the live ones in list order, then the ended unseen ones, newest ending first. An
// archived epic is left out, and so is an ended one the owner has seen.
private fun epicThingsOf(session: String, epics: List<EpicRow>, details: Map<String, EpicDetail>): List<EpicThing> {
    if (session.isEmpty()) return emptyList()
    val steered = epics.filter { it.steering == session && !it.archived && (it.live || it.endedUnseen()) }
    val live = steered.filter { it.live }
    val ended = steered.filter { !it.live }.sortedByDescending { it.phaseAt }
    return (live + ended).map { epicThingOf(it, details) }
}

// An orreryd epic that is not live has ended, and it is unseen while the owner has not seen it since it ended.
private fun EpicRow.endedUnseen(): Boolean = source == "orreryd" && !live && seen < phaseAt

// An epic asks when a need is open, and is live while it has a running step.
private fun epicThingOf(epic: EpicRow, details: Map<String, EpicDetail>): EpicThing {
    val detail = details[epic.id]
    val needs = epicNeedsOf(listOf(epic), details)
    val asks = needs.isNotEmpty()
    val tone = when {
        asks -> ThingTone.ATTENTION
        epic.runningSteps.isNotEmpty() -> ThingTone.LIVE
        else -> ThingTone.QUIET
    }
    return EpicThing(
        id = epic.id,
        slug = epic.slug,
        short = shortNameOf(epic),
        asks = asks,
        tone = tone,
        state = stateOf(epic, detail, needs),
        detail = detail,
    )
}

// The slug past its repository's name and the dash after it, up to the first dash after that.
private fun shortNameOf(epic: EpicRow): String {
    val prefix = "${repositoryNameOf(epic.repository)}-"
    val rest = if (epic.slug.startsWith(prefix)) epic.slug.removePrefix(prefix) else epic.slug
    return rest.substringBefore("-")
}

// A repository is named by its record id, which ends in a dash and seven hex digits. Its name is the id without them.
private val HEX_SUFFIX = Regex("-[0-9a-f]{7}$")

private fun repositoryNameOf(repository: String): String = repository.replace(HEX_SUFFIX, "")

// An epic asking reads its first need. Any other reads its detail's state, or its outcome when it has none.
private fun stateOf(epic: EpicRow, detail: EpicDetail?, needs: List<Need>): String {
    needs.firstOrNull()?.let { return it.text }
    val state = if (detail == null) "" else epicStateOf(detail)
    return state.ifEmpty { epic.outcome }
}

// The tasks of a kind that are running.
private fun runningOf(background: List<BackgroundTaskRow>, type: String): List<BackgroundTaskRow> =
    background.filter { it.type == type && it.status == "running" }

private fun groupOf(kind: GroupKind, label: String, things: List<RailThing>): RailGroup =
    RailGroup(kind, label, things, many = things.size > MANY_THINGS)
