package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class InboxTreeTest {
    private val serving = "laptop"

    private fun tmux(
        paneId: String,
        orreryId: String,
        cwd: String,
        state: String = "working",
        host: String = serving,
        changed: Long = 100L,
        newestReply: Long = 0L,
        seen: Long = 0L,
        session: String = "work",
    ) = Pane(
        id = paneId,
        host = host,
        session = session,
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
        orreryId = orreryId,
    )

    private fun headless(
        id: String,
        dir: String,
        state: String = "working",
        changed: Long = 100L,
        newestReply: Long = 0L,
        seen: Long = 0L,
        archived: Boolean = false,
    ) = Pane(
        id = id,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = dir,
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = changed,
        seen = seen,
        question = "",
        questionHash = "",
        newestReply = newestReply,
        headless = true,
        archived = archived,
    )

    private fun epic(
        id: String,
        steering: String = "",
        path: String = "/home/user/repo",
        repository: String = path,
        live: Boolean = true,
        archived: Boolean = false,
        changed: Long = 100L,
    ) = EpicRow(
        id = id,
        repository = repository,
        slug = id,
        outcome = "",
        changed = changed,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        archived = archived,
        steering = steering,
        path = path,
    )

    private fun contract(
        id: String,
        conversation: String = "",
        state: String = "proposed",
        repository: String = "/home/user/repo",
        changed: Long = 100L,
    ) = ContractRow(
        id = id,
        repository = repository,
        title = id,
        care = "",
        state = state,
        version = 1L,
        openDecisions = 0L,
        conversation = conversation,
        created = 0L,
        changed = changed,
    )

    /** A detail whose epic is parked, which raises a park need while the epic is live. */
    private fun parkedDetail(epic: EpicRow) = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        parked = "synthetic park",
    )

    private fun tree(
        panes: List<Pane> = emptyList(),
        archivedIds: Set<String> = emptySet(),
        epics: List<EpicRow> = emptyList(),
        details: Map<String, EpicDetail> = emptyMap(),
        contracts: List<ContractRow> = emptyList(),
        view: TreeView = TreeView.INBOX,
    ): List<Project> = inboxTree(panes, archivedIds, epics, details, contracts, serving, view)

    private fun List<Project>.named(key: String): Project = single { it.key == key }

    private fun Project.convo(key: String): Convo = convos.single { it.key == key }

    // R1: projects are cards by directory, worktrees folded in, the host named only when not the serving one, No
    // directory last.

    @Test
    fun panesInOneDirectoryShareOneProjectNamedByItsLastElement() {
        val project = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/code/lighthouse"),
                tmux("%2", "o2", "/home/user/code/lighthouse"),
            ),
        ).single()
        assertEquals("|/home/user/code/lighthouse", project.key)
        assertEquals("lighthouse", project.name)
        assertEquals("~/code/lighthouse", project.path)
        assertEquals(2, project.convos.size)
    }

    @Test
    fun worktreesOfBothFormsFoldIntoTheirRepository() {
        val projects = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/repo"),
                tmux("%2", "o2", "/home/user/repo.worktrees/feat"),
                tmux("%3", "o3", "/home/user/repo-worktrees/fix"),
            ),
        )
        val project = projects.single()
        assertEquals("|/home/user/repo", project.key)
        assertEquals("repo", project.name)
        assertEquals("~/repo", project.path)
        assertEquals(3, project.convos.size)
    }

    @Test
    fun subdirectoryOfAWorktreeIsItsOwnProjectUnderTheRepository() {
        val project = tree(panes = listOf(tmux("%1", "o1", "/home/user/repo.worktrees/feat/sub"))).single()
        assertEquals("|/home/user/repo/sub", project.key)
        assertEquals("sub", project.name)
        assertEquals("~/repo/sub", project.path)
    }

    @Test
    fun hostIsNamedOnlyWhenTheRowsHostIsNotTheServingOne() {
        val projects = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/app", host = "box"),
                tmux("%2", "o2", "/home/user/app"),
            ),
        )
        assertEquals(setOf("box|/home/user/app", "|/home/user/app"), projects.map { it.key }.toSet())
        assertEquals("box", projects.named("box|/home/user/app").host)
        assertEquals("", projects.named("|/home/user/app").host)
    }

    @Test
    fun headlessSessionSharesItsDirectoryProjectAndCarriesNoHost() {
        val project = tree(
            panes = listOf(headless("h1", "/home/user/repo"), tmux("%1", "o1", "/home/user/repo")),
        ).single()
        assertEquals("|/home/user/repo", project.key)
        assertEquals("", project.host)
        assertEquals(2, project.convos.size)
    }

    @Test
    fun homeItselfIsTildeAndADirectoryOutsideHomeKeepsItsAbsolutePath() {
        val projects = tree(
            panes = listOf(tmux("%1", "o1", "/home/user"), tmux("%2", "o2", "/srv/app")),
        )
        assertEquals("~", projects.named("|/home/user").name)
        assertEquals("~", projects.named("|/home/user").path)
        assertEquals("/srv/app", projects.named("|/srv/app").path)
        assertEquals("app", projects.named("|/srv/app").name)
    }

    @Test
    fun rowWithNoDirectoryIsTheNoDirectoryCardAtTheFootEvenWhenNewest() {
        val projects = tree(
            panes = listOf(
                tmux("%1", "o1", "", changed = 999L),
                tmux("%2", "o2", "/home/user/repo", changed = 1L),
            ),
        )
        assertEquals(listOf("|/home/user/repo", "nodir"), projects.map { it.key })
        assertEquals("No directory", projects.last().name)
    }

    @Test
    fun epicWithNoAbsolutePathHangsInACardNamedByItsRepository() {
        val project = tree(
            epics = listOf(epic("e1", path = "", repository = "lighthouse-abc1234")),
        ).single()
        assertEquals("repo:lighthouse-abc1234", project.key)
        assertEquals("lighthouse", project.name)
        assertEquals("", project.path)
        assertEquals("standin:unlisted:repo:lighthouse-abc1234", project.convos.single().key)
    }

    // R2: each epic hangs under the conversation it steers, each contract under the one that wrote it, and a stand-in
    // row holds what has no listed conversation.

    @Test
    fun epicHangsUnderItsSteeringConversationAndNowhereElse() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "o1")),
        ).single()
        assertEquals(listOf("epic:e1"), project.convo("pane:laptop:%1").hung.map { it.key })
        assertEquals(emptyList<Hung>(), project.convo("pane:laptop:%2").hung)
    }

    @Test
    fun epicSteeredByAHeadlessSessionHangsUnderItsId() {
        val project = tree(
            panes = listOf(headless("h1", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "h1")),
        ).single()
        assertEquals(listOf("epic:e1"), project.convo("session:h1").hung.map { it.key })
    }

    @Test
    fun changingAnEpicsSteererMovesItsRow() {
        val panes = listOf(tmux("%1", "o1", "/home/user/repo"), tmux("%2", "o2", "/home/user/repo"))
        val before = tree(panes = panes, epics = listOf(epic("e1", steering = "o1"))).single()
        val after = tree(panes = panes, epics = listOf(epic("e1", steering = "o2"))).single()
        assertEquals(listOf("epic:e1"), before.convo("pane:laptop:%1").hung.map { it.key })
        assertEquals(listOf("epic:e1"), after.convo("pane:laptop:%2").hung.map { it.key })
        assertEquals(emptyList<Hung>(), after.convo("pane:laptop:%1").hung)
    }

    @Test
    fun proposedContractHangsUnderTheConversationThatWroteIt() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            contracts = listOf(contract("c1", conversation = "o1")),
        ).single()
        assertEquals(listOf("contract:c1"), project.convo("pane:laptop:%1").hung.map { it.key })
    }

    @Test
    fun epicAndContractOfOneIdDrawOnceAsTheEpicNode() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "o1")),
            contracts = listOf(contract("e1", conversation = "o1")),
        ).single()
        val hung = project.convo("pane:laptop:%1").hung
        val node = hung.single() as EpicNode
        assertEquals("e1", node.contract?.id)
    }

    @Test
    fun aListedEpicCarriesOnlyAProposedContractOfItsId() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "o1"), epic("e2", steering = "o1")),
            contracts = listOf(
                contract("e1", conversation = "o1", state = "accepted"),
                contract("e2", conversation = "o1", state = "set_aside"),
            ),
        ).single()
        val nodes = project.convo("pane:laptop:%1").hung.map { it as EpicNode }
        assertEquals(listOf(null, null), nodes.map { it.contract })
    }

    @Test
    fun aStoppedSessionThatWroteAnAcceptedContractWithNoEpicStaysInTheTreeWithIt() {
        val project = tree(
            panes = listOf(headless("h1", "/home/user/repo", state = "stopped")),
            contracts = listOf(contract("c1", conversation = "h1", state = "accepted")),
        ).single()
        assertEquals(listOf("contract:c1"), project.convo("session:h1").hung.map { it.key })
        assertEquals(emptyList<Convo>(), project.ended)
    }

    @Test
    fun anEndedEpicCarryingAProposedContractStaysInTheTreeAndItsConversationWaits() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            epics = listOf(epic("x", steering = "o1", live = false)),
            contracts = listOf(contract("x", conversation = "o1")),
        ).single()
        val convo = project.convo("pane:laptop:%1")
        assertEquals(listOf("epic:x"), convo.hung.map { it.key })
        assertTrue(convoWaits(convo))
        assertEquals(emptyList<Convo>(), project.ended)
    }

    @Test
    fun aProposedContractOfAnArchivedConversationHangsUnderTheArchivedStandIn() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            archivedIds = setOf("o1"),
            contracts = listOf(contract("c1", conversation = "o1")),
        ).single()
        val convo = project.convos.single()
        assertEquals(StandIn.ARCHIVED, convo.standIn)
        assertEquals(listOf("contract:c1"), convo.hung.map { it.key })
    }

    @Test
    fun aStoppedSessionThatProposedAContractStaysInTheTree() {
        val project = tree(
            panes = listOf(headless("h1", "/home/user/repo", state = "stopped")),
            contracts = listOf(contract("c1", conversation = "h1")),
        ).single()
        assertEquals(listOf("contract:c1"), project.convo("session:h1").hung.map { it.key })
        assertEquals(emptyList<Convo>(), project.ended)
    }

    @Test
    fun aStandInHoldingAProposedContractRanksFirstByItsNewestHungThing() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo", changed = 500L)),
            contracts = listOf(contract("c1", conversation = "gone", changed = 50L)),
        ).single()
        assertEquals(listOf("standin:unlisted:|/home/user/repo", "pane:laptop:%1"), project.convos.map { it.key })
    }

    @Test
    fun theArchivedViewHangsAContractUnderTheArchivedConversationThatWroteIt() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            archivedIds = setOf("o1"),
            contracts = listOf(contract("c1", conversation = "o1")),
            view = TreeView.ARCHIVED,
        ).single()
        assertEquals(listOf("contract:c1"), project.convo("pane:laptop:%1").hung.map { it.key })
    }

    @Test
    fun contractsShownAreProposedOrAcceptedWithNoListedEpicAndNeverSetAside() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            epics = listOf(epic("c2", steering = "o1")),
            contracts = listOf(
                contract("c2", conversation = "o1", state = "accepted"),
                contract("c3", conversation = "o1", state = "set_aside"),
                contract("c4", conversation = "o1", state = "accepted"),
            ),
        ).single()
        assertEquals(listOf("epic:c2", "contract:c4"), project.convo("pane:laptop:%1").hung.map { it.key })
    }

    @Test
    fun liveEpicWithNoSteererOrAMissingConversationHangsUnderTheUnlistedStandIn() {
        val projects = tree(
            epics = listOf(epic("e1", steering = ""), epic("e2", steering = "gone")),
        )
        val convo = projects.single().convos.single()
        assertEquals("standin:unlisted:|/home/user/repo", convo.key)
        assertEquals(StandIn.UNLISTED, convo.standIn)
        assertEquals(null, convo.pane)
        assertEquals(listOf("epic:e1", "epic:e2"), convo.hung.map { it.key })
    }

    @Test
    fun contractWithNoListedConversationHangsInItsRepositoryCardUnderTheUnlistedStandIn() {
        val projects = tree(
            contracts = listOf(contract("c1", conversation = "", repository = "lighthouse")),
        )
        val project = projects.single()
        assertEquals("repo:lighthouse", project.key)
        assertEquals(listOf("contract:c1"), project.convos.single().hung.map { it.key })
        assertEquals(StandIn.UNLISTED, project.convos.single().standIn)
    }

    @Test
    fun liveEpicUnderAnArchivedConversationHangsUnderTheArchivedStandIn() {
        val project = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            archivedIds = setOf("o1"),
            epics = listOf(epic("e1", steering = "o1")),
        ).single()
        val convo = project.convos.single()
        assertEquals("standin:archived:|/home/user/repo", convo.key)
        assertEquals(StandIn.ARCHIVED, convo.standIn)
        assertEquals(listOf("epic:e1"), convo.hung.map { it.key })
    }

    @Test
    fun archivedEpicsAndArchivedConversationsAreOutOfTheInbox() {
        val projects = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/repo"),
                tmux("%2", "o2", "/home/user/other"),
                headless("h1", "/home/user/other", archived = true),
            ),
            archivedIds = setOf("o2"),
            epics = listOf(epic("e1", steering = "o1", archived = true)),
        )
        assertEquals(listOf("|/home/user/repo"), projects.map { it.key })
        assertEquals(emptyList<Hung>(), projects.single().convos.single().hung)
    }

    @Test
    fun shellSessionPanesAreNeverConversations() {
        val projects = tree(panes = listOf(tmux("%1", "o1", "/home/user/repo", session = "_orrery-shell-1")))
        assertTrue(projects.isEmpty())
    }

    @Test
    fun terminalPaneIsARowWithNothingUnderIt() {
        val convo = tree(panes = listOf(tmux("%1", "o1", "/home/user/repo", state = ""))).single().convos.single()
        assertEquals("pane:laptop:%1", convo.key)
        assertEquals(emptyList<Hung>(), convo.hung)
    }

    // R3: rank within a project, the ended line and its parents, and the archived view.

    @Test
    fun conversationsRankWaitingUnseenWorkingThenTheRestNewestFirstWithinARank() {
        val project = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/repo", state = "idle", changed = 400L),
                tmux("%2", "o2", "/home/user/repo", state = "working", changed = 300L),
                tmux("%3", "o3", "/home/user/repo", state = "working", changed = 200L, newestReply = 200L, seen = 100L),
                tmux("%4", "o4", "/home/user/repo", state = "waiting", changed = 100L),
                tmux("%5", "o5", "/home/user/repo", state = "working", changed = 500L),
            ),
        ).single()
        assertEquals(
            listOf("pane:laptop:%4", "pane:laptop:%3", "pane:laptop:%5", "pane:laptop:%2", "pane:laptop:%1"),
            project.convos.map { it.key },
        )
    }

    @Test
    fun conversationWaitsThroughAnEpicThatNeedsYouAndRanksWithWaiting() {
        val blocked = epic("e6", steering = "o6", changed = 50L)
        val project = tree(
            panes = listOf(
                tmux("%2", "o2", "/home/user/repo", changed = 300L, newestReply = 200L, seen = 100L),
                tmux("%6", "o6", "/home/user/repo", changed = 50L),
            ),
            epics = listOf(blocked),
            details = mapOf("e6" to parkedDetail(blocked)),
        ).single()
        assertEquals("pane:laptop:%6", project.convos.first().key)
    }

    @Test
    fun projectsRankByTheirBestConversationThenNewestAndNoDirectoryLast() {
        val projects = tree(
            panes = listOf(
                tmux("%1", "o1", "/home/user/alpha", state = "idle", changed = 900L),
                tmux("%2", "o2", "/home/user/beta", state = "waiting", changed = 100L),
                tmux("%3", "o3", "/home/user/gamma", state = "idle", changed = 1000L),
                tmux("%4", "o4", "", state = "waiting", changed = 9999L),
            ),
        )
        assertEquals(
            listOf("|/home/user/beta", "|/home/user/gamma", "|/home/user/alpha", "nodir"),
            projects.map { it.key },
        )
    }

    @Test
    fun stoppedHeadlessSessionsGoBehindTheEndedLineNewestFirst() {
        val project = tree(
            panes = listOf(
                headless("h1", "/home/user/repo", state = "stopped", changed = 10L),
                headless("h4", "/home/user/repo", state = "stopped", changed = 20L),
            ),
        ).single()
        assertTrue(project.convos.isEmpty())
        assertEquals(listOf("session:h4", "session:h1"), project.ended.map { it.key })
    }

    @Test
    fun failedHeadlessSessionEndsOnlyOnceSeen() {
        val project = tree(
            panes = listOf(
                headless("h2", "/home/user/repo", state = "failed", newestReply = 0L, seen = 0L),
                headless("h3", "/home/user/repo", state = "failed", newestReply = 50L, seen = 10L),
            ),
        ).single()
        assertEquals(listOf("session:h3"), project.convos.map { it.key })
        assertEquals(listOf("session:h2"), project.ended.map { it.key })
    }

    @Test
    fun doneConversationSteeringOnlyEndedEpicsGoesBehindTheLineWithThem() {
        val ended = epic("e5", steering = "o5", live = false)
        val project = tree(
            panes = listOf(tmux("%5", "o5", "/home/user/repo", state = "done")),
            epics = listOf(ended),
        ).single()
        assertTrue(project.convos.isEmpty())
        val parent = project.ended.single()
        assertEquals("pane:laptop:%5", parent.key)
        assertFalse(parent.again)
        assertEquals(listOf("epic:e5"), parent.hung.map { it.key })
    }

    @Test
    fun doneConversationWithNoEndedEpicStaysInTheTree() {
        val project = tree(panes = listOf(tmux("%5", "o5", "/home/user/repo", state = "done"))).single()
        assertEquals(listOf("pane:laptop:%5"), project.convos.map { it.key })
        assertTrue(project.ended.isEmpty())
    }

    @Test
    fun conversationSteeringALiveEpicStaysInTheTreeEvenWhenStopped() {
        val project = tree(
            panes = listOf(headless("h1", "/home/user/repo", state = "stopped")),
            epics = listOf(epic("e1", steering = "h1")),
        ).single()
        assertEquals(listOf("session:h1"), project.convos.map { it.key })
        assertEquals(listOf("epic:e1"), project.convos.single().hung.map { it.key })
        assertTrue(project.ended.isEmpty())
    }

    @Test
    fun endedEpicOfAConversationInTheTreeIsDrawnAgainBehindTheLine() {
        val project = tree(
            panes = listOf(tmux("%6", "o6", "/home/user/repo")),
            epics = listOf(epic("e1", steering = "o6"), epic("e2", steering = "o6", live = false)),
        ).single()
        assertEquals(listOf("epic:e1"), project.convo("pane:laptop:%6").hung.map { it.key })
        val again = project.ended.single()
        assertEquals("pane:laptop:%6", again.key)
        assertTrue(again.again)
        assertEquals(listOf("epic:e2"), again.hung.map { it.key })
    }

    @Test
    fun endedEpicWithNoSteererSitsBehindTheEndedLineUnderTheUnlistedStandIn() {
        val project = tree(epics = listOf(epic("e1", steering = "", live = false))).single()
        assertTrue(project.convos.isEmpty())
        val parent = project.ended.single()
        assertEquals(StandIn.UNLISTED, parent.standIn)
        assertEquals(listOf("epic:e1"), parent.hung.map { it.key })
    }

    @Test
    fun archivedViewHangsEverySteeredEpicUnderItsArchivedConversation() {
        val projects = tree(
            panes = listOf(tmux("%1", "o1", "/home/user/repo")),
            archivedIds = setOf("o1"),
            epics = listOf(epic("e1", steering = "o1"), epic("e2", steering = "o1", live = false)),
            view = TreeView.ARCHIVED,
        )
        val project = projects.single()
        assertEquals(listOf("epic:e1", "epic:e2"), project.convo("pane:laptop:%1").hung.map { it.key })
        assertTrue(project.ended.isEmpty())
    }

    @Test
    fun archivedViewDrawsAnArchivedEpicUnderItsLiveConversationAsItsParent() {
        val panes = listOf(tmux("%2", "o2", "/home/user/repo"))
        val archived = tree(
            panes = panes,
            epics = listOf(epic("e3", steering = "o2", archived = true)),
            view = TreeView.ARCHIVED,
        ).single()
        assertEquals(listOf("epic:e3"), archived.convo("pane:laptop:%2").hung.map { it.key })
        val inbox = tree(panes = panes, epics = listOf(epic("e3", steering = "o2", archived = true))).single()
        assertEquals(emptyList<Hung>(), inbox.convo("pane:laptop:%2").hung)
    }

    // R2 and R3 through the functions the counts and rows read: waits per rule 8, and a conversation's convoWaits.

    @Test
    fun anEpicWaitsWhenItsDetailRaisesANeedAndAnArchivedOneNeverDoes() {
        val live = epic("e1")
        assertTrue(waits(EpicNode(live, parkedDetail(live), null)))
        val archived = epic("e2", archived = true)
        assertFalse(waits(EpicNode(archived, parkedDetail(archived), null)))
    }

    @Test
    fun aProposedContractWaitsAndAnAcceptedOneDoesNot() {
        assertTrue(waits(ContractNode(contract("c1", state = "proposed"))))
        assertFalse(waits(ContractNode(contract("c2", state = "accepted"))))
        assertTrue(waits(EpicNode(epic("e3"), null, contract("e3", state = "proposed"))))
    }

    @Test
    fun aConversationWaitsWhenItsRowWaitsOrAHungThingWaits() {
        val waitingPane = tmux("%1", "o1", "/home/user/repo", state = "waiting")
        val waitingRow = Convo("pane:laptop:%1", waitingPane, StandIn.NONE, false, emptyList())
        assertTrue(convoWaits(waitingRow))
        val workingRow = Convo("pane:laptop:%2", tmux("%2", "o2", "/home/user/repo"), StandIn.NONE, false, emptyList())
        assertFalse(convoWaits(workingRow))
        val standIn = Convo(
            "standin:unlisted:|/home/user/repo",
            null,
            StandIn.UNLISTED,
            false,
            listOf(ContractNode(contract("c1"))),
        )
        assertTrue(convoWaits(standIn))
    }
}
