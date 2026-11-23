package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class InboxTreeDesignsTest {
    private val serving = "laptop"

    private fun tmux(paneId: String, orreryId: String, cwd: String, state: String = "working") = Pane(
        id = paneId,
        host = serving,
        session = "work",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = 100L,
        seen = 0L,
        question = "",
        questionHash = "",
        orreryId = orreryId,
    )

    private fun epic(id: String, steering: String = "", live: Boolean = true) = EpicRow(
        id = id,
        repository = "/home/user/repo",
        slug = id,
        outcome = "",
        changed = 100L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        steering = steering,
        path = "/home/user/repo",
    )

    private fun contract(id: String, conversation: String = "", repository: String = "/home/user/repo") = ContractRow(
        id = id,
        repository = repository,
        title = id,
        care = "",
        state = "proposed",
        version = 1L,
        openDecisions = 0L,
        conversation = conversation,
        created = 0L,
        changed = 100L,
    )

    /** A contract of a design: its [position], the epic it made, if any, and its repository. */
    private fun kid(
        id: String,
        position: Long,
        epic: String = "",
        repository: String = "/home/user/repo",
    ) = DesignContractRow(
        id = id,
        title = id,
        repository = repository,
        state = "proposed",
        position = position,
        after = emptyList(),
        epic = epic,
        epicPhase = "",
    )

    private fun design(
        id: String,
        conversation: String,
        contracts: List<DesignContractRow>,
        title: String = id,
        ofOne: Boolean = false,
    ) = DesignRow(
        id = id,
        title = title,
        conversation = conversation,
        created = 0L,
        ofOne = ofOne,
        state = "",
        contracts = contracts,
    )

    private fun tree(
        panes: List<Pane> = emptyList(),
        archivedIds: Set<String> = emptySet(),
        epics: List<EpicRow> = emptyList(),
        contracts: List<ContractRow> = emptyList(),
        designs: List<DesignRow> = emptyList(),
        view: TreeView = TreeView.INBOX,
    ): List<Project> = inboxTree(panes, archivedIds, epics, emptyMap(), contracts, serving, view, designs)

    private fun Project.convo(key: String): Convo = convos.single { it.key == key }

    @Test
    fun aDesignsKidsSitInOneBlockOnItsConversationInPositionOrderAndNotLoose() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "o2")),
            contracts = listOf(contract("c2", conversation = "o1"), contract("c3", conversation = "o1")),
            designs = listOf(design("d1", "o1", listOf(kid("c3", 3), kid("c1", 1, epic = "e1"), kid("c2", 2)))),
        ).single()
        assertEquals(emptyList<Hung>(), project.convo("pane:laptop:%1").hung)
        assertEquals(emptyList<Hung>(), project.convo("pane:laptop:%2").hung)
        val block = project.convo("pane:laptop:%1").designs.single()
        assertEquals("d1", block.design.id)
        assertEquals(listOf("epic:e1", "contract:c2", "contract:c3"), block.kids.map { it.key })
    }

    @Test
    fun anEpicThatNoDesignNamesStillHangsLooseUnderItsConversation() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo")),
            epics = listOf(epic("e9", steering = "o1")),
            contracts = listOf(contract("c7", conversation = "o2")),
            designs = listOf(design("d1", "o2", listOf(kid("c7", 1)))),
        ).single()
        assertEquals(listOf("epic:e9"), project.convo("pane:laptop:%1").hung.map { it.key })
        assertEquals(emptyList<DesignBlock>(), project.convo("pane:laptop:%1").designs)
        assertEquals(listOf("contract:c7"), project.convo("pane:laptop:%2").designs.single().kids.map { it.key })
    }

    @Test
    fun aDesignOfOneHangsAsItsContractRowAndIsNoBlock() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            contracts = listOf(contract("c1", conversation = "o1")),
            designs = listOf(design("c1", "o1", listOf(kid("c1", 1)), ofOne = true)),
        ).single()
        assertEquals(listOf("contract:c1"), project.convo("pane:laptop:%1").hung.map { it.key })
        assertEquals(emptyList<DesignBlock>(), project.convo("pane:laptop:%1").designs)
    }

    @Test
    fun aDesignOfAnUnlistedConversationSitsOnTheUnlistedStandInAtItsContractsRepository() {
        val project = tree(
            contracts = listOf(contract("c1", conversation = "ghost", repository = "/home/user/other")),
            designs = listOf(design("d1", "ghost", listOf(kid("c1", 1, repository = "/home/user/other")))),
        ).single()
        assertEquals("|/home/user/other", project.key)
        val row = project.convos.single()
        assertEquals(StandIn.UNLISTED, row.standIn)
        assertNull(row.pane)
        assertEquals(listOf("d1"), row.designs.map { it.design.id })
        assertEquals(listOf("contract:c1"), row.designs.single().kids.map { it.key })
        assertEquals(emptyList<Hung>(), row.hung)
    }

    @Test
    fun theArchivedViewDrawsADesignOnItsArchivedConversationsRowWithItsKidsInside() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo")),
            archivedIds = setOf("o1"),
            epics = listOf(epic("e1", steering = "o1"), epic("e2", steering = "o2")),
            contracts = listOf(contract("c2", conversation = "o1"), contract("c3", conversation = "o2")),
            designs = listOf(
                design("d1", "o1", listOf(kid("c1", 1, epic = "e1"), kid("c2", 2, epic = "e2"))),
                design("d2", "o2", listOf(kid("c3", 1))),
            ),
            view = TreeView.ARCHIVED,
        ).single()
        assertEquals(listOf("pane:laptop:%1"), project.convos.map { it.key })
        assertEquals(emptyList<Hung>(), project.convo("pane:laptop:%1").hung)
        assertEquals(listOf("epic:e1", "contract:c2"), project.convo("pane:laptop:%1").designs.single().kids.map { it.key })
    }

    @Test
    fun aDesignOfAnArchivedConversationSitsOnTheArchivedStandInInTheInbox() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo")),
            archivedIds = setOf("o1"),
            epics = listOf(epic("e1", steering = "o1"), epic("e2", steering = "o2")),
            designs = listOf(design("d1", "o1", listOf(kid("c1", 1, epic = "e1"), kid("c2", 2, epic = "e2")))),
        ).single()
        val standIn = project.convos.single { it.standIn == StandIn.ARCHIVED }
        assertEquals(listOf("d1"), standIn.designs.map { it.design.id })
        assertEquals(listOf("epic:e1", "epic:e2"), standIn.designs.single().kids.map { it.key })
        assertEquals(emptyList<Hung>(), project.convo("pane:laptop:%2").hung)
    }

    @Test
    fun theWaitingChipCountsADesignsProposedContract() {
        val projects = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            contracts = listOf(contract("c1", conversation = "o1")),
            designs = listOf(design("d1", "o1", listOf(kid("c1", 1)))),
        )
        assertEquals(1, countsOf(projects).waiting)
        val narrowed = narrow(projects, Narrow.WAITING).single().convos.single()
        assertEquals(listOf("contract:c1"), narrowed.designs.single().kids.map { it.key })
    }

    @Test
    fun searchMatchesADesignsTitle() {
        val projects = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            contracts = listOf(contract("c1", conversation = "o1")),
            designs = listOf(design("d1", "o1", listOf(kid("c1", 1)), title = "Lighthouse rewrite")),
        )
        val found = search(projects, "lighthouse").single().convos.single()
        assertEquals(listOf("d1"), found.designs.map { it.design.id })
        assertEquals(emptyList<Project>(), search(projects, "zebra"))
    }

    @Test
    fun aDesignWhoseKidsAreAllEndedEpicsSitsOnItsConversationsEndedRow() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            epics = listOf(epic("e1", live = false)),
            designs = listOf(design("d1", "o1", listOf(kid("c1", 1, epic = "e1")))),
        ).single()
        assertEquals(emptyList<DesignBlock>(), project.convo("pane:laptop:%1").designs)
        val ended = project.ended.single()
        assertEquals("pane:laptop:%1", ended.key)
        assertEquals(listOf("epic:e1"), ended.designs.single().kids.map { it.key })
    }

    @Test
    fun aConversationWhoseDesignHasALiveKidIsNotBehindTheEndedLine() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo", state = "done")),
            epics = listOf(epic("e0", steering = "o1", live = false), epic("e1")),
            designs = listOf(design("d1", "o1", listOf(kid("c1", 1, epic = "e1")))),
        ).single()
        assertEquals(listOf("epic:e1"), project.convo("pane:laptop:%1").designs.single().kids.map { it.key })
        assertEquals(listOf("epic:e0"), project.ended.single().hung.map { it.key })
    }
}
