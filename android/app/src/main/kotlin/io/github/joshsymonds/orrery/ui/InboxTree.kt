package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane

/** A thing drawn under a conversation's row or on its own row: a listed epic, or a contract that no drawn epic carries. */
internal sealed interface Hung {
    val key: String
}

/**
 * A listed epic hung under its conversation. [detail] is its loaded detail, null until it loads; [contract] is the
 * contract of its id when Contracts.List holds one, drawn with the epic.
 */
internal data class EpicNode(val epic: EpicRow, val detail: EpicDetail?, val contract: ContractRow?) : Hung {
    override val key: String = "epic:${epic.id}"
}

/** A contract hung under the conversation that wrote it, or on its own row. */
internal data class ContractNode(val contract: ContractRow) : Hung {
    override val key: String = "contract:${contract.id}"
}

/**
 * One row of a project. [pane] is the conversation's pane or headless session, null for the own row of a thing with no
 * drawn conversation, which holds just that thing. [hung] is what hangs under the row. [folded] is the ended epics of a
 * conversation in the tree, newest first, which fold under the row; a conversation behind the ended line keeps its
 * ended epics in [hung] and has none folded.
 */
internal data class Convo(
    val key: String,
    val pane: Pane?,
    val hung: List<Hung>,
    val folded: List<EpicNode>,
)

/**
 * A card of the tree: [convos] in the tree, ranked, and [ended] behind its ended line, newest first. [dir] is the
 * absolute directory, empty for a repository card and the No directory card; [path] is the directory with home as ~.
 */
internal data class Project(
    val key: String,
    val dir: String,
    val name: String,
    val path: String,
    val host: String,
    val convos: List<Convo>,
    val ended: List<Convo>,
)

/** The inbox lists what is not archived; the archived view lists what is. */
internal enum class TreeView { INBOX, ARCHIVED }

/** A row is unseen while its newest reply is after the owner last saw it. */
internal fun Pane.unread(): Boolean = newestReply > seen

private const val SHELL_SESSION_PREFIX = "_orrery-shell-"
private const val STATE_WAITING = "waiting"
private const val STATE_WORKING = "working"
private const val CONTRACT_PROPOSED = "proposed"
private const val CONTRACT_ACCEPTED = "accepted"
private const val NO_DIRECTORY_KEY = "nodir"
private const val NO_DIRECTORY_NAME = "No directory"
private const val RANK_WAITING = 0
private const val RANK_UNSEEN = 1
private const val RANK_WORKING = 2
private const val RANK_REST = 3
private val HOME_PARENTS = setOf("home", "Users")
private val WORKTREE_SUFFIXES = listOf(".worktrees", "-worktrees")
private val HEX_SUFFIX = Regex("-[0-9a-f]{7}$")

/** The key a conversation is drawn and named by: its headless session's id, or its tmux pane's host and id. */
internal fun convoKeyOf(pane: Pane): String =
    if (pane.headless) "session:${pane.id}" else "pane:${pane.host}:${pane.id}"

/** A conversation: a tmux pane or a headless session, with the id that epics and contracts name it by. */
private class Conversation(val pane: Pane, val archived: Boolean, val place: CardPlace) {
    val id: String = if (pane.headless) pane.id else pane.orreryId
    val key: String = convoKeyOf(pane)
}

/** Where a card sits: its key, absolute directory, name, shown path, and the host when it is not the serving one. */
private data class CardPlace(val key: String, val dir: String, val name: String, val path: String, val host: String)

/** One row being built: its place, its key within the place, and what hangs under it. */
private class Slot(
    val place: CardPlace,
    val key: String,
    val pane: Pane?,
    val ended: Boolean,
) {
    val hung = mutableListOf<Hung>()
    val folded = mutableListOf<EpicNode>()

    fun toConvo(): Convo = Convo(key, pane, hung.toList(), folded.sortedByDescending { it.epic.changed })
}

private class Ranked(val project: Project, val best: Int, val newest: Long)

/** The rows of a tree, keyed by the card they sit in, whether they are ended, and their key. */
private class Slots {
    private val rows = LinkedHashMap<Triple<Boolean, String, String>, Slot>()

    fun conversation(conversation: Conversation, ended: Boolean): Slot =
        rows.getOrPut(Triple(ended, conversation.place.key, conversation.key)) {
            Slot(conversation.place, conversation.key, conversation.pane, ended)
        }

    /** The own row of a thing with no drawn conversation: keyed by the thing's key, it holds just that thing. */
    fun own(place: CardPlace, hung: Hung, ended: Boolean) {
        rows.getOrPut(Triple(ended, place.key, hung.key)) { Slot(place, hung.key, null, ended) }.hung.add(hung)
    }

    fun projects(): List<Project> {
        val cards = rows.values.groupBy { it.place.key }.values.map { slots ->
            val place = slots.first().place
            val convos = slots.filterNot { it.ended }.map { it.toConvo() }
                .sortedWith(compareBy<Convo> { rankOf(it) }.thenByDescending { changedOf(it) })
            val ended = slots.filter { it.ended }.map { it.toConvo() }.sortedByDescending { changedOf(it) }
            Ranked(
                Project(place.key, place.dir, place.name, place.path, place.host, convos, ended),
                convos.minOfOrNull { rankOf(it) } ?: (RANK_REST + 1),
                (convos + ended).maxOfOrNull { changedOf(it) } ?: 0L,
            )
        }
        return cards
            .sortedWith(
                compareBy<Ranked> { it.project.key == NO_DIRECTORY_KEY }
                    .thenBy { it.best }
                    .thenByDescending { it.newest },
            )
            .map { it.project }
    }
}

/** The lookups every placement reads: the conversations by id, the listed epics and contracts, and the serving host. */
private class Index(
    val host: String,
    val details: Map<String, EpicDetail>,
    val epics: List<EpicRow>,
    val contracts: List<ContractRow>,
    val conversations: List<Conversation>,
) {
    private val byId: Map<String, Conversation> = conversations.filter { it.id.isNotEmpty() }.associateBy { it.id }
    private val contractsById: Map<String, ContractRow> = contracts.associateBy { it.id }
    private val listedIds: Set<String> = epics.map { it.id }.toSet()

    /** The contracts shown: proposed, or accepted while no listed epic has the id. Set-aside ones never show. */
    val shownContracts: List<ContractRow> = contracts.filter {
        it.state == CONTRACT_PROPOSED || (it.state == CONTRACT_ACCEPTED && it.id !in listedIds)
    }

    /** The conversation an id names, or null when none does; an empty id names none. */
    fun target(id: String): Conversation? = byId[id]

    fun node(epic: EpicRow): EpicNode =
        EpicNode(epic, details[epic.id], contractsById[epic.id]?.takeIf { it.state == CONTRACT_PROPOSED })

    fun placeOfEpic(epic: EpicRow): CardPlace =
        if (epic.path.startsWith("/")) placeOfDirectory(epic.path, host, host) else placeOfRepository(epic.repository)

    fun placeOfContract(contract: ContractRow): CardPlace =
        if (contract.repository.startsWith("/")) {
            placeOfDirectory(contract.repository, host, host)
        } else {
            placeOfRepository(contract.repository)
        }
}

/**
 * Places each conversation and what hangs under it in the inbox: live work and the ended line. An epic or contract whose
 * conversation is not listed, or is archived, draws on its own row in its project.
 */
private fun placeInbox(index: Index, slots: Slots) {
    // An ended epic carrying a proposed contract stays in the tree: the contract's ask is live work.
    val proposedIds = index.shownContracts.filter { it.state == CONTRACT_PROPOSED }.map { it.id }.toSet()
    val liveShown = index.epics.filter { !it.archived && (it.live || it.id in proposedIds) }
    val endedShown = index.epics.filter { !it.archived && !it.live && it.id !in proposedIds }
    val carried = (liveShown + endedShown).map { it.id }.toSet()
    val contractItems = index.shownContracts.filterNot { it.id in carried }

    // Serve decides the ended line: an archived conversation draws only in the archived view, and one serve marks ended
    // draws behind its project's ended line.
    val behind = index.conversations.filter { !it.archived && it.pane.ended }.toSet()
    val inTree = index.conversations.filter { !it.archived && it !in behind }
    for (conversation in inTree) slots.conversation(conversation, ended = false)
    for (conversation in behind) slots.conversation(conversation, ended = true)

    for (epic in liveShown) {
        val target = index.target(epic.steering)
        if (target != null && !target.archived) {
            slots.conversation(target, ended = false).hung.add(index.node(epic))
        } else {
            slots.own(index.placeOfEpic(epic), index.node(epic), ended = false)
        }
    }

    // An ended epic of a conversation in the tree folds under that row; one behind the ended line stays in its hung. An
    // ended epic with no drawn conversation sits behind the ended line on its own row.
    for (epic in endedShown) {
        val target = index.target(epic.steering)
        val node = index.node(epic)
        when {
            target == null || target.archived -> slots.own(index.placeOfEpic(epic), node, ended = true)
            target in behind -> slots.conversation(target, ended = true).hung.add(node)
            else -> slots.conversation(target, ended = false).folded.add(node)
        }
    }

    for (contract in contractItems) {
        val target = index.target(contract.conversation)
        val node = ContractNode(contract)
        if (target != null && !target.archived) {
            slots.conversation(target, ended = false).hung.add(node)
        } else {
            slots.own(index.placeOfContract(contract), node, ended = false)
        }
    }
}

/**
 * Places the archived view: each archived conversation with every epic and contract it steered, and each archived epic
 * whose conversation is not archived on its own row. A contract shows here only under an archived conversation.
 */
private fun placeArchived(index: Index, slots: Slots) {
    val archivedConversations = index.conversations.filter { it.archived }
    for (conversation in archivedConversations) slots.conversation(conversation, ended = false)

    val visible = index.epics.filter { epic -> index.target(epic.steering)?.archived == true || epic.archived }
    for (epic in visible) {
        val target = index.target(epic.steering)
        if (target != null && target.archived) {
            slots.conversation(target, ended = false).hung.add(index.node(epic))
        } else {
            slots.own(index.placeOfEpic(epic), index.node(epic), ended = false)
        }
    }

    val carried = visible.map { it.id }.toSet()
    for (contract in index.shownContracts) {
        if (contract.id in carried) continue
        val target = index.target(contract.conversation) ?: continue
        if (!target.archived) continue
        slots.conversation(target, ended = false).hung.add(ContractNode(contract))
    }
}

/**
 * The tree the inbox or the archived view draws from the rows and epics: conversations grouped by project, with what
 * steers each hung under it, the ended line behind each project, and both ranked.
 */
internal fun inboxTree(
    panes: List<Pane>,
    archivedIds: Set<String>,
    epics: List<EpicRow>,
    details: Map<String, EpicDetail>,
    contracts: List<ContractRow>,
    host: String,
    view: TreeView,
): List<Project> {
    val drawn = panes.filterNot { it.session.startsWith(SHELL_SESSION_PREFIX) }.filter { drawsAsConversation(it) }
    val conversations = drawn.map { pane ->
        val archived = archivedOf(pane, archivedIds)
        // The convo's pane carries the archived flag a tmux pane takes from Sessions.List, so readers of the tree need no
        // archivedIds of their own.
        Conversation(pane.copy(archived = archived), archived, placeOfConversation(pane, host))
    }
    val index = Index(host, details, epics, contracts, conversations)
    val slots = Slots()
    when (view) {
        TreeView.INBOX -> placeInbox(index, slots)
        TreeView.ARCHIVED -> placeArchived(index, slots)
    }
    return slots.projects()
}

/** Whether a row or epic waits on you: an epic with a need while live and not archived, or a proposed contract. */
internal fun waits(hung: Hung): Boolean = when (hung) {
    is EpicNode -> {
        val needs = hung.detail?.let { epicNeedsOf(listOf(hung.epic), mapOf(hung.epic.id to it)) }.orEmpty()
        (hung.epic.live && !hung.epic.archived && needs.isNotEmpty()) || hung.contract?.state == CONTRACT_PROPOSED
    }
    is ContractNode -> hung.contract.state == CONTRACT_PROPOSED
}

/**
 * Whether a pane draws as a conversation: a headless session always does, and a tmux pane only while a Claude session is
 * bound to it or claude runs in it unbound. A plain terminal draws no row.
 */
internal fun drawsAsConversation(pane: Pane): Boolean = pane.headless || pane.claudeSession.isNotEmpty() || pane.unbound

/** Whether a pane is archived: a headless session by its own flag, a tmux pane by its orrery_id's place in [archivedIds]. */
internal fun archivedOf(pane: Pane, archivedIds: Set<String>): Boolean =
    if (pane.headless) pane.archived else pane.orreryId.isNotEmpty() && pane.orreryId in archivedIds

/** Whether a pane waits on you: it is waiting, not archived, and its owner has not put its question off. */
internal fun waitsOnYou(pane: Pane): Boolean = pane.state == STATE_WAITING && !pane.archived && !pane.putOff

/** Whether a conversation waits on you: its own row waits, or something hung under it does. */
internal fun convoWaits(convo: Convo): Boolean =
    convo.pane?.let { waitsOnYou(it) } == true || convo.hung.any { waits(it) }

private fun rankOf(convo: Convo): Int = when {
    convoWaits(convo) -> RANK_WAITING
    convo.pane?.unread() == true -> RANK_UNSEEN
    convo.pane?.state == STATE_WORKING -> RANK_WORKING
    else -> RANK_REST
}

/** A row's change: its pane's, or for a row with no pane its newest hung thing's. */
private fun changedOf(convo: Convo): Long =
    convo.pane?.changed ?: convo.hung.maxOfOrNull { changedOfHung(it) } ?: 0L

private fun changedOfHung(hung: Hung): Long = when (hung) {
    is EpicNode -> hung.epic.changed
    is ContractNode -> hung.contract.changed
}

/** The card a conversation sits in: its directory, or the No directory card when it has none. */
private fun placeOfConversation(pane: Pane, serving: String): CardPlace =
    if (pane.cwd.isEmpty()) {
        CardPlace(NO_DIRECTORY_KEY, "", NO_DIRECTORY_NAME, "", "")
    } else {
        placeOfDirectory(pane.cwd, pane.host, serving)
    }

/**
 * The card of a directory: a `<repo>.worktrees/<x>` or `<repo>-worktrees/<x>` pair folds to `<repo>`, home is shown
 * as ~ under /home/<user> or /Users/<user>, and the host is named only when it is not the serving one.
 */
private fun placeOfDirectory(dir: String, host: String, serving: String): CardPlace {
    val parts = dir.split('/').filter { it.isNotEmpty() }
    val homed = parts.size >= 2 && parts[0] in HOME_PARENTS
    val head = if (homed) parts.take(2) else emptyList()
    val folders = foldWorktrees(if (homed) parts.drop(2) else parts)
    val absolute = "/" + (head + folders).joinToString("/")
    val shownHost = if (host == serving) "" else host
    val path = when {
        !homed -> absolute
        folders.isEmpty() -> "~"
        else -> "~/" + folders.joinToString("/")
    }
    val name = when {
        folders.isNotEmpty() -> folders.last()
        homed -> "~"
        else -> "/"
    }
    return CardPlace("$shownHost|$absolute", absolute, name, path, shownHost)
}

/** The card of a repository named by no absolute path: its name, without the hex suffix a repository name carries. */
private fun placeOfRepository(repository: String): CardPlace =
    CardPlace("repo:$repository", "", repository.replace(HEX_SUFFIX, ""), "", "")

private fun foldWorktrees(parts: List<String>): List<String> {
    val folders = mutableListOf<String>()
    var index = 0
    while (index < parts.size) {
        val repo = if (index + 1 < parts.size) repoOf(parts[index]) else null
        if (repo != null) {
            folders.add(repo)
            index += 2
        } else {
            folders.add(parts[index])
            index += 1
        }
    }
    return folders
}

/** The repo a `<repo>.worktrees` or `<repo>-worktrees` segment belongs to, or null for any other name. */
private fun repoOf(name: String): String? {
    for (suffix in WORKTREE_SUFFIXES) {
        if (name.endsWith(suffix) && name.length > suffix.length) return name.dropLast(suffix.length)
    }
    return null
}
