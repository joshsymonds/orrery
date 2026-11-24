package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.DesignRow
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
 * A design drawn in the tree: its [design] row and the [kids] hung under it. Not a [Hung]: a design is a block of its
 * own, not a row that hangs under a conversation.
 */
internal data class DesignBlock(val design: DesignRow, val kids: List<Hung>) {
    val key: String = "design:${design.id}"
}

/**
 * One row of a project. [pane] is the conversation's pane or headless session, null for the own row of a thing or a
 * design with no drawn conversation, which holds just that thing or that design. [hung] is what hangs under the row.
 * [folded] is the ended epics of a conversation in the tree, newest first, which fold under the row; a conversation
 * behind the ended line keeps its ended epics in [hung] and has none folded. [designs] are the design blocks drawn on
 * the row, none by default.
 */
internal data class Convo(
    val key: String,
    val pane: Pane?,
    val hung: List<Hung>,
    val folded: List<EpicNode>,
    val designs: List<DesignBlock> = emptyList(),
)

/** What hangs under a row: its own hung things, then the kids of each design block drawn on it. */
internal val Convo.allHung: List<Hung> get() = hung + designs.flatMap { it.kids }

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
    val designs = mutableListOf<DesignBlock>()

    fun toConvo(): Convo = Convo(key, pane, hung.toList(), folded.sortedByDescending { it.epic.changed }, designs.toList())
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

    /** The own row of a design with no drawn conversation: keyed design:<id>, it holds just that design. */
    fun ownDesign(place: CardPlace, block: DesignBlock, ended: Boolean) {
        rows.getOrPut(Triple(ended, place.key, block.key)) { Slot(place, block.key, null, ended) }.designs.add(block)
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

    /** The conversation an id names when a view draws it: archived for the archived view, unarchived for the inbox. */
    fun drawnTarget(id: String, archived: Boolean): Conversation? = target(id)?.takeIf { it.archived == archived }

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
 * A thing one view draws: its [hung] node, its [place], whether it has [ended], and the conversation row it hangs under,
 * null when the view draws no conversation it names.
 */
private class Item(val hung: Hung, val place: CardPlace, val ended: Boolean, val row: Conversation?)

/** A design a view draws: its [block], the conversation row it sits on (null for its own row), and the [kids] it takes. */
private class PlannedDesign(val block: DesignBlock, val row: Conversation?, val kids: List<Item>)

/**
 * Places each conversation and what hangs under it in the inbox: live work and the ended line. An epic or contract whose
 * conversation is not listed, or is archived, draws on its own row in its project. Each conversation draws in one row,
 * behind the ended line when serve marks it ended and in the tree otherwise.
 */
private fun placeInbox(index: Index, slots: Slots, designs: List<DesignRow>) {
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

    val items = buildList {
        for (epic in liveShown) {
            add(Item(index.node(epic), index.placeOfEpic(epic), ended = false, row = index.drawnTarget(epic.steering, false)))
        }
        for (epic in endedShown) {
            add(Item(index.node(epic), index.placeOfEpic(epic), ended = true, row = index.drawnTarget(epic.steering, false)))
        }
        for (contract in contractItems) {
            add(
                Item(
                    ContractNode(contract),
                    index.placeOfContract(contract),
                    ended = false,
                    row = index.drawnTarget(contract.conversation, false),
                ),
            )
        }
    }
    val planned = planDesigns(index, items, designs, inbox = true)
    placeItems(slots, items, takenKeys(planned), behind)
    placeDesigns(slots, planned, behind)
}

/**
 * Places the archived view: each archived conversation with every epic and contract it steered, and each archived epic
 * whose conversation is not archived on its own row. A contract shows here only under an archived conversation, and a
 * design only on its archived conversation's row.
 */
private fun placeArchived(index: Index, slots: Slots, designs: List<DesignRow>) {
    for (conversation in index.conversations.filter { it.archived }) slots.conversation(conversation, ended = false)

    val visible = index.epics.filter { epic -> index.target(epic.steering)?.archived == true || epic.archived }
    val carried = visible.map { it.id }.toSet()
    val items = buildList {
        for (epic in visible) {
            add(
                Item(
                    index.node(epic),
                    index.placeOfEpic(epic),
                    ended = false,
                    row = index.drawnTarget(epic.steering, true),
                ),
            )
        }
        for (contract in index.shownContracts) {
            if (contract.id in carried) continue
            val row = index.drawnTarget(contract.conversation, true) ?: continue
            add(Item(ContractNode(contract), index.placeOfContract(contract), ended = false, row = row))
        }
    }
    val planned = planDesigns(index, items, designs, inbox = false)
    placeItems(slots, items, takenKeys(planned), emptySet())
    placeDesigns(slots, planned, emptySet())
}

/**
 * Plans the designs a view draws, in the order they were created: each design of a conversation the view draws, and in
 * the inbox also each design whose conversation is not drawn but whose kids are. A design takes the items its contracts
 * name, by id or by epic, in position order, and an item one design takes is no other design's kid.
 */
private fun planDesigns(index: Index, items: List<Item>, designs: List<DesignRow>, inbox: Boolean): List<PlannedDesign> {
    val claimed = mutableSetOf<String>()
    val planned = mutableListOf<PlannedDesign>()
    val ordered = designs.filterNot { it.ofOne }.sortedWith(compareBy<DesignRow> { it.created }.thenBy { it.id })
    for (design in ordered) {
        val row = index.drawnTarget(design.conversation, archived = !inbox)
        val kids = kidsOf(design, items, claimed)
        if (row == null && (!inbox || kids.isEmpty())) continue
        claimed += kids.map { it.hung.key }
        planned += PlannedDesign(DesignBlock(design, kids.map { it.hung }), row, kids)
    }
    return planned
}

/**
 * A design's kids: for each of its contracts in position order, the items its id names and then the items its epic
 * names, each once, and never one that another design took.
 */
private fun kidsOf(design: DesignRow, items: List<Item>, taken: Set<String>): List<Item> {
    val kids = mutableListOf<Item>()
    for (contract in design.contracts.sortedBy { it.position }) {
        for (id in listOf(contract.id, contract.epic)) {
            for (item in items) {
                if (id.isNotEmpty() && idOf(item.hung) == id && item.hung.key !in taken && item !in kids) kids.add(item)
            }
        }
    }
    return kids
}

/** The keys of every kid the planned designs take. */
private fun takenKeys(planned: List<PlannedDesign>): Set<String> = planned.flatMap { it.kids }.map { it.hung.key }.toSet()

/** The id a thing is named by: an epic's id, or a contract's. */
private fun idOf(hung: Hung): String = when (hung) {
    is EpicNode -> hung.epic.id
    is ContractNode -> hung.contract.id
}

/**
 * Draws each item no design took: under its drawn row, folded when it is an ended epic of a row in the tree, or on its
 * own row when no conversation it names is drawn.
 */
private fun placeItems(slots: Slots, items: List<Item>, taken: Set<String>, behind: Set<Conversation>) {
    for (item in items) {
        if (item.hung.key in taken) continue
        val row = item.row
        val hung = item.hung
        if (row == null) {
            slots.own(item.place, hung, ended = item.ended)
        } else if (hung is EpicNode && item.ended && row !in behind) {
            slots.conversation(row, ended = false).folded.add(hung)
        } else {
            slots.conversation(row, ended = row in behind).hung.add(hung)
        }
    }
}

/**
 * Draws each planned design: on its conversation's row, wherever serve places that row, or on its own row in the project
 * of its first kid, behind the ended line only when every kid has ended.
 */
private fun placeDesigns(slots: Slots, planned: List<PlannedDesign>, behind: Set<Conversation>) {
    for (design in planned) {
        val row = design.row
        if (row != null) {
            slots.conversation(row, ended = row in behind).designs.add(design.block)
        } else {
            slots.ownDesign(design.kids.first().place, design.block, ended = design.kids.all { it.ended })
        }
    }
}

/**
 * Places the archived view or the inbox, and returns the tree the view draws from the rows, epics, contracts and designs:
 * conversations grouped by project, with what steers each hung under it, each design a block under the conversation
 * that works it, the ended line behind each project, and both ranked.
 */
internal fun inboxTree(
    panes: List<Pane>,
    archivedIds: Set<String>,
    epics: List<EpicRow>,
    details: Map<String, EpicDetail>,
    contracts: List<ContractRow>,
    host: String,
    view: TreeView,
    designs: List<DesignRow> = emptyList(),
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
        TreeView.INBOX -> placeInbox(index, slots, designs)
        TreeView.ARCHIVED -> placeArchived(index, slots, designs)
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

/** Whether a conversation waits on you: its own row waits, or something hung under it or under its designs does. */
internal fun convoWaits(convo: Convo): Boolean =
    convo.pane?.let { waitsOnYou(it) } == true || convo.allHung.any { waits(it) }

private fun rankOf(convo: Convo): Int = when {
    convoWaits(convo) -> RANK_WAITING
    convo.pane?.unread() == true -> RANK_UNSEEN
    convo.pane?.state == STATE_WORKING -> RANK_WORKING
    else -> RANK_REST
}

/** A row's change: its pane's, or for a row with no pane its newest thing under it, design kids included. */
private fun changedOf(convo: Convo): Long =
    convo.pane?.changed ?: convo.allHung.maxOfOrNull { changedOfHung(it) } ?: 0L

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
