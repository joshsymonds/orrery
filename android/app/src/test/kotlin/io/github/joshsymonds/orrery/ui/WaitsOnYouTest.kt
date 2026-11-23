package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WaitsOnYouTest {
    private val serving = "laptop"

    private fun pane(
        id: String,
        orreryId: String,
        state: String,
        cwd: String = "/home/user/repo",
        changed: Long = 100L,
        putOff: Boolean = false,
        archived: Boolean = false,
    ) = Pane(
        id = id,
        host = serving,
        session = "work",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = changed,
        seen = 0L,
        question = "",
        questionHash = "",
        orreryId = orreryId,
        putOff = putOff,
        archived = archived,
    )

    private fun headless(id: String, state: String, putOff: Boolean = false, archived: Boolean = false) = Pane(
        id = id,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/repo",
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = 100L,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = true,
        putOff = putOff,
        archived = archived,
    )

    private fun epic(id: String, steering: String) = EpicRow(
        id = id,
        repository = "/home/user/repo",
        slug = id,
        outcome = "",
        changed = 100L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        archived = false,
        steering = steering,
        path = "/home/user/repo",
    )

    private fun parked(epic: EpicRow, plannerParked: Boolean = false, steererParked: Boolean = false) = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        steps = emptyList(),
        questions = emptyList(),
        parked = "held",
        plannerParked = plannerParked,
        steererParked = steererParked,
    )

    private fun tree(
        panes: List<Pane> = emptyList(),
        archivedIds: Set<String> = emptySet(),
        epics: List<EpicRow> = emptyList(),
        details: Map<String, EpicDetail> = emptyMap(),
        view: TreeView = TreeView.INBOX,
    ): List<Project> = inboxTree(panes, archivedIds, epics, details, emptyList(), serving, view)

    private fun List<Project>.named(key: String): Project = single { it.key == key }

    @Test
    fun countsOfLeavesOutAWaitingConversationPutOff() {
        val projects = tree(panes = listOf(pane("%1", "o1", "waiting", putOff = true)))
        assertEquals(0, countsOf(projects).waiting)
    }

    @Test
    fun countsOfCountsAWaitingConversationAgainOnceItsQuestionIsNotPutOff() {
        val projects = tree(panes = listOf(pane("%1", "o1", "waiting", putOff = false)))
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun projectWaitingLeavesOutAPutOffWaitingConversationOfAFoldedProject() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", "waiting", cwd = "/home/user/a", putOff = true),
                pane("%2", "o2", "waiting", cwd = "/home/user/b"),
            ),
        )
        assertEquals(0, projectWaiting(projects.named("|/home/user/a")))
        assertEquals(1, projectWaiting(projects.named("|/home/user/b")))
    }

    @Test
    fun narrowWaitingDropsAPutOffWaitingRow() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", "waiting", putOff = true),
                pane("%2", "o2", "waiting"),
            ),
        )
        val kept = narrow(projects, Narrow.WAITING).single()
        assertEquals(setOf("pane:laptop:%2"), kept.convos.map { it.key }.toSet())
    }

    @Test
    fun aPutOffWaitingRowRanksAsNotWaiting() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", "waiting", changed = 100L, putOff = true),
                pane("%2", "o2", "working", changed = 200L),
            ),
        )
        assertEquals(
            listOf("pane:laptop:%2", "pane:laptop:%1"),
            projects.single().convos.map { it.key },
        )
    }

    @Test
    fun aPutOffWaitingRowStillCountsWhatWaitsUnderItAndStaysInTheWaitingNarrow() {
        val e = epic("e1", steering = "o1")
        val projects = tree(
            panes = listOf(pane("%1", "o1", "waiting", putOff = true)),
            epics = listOf(e),
            details = mapOf("e1" to parked(e)),
        )
        assertEquals(1, countsOf(projects).waiting)
        val kept = narrow(projects, Narrow.WAITING).single().convos.single()
        assertEquals("pane:laptop:%1", kept.key)
        assertEquals(listOf("epic:e1"), kept.hung.map { it.key })
    }

    @Test
    fun anEpicWhosePlannerOrSteererHoldsItsParkDoesNotWaitOnYou() {
        val e = epic("e1", steering = "o1")
        for (held in listOf(parked(e, plannerParked = true), parked(e, steererParked = true))) {
            val projects = tree(
                panes = listOf(pane("%1", "o1", "working")),
                epics = listOf(e),
                details = mapOf("e1" to held),
            )
            assertEquals(0, countsOf(projects).waiting)
            assertFalse(waits(EpicNode(e, held, contract = null)))
        }
        assertTrue(waits(EpicNode(e, parked(e), contract = null)))
    }

    @Test
    fun narrowWaitingNeverKeepsAnArchivedWaitingRow() {
        val projects = tree(
            panes = listOf(headless("h1", "waiting", archived = true)),
            view = TreeView.ARCHIVED,
        )
        assertEquals(emptyList<Project>(), narrow(projects, Narrow.WAITING))
    }

    @Test
    fun ownAsksLeavesOutAPutOffAndAnArchivedWaitingPane() {
        assertFalse(ownAsks(pane("%1", "o1", "waiting", putOff = true)))
        assertFalse(ownAsks(pane("%1", "o1", "waiting", archived = true)))
        assertTrue(ownAsks(pane("%1", "o1", "waiting", putOff = false)))
    }

    @Test
    fun countsOfLeavesOutAnArchivedWaitingConversation() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", "waiting")),
            archivedIds = setOf("o1"),
            view = TreeView.ARCHIVED,
        )
        assertEquals(1, projects.single().convos.size)
        assertEquals(0, countsOf(projects).waiting)
    }

    @Test
    fun projectWaitingLeavesOutAnArchivedWaitingConversation() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", "waiting", cwd = "/home/user/a")),
            archivedIds = setOf("o1"),
            view = TreeView.ARCHIVED,
        )
        assertEquals(1, projects.single().convos.size)
        assertEquals(0, projectWaiting(projects.single()))
    }

    @Test
    fun anArchivedWaitingRowCountsNothingHungUnderItInItsCountsOrItsProject() {
        val e = epic("e1", steering = "o1")
        val projects = tree(
            panes = listOf(pane("%1", "o1", "waiting")),
            archivedIds = setOf("o1"),
            epics = listOf(e),
            details = mapOf("e1" to parked(e)),
            view = TreeView.ARCHIVED,
        )
        assertEquals(listOf("epic:e1"), projects.single().convos.single().hung.map { it.key })
        assertEquals(0, countsOf(projects).waiting)
        assertEquals(0, projectWaiting(projects.single()))
    }
}
