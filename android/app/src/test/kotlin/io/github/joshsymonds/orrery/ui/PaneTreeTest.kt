package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PaneTreeTest {
    private fun pane(
        id: String,
        cwd: String,
        state: String = "working",
        newestReply: Long = 0L,
        seen: Long = 0L,
        changed: Long = 0L,
    ) = Pane(
        id = id,
        host = "host",
        session = "session",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = changed,
        seen = seen,
        question = "",
        questionHash = "",
        newestReply = newestReply,
    )

    private fun folder(key: String, name: String, depth: Int, collapsed: Boolean = false) =
        Folder(key, name, depth, collapsed)

    private fun at(pane: Pane, depth: Int) = PaneAt(pane, depth)

    // R1: nesting, both worktree forms, outside home, same-named folders, collapse, each pane once.

    @Test
    fun panesNestUnderTheirHomeRelativeFolders() {
        val home = pane("home", "/home/josh")
        val a = pane("a", "/home/josh/a")
        val ab = pane("ab", "/home/josh/a/b")
        val rows = treeRows(listOf(home, a, ab), emptySet())
        assertEquals(
            listOf(
                at(home, 0),
                folder("/home/josh/a", "a", 0),
                at(a, 1),
                folder("/home/josh/a/b", "b", 1),
                at(ab, 2),
            ),
            rows,
        )
    }

    @Test
    fun bothWorktreeFormsSitUnderTheirRepo() {
        val repo = pane("repo", "/home/josh/repo")
        val dot = pane("dot", "/home/josh/repo.worktrees/feat")
        val dash = pane("dash", "/home/josh/repo-worktrees/fix")
        val rows = treeRows(listOf(repo, dot, dash), emptySet())
        assertEquals(
            listOf(
                folder("/home/josh/repo", "repo", 0),
                at(repo, 1),
                folder("/home/josh/repo.worktrees/feat", "feat", 1),
                at(dot, 2),
                folder("/home/josh/repo-worktrees/fix", "fix", 1),
                at(dash, 2),
            ),
            rows,
        )
    }

    @Test
    fun worktreeFoldersWithoutAMainCheckoutShareOneRepoFolder() {
        val dot = pane("dot", "/home/josh/repo.worktrees/feat")
        val dash = pane("dash", "/home/josh/repo-worktrees/fix")
        val rows = treeRows(listOf(dot, dash), emptySet())
        val repoFolders = rows.filterIsInstance<Folder>().filter { it.name == "repo" }
        assertEquals(listOf(folder("/home/josh/repo", "repo", 0)), repoFolders)
    }

    @Test
    fun bareWorktreesSegmentIsAnOrdinaryFolder() {
        val bare = pane("bare", "/home/josh/.worktrees/feat")
        val rows = treeRows(listOf(bare), emptySet())
        assertEquals(
            listOf(
                folder("/home/josh/.worktrees", ".worktrees", 0),
                folder("/home/josh/.worktrees/feat", "feat", 1),
                at(bare, 2),
            ),
            rows,
        )
    }

    @Test
    fun paneOutsideHomeSitsUnderTheRootFolderByItsSegments() {
        val outside = pane("outside", "/srv/app/x")
        val rows = treeRows(listOf(outside), emptySet())
        assertEquals(
            listOf(
                folder("/", "/", 0),
                folder("/srv", "srv", 1),
                folder("/srv/app", "app", 2),
                folder("/srv/app/x", "x", 3),
                at(outside, 4),
            ),
            rows,
        )
    }

    @Test
    fun sameNamedFoldersInDifferentPlacesStayApartWithDistinctKeys() {
        val work = pane("work", "/home/josh/work/api")
        val oss = pane("oss", "/home/josh/oss/api")
        val rows = treeRows(listOf(work, oss), emptySet())
        assertEquals(
            listOf(
                folder("/home/josh/oss", "oss", 0),
                folder("/home/josh/oss/api", "api", 1),
                at(oss, 2),
                folder("/home/josh/work", "work", 0),
                folder("/home/josh/work/api", "api", 1),
                at(work, 2),
            ),
            rows,
        )
        val keys = rows.filterIsInstance<Folder>().filter { it.name == "api" }.map { it.key }
        assertEquals(2, keys.toSet().size)
    }

    @Test
    fun sameNamedFoldersUnderDifferentHomesStayApart() {
        val alice = pane("alice", "/home/alice/notes")
        val bob = pane("bob", "/home/bob/notes")
        val rows = treeRows(listOf(alice, bob), emptySet())
        assertEquals(
            listOf(
                folder("/home/alice/notes", "notes", 0),
                at(alice, 1),
                folder("/home/bob/notes", "notes", 0),
                at(bob, 1),
            ),
            rows,
        )
    }

    @Test
    fun usersHomeIsAHomeToo() {
        val mac = pane("mac", "/Users/josh/code")
        val rows = treeRows(listOf(mac), emptySet())
        assertEquals(
            listOf(folder("/Users/josh/code", "code", 0), at(mac, 1)),
            rows,
        )
    }

    @Test
    fun collapsingAFolderHidesEverythingBeneathButKeepsItsHeader() {
        val ab = pane("ab", "/home/josh/a/b")
        val abDeep = pane("deep", "/home/josh/a/b/deep")
        val a = pane("a", "/home/josh/a")
        val d = pane("d", "/home/josh/d")
        val rows = treeRows(listOf(ab, abDeep, a, d), setOf("/home/josh/a"))
        assertEquals(
            listOf(
                folder("/home/josh/a", "a", 0, collapsed = true),
                folder("/home/josh/d", "d", 0),
                at(d, 1),
            ),
            rows,
        )
    }

    @Test
    fun collapsingASubfolderHidesOnlyItsOwnDescendants() {
        val ab = pane("ab", "/home/josh/a/b")
        val abDeep = pane("deep", "/home/josh/a/b/deep")
        val ac = pane("ac", "/home/josh/a/c")
        val a = pane("a", "/home/josh/a")
        val rows = treeRows(listOf(ab, abDeep, ac, a), setOf("/home/josh/a/b"))
        assertEquals(
            listOf(
                folder("/home/josh/a", "a", 0),
                at(a, 1),
                folder("/home/josh/a/b", "b", 1, collapsed = true),
                folder("/home/josh/a/c", "c", 1),
                at(ac, 2),
            ),
            rows,
        )
    }

    @Test
    fun eachPaneAppearsExactlyOnce() {
        val panes = listOf(
            pane("p1", "/home/josh"),
            pane("p2", "/home/josh/repo"),
            pane("p3", "/home/josh/repo.worktrees/feat/src"),
            pane("p4", "/home/josh/repo-worktrees/fix"),
            pane("p5", "/srv/app/x"),
            pane("p6", "/srv"),
            pane("p7", "/"),
        )
        val rows = treeRows(panes, emptySet())
        val appearing = rows.filterIsInstance<PaneAt>().map { it.pane.id }
        assertEquals(panes.map { it.id }.sorted(), appearing.sorted())
    }

    @Test
    fun foldersSortByNameAndListTheirPanesBeforeSubfolders() {
        val zeta = pane("zeta", "/home/josh/zeta")
        val alphaSub = pane("sub", "/home/josh/alpha/sub")
        val alpha = pane("alpha", "/home/josh/alpha")
        val alphaSecond = pane("alpha2", "/home/josh/alpha")
        val rows = treeRows(listOf(zeta, alphaSub, alpha, alphaSecond), emptySet())
        assertEquals(
            listOf(
                folder("/home/josh/alpha", "alpha", 0),
                at(alpha, 1),
                at(alphaSecond, 1),
                folder("/home/josh/alpha/sub", "sub", 1),
                at(alphaSub, 2),
                folder("/home/josh/zeta", "zeta", 0),
                at(zeta, 1),
            ),
            rows,
        )
    }

    @Test
    fun trailingSlashesDoNotChangeThePlace() {
        val a = pane("a", "/home/josh/a///")
        val rows = treeRows(listOf(a), emptySet())
        assertEquals(listOf(folder("/home/josh/a", "a", 0), at(a, 1)), rows)
    }

    // R2: new flat list, newest first, unread rule, with project.

    @Test
    fun unreadMeansNewestReplyIsAfterSeen() {
        assertTrue(pane("u", "/x", newestReply = 200L, seen = 100L).unread())
        assertFalse(pane("r", "/x", newestReply = 100L, seen = 100L).unread())
        assertFalse(pane("o", "/x", newestReply = 50L, seen = 100L).unread())
    }

    @Test
    fun aPaneChangedAfterSeenWithoutANewReplyIsNotNew() {
        val toolCall = pane("tool", "/x", newestReply = 50L, seen = 100L, changed = 500L)
        assertFalse(toolCall.unread())
        assertEquals(emptyList<Pane>(), newPanes(listOf(toolCall)))
    }

    @Test
    fun newPanesKeepUnreadPanesNewestFirstWithTiesInListOrder() {
        val a = pane("a", "/x", newestReply = 300L, seen = 0L)
        val b = pane("b", "/x", newestReply = 500L, seen = 0L)
        val read = pane("read", "/x", newestReply = 900L, seen = 900L)
        val c = pane("c", "/x", newestReply = 500L, seen = 0L)
        assertEquals(listOf(b, c, a), newPanes(listOf(a, b, read, c)))
    }

    @Test
    fun projectOfIsTheFolderPathUnderHomeOrTheAbsolutePathOutside() {
        assertEquals("~", projectOf(pane("h", "/home/josh")))
        assertEquals("~", projectOf(pane("h2", "/home/josh/")))
        assertEquals("a", projectOf(pane("a", "/home/josh/a/")))
        assertEquals("a/b", projectOf(pane("ab", "/home/josh/a/b")))
        assertEquals("repo/feat", projectOf(pane("w", "/home/josh/repo.worktrees/feat")))
        assertEquals("/srv/app/x", projectOf(pane("s", "/srv/app/x")))
        assertEquals("/", projectOf(pane("root", "/")))
    }

    // R3: Waiting is the same flat list, keeping waiting panes only.

    @Test
    fun waitingPanesKeepWaitingStateNewestFirstEvenWhenRead() {
        val oldWait = pane("old", "/x", state = "waiting", newestReply = 100L, seen = 900L)
        val newWait = pane("new", "/x", state = "waiting", newestReply = 400L, seen = 900L)
        val working = pane("work", "/x", state = "working", newestReply = 999L, seen = 0L)
        assertEquals(listOf(newWait, oldWait), waitingPanes(listOf(oldWait, working, newWait)))
    }
}
