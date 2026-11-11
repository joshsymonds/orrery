package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Pane

/** One row of the pane list's tree: a folder header, or a pane under its folder. */
internal sealed interface TreeRow

/** A folder header. [depth] is 0 at the top level; [key] is the folder's home plus its path. */
internal data class Folder(val key: String, val name: String, val depth: Int, val collapsed: Boolean) : TreeRow

/** A pane under its folder: [depth] is the folder's depth plus one, and 0 at the top level. */
internal data class PaneAt(val pane: Pane, val depth: Int) : TreeRow

/** One folder on a pane's path. [name] is what the tree shows; [key] tells folders apart. */
private data class Link(val key: String, val name: String)

/** Where a pane sits: [homed] when its cwd is under a home, and its folders from the top down. */
private class Place(val homed: Boolean, val folders: List<Link>)

/** The top-level folder for cwds outside every home. */
private val ROOT = Link("/", "/")

private val HOME_PARENTS = setOf("home", "Users")

private val WORKTREE_SUFFIXES = listOf(".worktrees", "-worktrees")

private class FolderNode(val key: String, val name: String, val depth: Int) {
    val children = LinkedHashMap<String, FolderNode>()
    val panes = mutableListOf<Pane>()
}

internal fun treeRows(panes: List<Pane>, collapsed: Set<String>): List<TreeRow> {
    val top = FolderNode("", "", -1)
    for (pane in panes) {
        val place = placeOf(pane.cwd)
        val chain = if (place.homed) place.folders else listOf(ROOT) + place.folders
        var parent = top
        for (link in chain) {
            val depth = parent.depth + 1
            parent = parent.children.getOrPut(link.key) { FolderNode(link.key, link.name, depth) }
        }
        parent.panes += pane
    }
    val rows = mutableListOf<TreeRow>()
    emitRows(top, collapsed, rows)
    return rows
}

/** A folder's panes in list order, then its subfolders by name; a collapsed folder hides all of it. */
private fun emitRows(node: FolderNode, collapsed: Set<String>, rows: MutableList<TreeRow>) {
    for (pane in node.panes) rows += PaneAt(pane, node.depth + 1)
    for (child in node.children.values.sortedWith(compareBy<FolderNode>({ it.name }, { it.key }))) {
        val folded = child.key in collapsed
        rows += Folder(child.key, child.name, child.depth, folded)
        if (!folded) emitRows(child, collapsed, rows)
    }
}

private fun placeOf(cwd: String): Place {
    val parts = cwd.split('/').filter { it.isNotEmpty() }
    val homed = parts.size >= 2 && parts[0] in HOME_PARENTS
    val home = if (homed) "/${parts[0]}/${parts[1]}" else ""
    val rest = if (homed) parts.drop(2) else parts
    val raw = mutableListOf<String>()
    val folders = rest.mapIndexed { index, name ->
        raw += name
        val repo = if (index < rest.lastIndex) repoOfWorktrees(name) else null
        val path = if (repo != null) raw.dropLast(1) + repo else raw
        Link("$home/${path.joinToString("/")}", repo ?: name)
    }
    return Place(homed, folders)
}

/** The repo a `<repo>.worktrees` or `<repo>-worktrees` segment belongs to, or null for any other name. */
private fun repoOfWorktrees(name: String): String? {
    for (suffix in WORKTREE_SUFFIXES) {
        if (name.endsWith(suffix)) {
            val repo = name.removeSuffix(suffix)
            if (repo.isNotEmpty()) return repo
        }
    }
    return null
}

internal fun Pane.unread(): Boolean = newestReply > seen

internal fun newPanes(panes: List<Pane>): List<Pane> =
    panes.filter { it.unread() }.sortedByDescending { it.newestReply }

internal fun waitingPanes(panes: List<Pane>): List<Pane> =
    panes.filter { it.state == "waiting" }.sortedByDescending { it.newestReply }

/** The pane's folder path: `~` at a home itself, the absolute path outside a home. */
internal fun projectOf(pane: Pane): String {
    val place = placeOf(pane.cwd)
    val names = place.folders.joinToString("/") { it.name }
    return when {
        !place.homed -> "/$names"
        names.isEmpty() -> "~"
        else -> names
    }
}
