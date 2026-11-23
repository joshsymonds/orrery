package io.github.joshsymonds.orrery.ui

/** The two chips: what waits on you, one per thing, and the unseen rows. */
internal data class Counts(val waiting: Int, val fresh: Int)

/** The narrowing a chip picks: [NONE] for the whole tree, [WAITING] for what waits on you, [NEW] for unseen rows. */
internal enum class Narrow { NONE, WAITING, NEW }

private val WHITESPACE = Regex("\\s+")

/**
 * The chips' counts over the projects in the tree: each waiting conversation, each epic with a need and each proposed
 * contract once, a contract not again when its conversation waits itself, and each unseen row once.
 */
internal fun countsOf(projects: List<Project>): Counts = Counts(
    waiting = projects.sumOf { project -> project.convos.sumOf { waitingThings(it) } },
    fresh = projects.sumOf { unseenRows(it).size },
)

/** A folded project's waiting number: the chip's count over that project alone. */
internal fun projectWaiting(project: Project): Int = countsOf(listOf(project)).waiting

/**
 * The projects narrowed to what waits on you, keeping each conversation that waits as the parent of what waits under
 * it, or to unseen rows with nothing hung under them. Either narrowing drops the ended line, and the ended epics folded
 * under each row.
 */
internal fun narrow(projects: List<Project>, by: Narrow): List<Project> = when (by) {
    Narrow.NONE -> projects
    Narrow.WAITING -> projects.mapNotNull { project ->
        val convos = project.convos.filter { convoWaits(it) }.map { convo ->
            convo.copy(hung = convo.hung.filter { waits(it) }, folded = emptyList())
        }
        if (convos.isEmpty()) null else project.copy(convos = convos, ended = emptyList())
    }
    Narrow.NEW -> projects.mapNotNull { project ->
        val rows = unseenRows(project).map { it.copy(hung = emptyList(), folded = emptyList()) }
        if (rows.isEmpty()) null else project.copy(convos = rows, ended = emptyList())
    }
}

/**
 * The projects with the conversations and hung things that match every word of [query], ignoring case. A
 * conversation whose own text matches keeps everything hung under it; otherwise only the matching things stay.
 */
internal fun search(projects: List<Project>, query: String): List<Project> {
    val words = query.lowercase().split(WHITESPACE).filter { it.isNotEmpty() }
    return projects.mapNotNull { project ->
        val convos = matchingConvos(project, project.convos, words)
        val ended = matchingConvos(project, project.ended, words)
        if (convos.isEmpty() && ended.isEmpty()) null else project.copy(convos = convos, ended = ended)
    }
}

/**
 * What waits on you in one conversation: its own row when the row is waiting, and each hung thing that counts. An
 * archived conversation counts nothing.
 */
private fun waitingThings(convo: Convo): Int {
    if (convo.pane?.archived == true) return 0
    val ownWaiting = convo.pane?.let { waitsOnYou(it) } == true
    return (if (ownWaiting) 1 else 0) + convo.hung.count { countsHung(it, ownWaiting) }
}

/**
 * Whether a thing hung under a conversation counts. An epic counts once, for its needs or for its proposed contract. A
 * proposed contract does not count when its conversation's own row is waiting, since that conversation covers it.
 */
private fun countsHung(hung: Hung, ownWaiting: Boolean): Boolean = when (hung) {
    is EpicNode -> waits(hung) && (!ownWaiting || waits(hung.copy(contract = null)))
    is ContractNode -> waits(hung) && !ownWaiting
}

/** A project's unseen rows, archived ones never. Each conversation draws in one place, so each is listed once. */
private fun unseenRows(project: Project): List<Convo> =
    (project.convos + project.ended).filter { it.pane?.unread() == true && !it.pane.archived }

private fun matchingConvos(project: Project, convos: List<Convo>, words: List<String>): List<Convo> =
    convos.mapNotNull { convo ->
        val own = matches(words, rowFields(project, convo))
        val hung = if (own) convo.hung else convo.hung.filter { matches(words, hungFields(project, it)) }
        val folded = if (own) convo.folded else convo.folded.filter { matches(words, hungFields(project, it)) }
        if (own || hung.isNotEmpty() || folded.isNotEmpty()) convo.copy(hung = hung, folded = folded) else null
    }

/** Whether every word appears in the fields' text, ignoring case. */
private fun matches(words: List<String>, fields: List<String>): Boolean {
    val text = fields.joinToString(" ").lowercase()
    return words.all { text.contains(it) }
}

private fun rowFields(project: Project, convo: Convo): List<String> {
    val pane = convo.pane ?: return projectFields(project)
    return listOf(pane.label.ifEmpty { pane.summary }, pane.state, pane.host) + projectFields(project)
}

private fun hungFields(project: Project, hung: Hung): List<String> = when (hung) {
    is EpicNode -> listOf(hung.epic.slug, hung.epic.outcome) +
        hung.contract?.let { listOf(it.id, it.title) }.orEmpty() +
        projectFields(project)
    is ContractNode -> listOf(hung.contract.id, hung.contract.title) + projectFields(project)
}

private fun projectFields(project: Project): List<String> = listOf(project.name, project.path)
