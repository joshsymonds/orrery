package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class InboxTreeFilterTest {
    private val serving = "laptop"

    private fun pane(
        id: String,
        orreryId: String,
        cwd: String = "/home/user/repo",
        state: String = "working",
        label: String = "",
        summary: String = "",
        newestReply: Long = 0L,
        seen: Long = 0L,
    ) = Pane(
        id = id,
        host = serving,
        session = "work",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "",
        state = state,
        summary = summary,
        label = label,
        changed = 100L,
        seen = seen,
        question = "",
        questionHash = "",
        newestReply = newestReply,
        orreryId = orreryId,
    )

    private fun headless(
        id: String,
        state: String = "working",
        label: String = "",
        newestReply: Long = 0L,
        seen: Long = 0L,
        archived: Boolean = false,
    ) = Pane(
        id = id,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/repo",
        command = "",
        state = state,
        summary = "",
        label = label,
        changed = 100L,
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
        slug: String = id,
        outcome: String = "",
        live: Boolean = true,
        archived: Boolean = false,
    ) = EpicRow(
        id = id,
        repository = "/home/user/repo",
        slug = slug,
        outcome = outcome,
        changed = 100L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        archived = archived,
        steering = steering,
        path = "/home/user/repo",
    )

    private fun contract(
        id: String,
        conversation: String,
        state: String = "proposed",
        title: String = id,
    ) = ContractRow(
        id = id,
        repository = "/home/user/repo",
        title = title,
        care = "",
        state = state,
        version = 1L,
        openDecisions = 0L,
        conversation = conversation,
        created = 0L,
        changed = 100L,
    )

    /** A detail that raises a need while its epic is live: a park when [parked] is set, else each question. */
    private fun detail(
        epic: EpicRow,
        parked: String = "",
        questions: List<EpicQuestionRow> = emptyList(),
    ) = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        questions = questions,
        parked = parked,
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

    private fun keysOf(convos: List<Convo>): Set<String> = convos.map { it.key }.toSet()

    // R4: the waiting chip counts one per thing: a waiting conversation, a live epic with a need, a proposed contract.

    @Test
    fun countsWaitingCountsAWaitingConversationOnce() {
        val projects = tree(panes = listOf(pane("%1", "o1", state = "waiting")))
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun countsWaitingCountsAnEpicOnceHoweverManyQuestionsItHas() {
        val e = epic("e1", steering = "o1")
        val questions = listOf(EpicQuestionRow("q1", "Which?", 1L, 1L), EpicQuestionRow("q2", "When?", 1L, 1L))
        val projects = tree(
            panes = listOf(pane("%1", "o1")),
            epics = listOf(e),
            details = mapOf("e1" to detail(e, questions = questions)),
        )
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun countsWaitingDoesNotCountAConversationForWhatWaitsUnderIt() {
        val e = epic("e1", steering = "o1")
        val projects = tree(
            panes = listOf(pane("%1", "o1")),
            epics = listOf(e),
            details = mapOf("e1" to detail(e, parked = "held")),
        )
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun countsWaitingCountsAProposedContractUnderAWorkingConversation() {
        val projects = tree(
            panes = listOf(pane("%1", "o1")),
            contracts = listOf(contract("c1", conversation = "o1")),
        )
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun countsWaitingNeverCountsAContractAgainUnderItsWaitingConversation() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", state = "waiting")),
            contracts = listOf(contract("c1", conversation = "o1")),
        )
        assertEquals(1, countsOf(projects).waiting)
    }

    @Test
    fun countsOfTheArchivedViewCountNothingArchived() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", state = "waiting"),
                headless("h1", state = "waiting", archived = true),
            ),
            archivedIds = setOf("o1"),
            view = TreeView.ARCHIVED,
        )
        assertEquals(Counts(0, 0), countsOf(projects))
    }

    @Test
    fun countsWaitingNeverCountsArchivedThings() {
        val e = epic("e1", steering = "o1", archived = true)
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", state = "waiting"),
                pane("%2", "o2"),
                headless("h1", state = "waiting", archived = true),
            ),
            archivedIds = setOf("o1"),
            epics = listOf(e),
            details = mapOf("e1" to detail(e, parked = "held")),
        )
        assertEquals(0, countsOf(projects).waiting)
    }

    // R2: a live epic or proposed contract whose conversation is archived hangs under the archived stand-in and counts.

    @Test
    fun countsWaitingCountsALiveEpicThatNeedsYouUnderAnArchivedStandIn() {
        val e = epic("e1", steering = "o1")
        val projects = tree(
            panes = listOf(pane("%1", "o1")),
            archivedIds = setOf("o1"),
            epics = listOf(e),
            details = mapOf("e1" to detail(e, parked = "held")),
        )
        val convo = projects.single().convos.single()
        assertEquals(StandIn.ARCHIVED, convo.standIn)
        assertEquals(listOf("epic:e1"), convo.hung.map { it.key })
        assertEquals(1, countsOf(projects).waiting)
        assertEquals(1, projectWaiting(projects.single()))
    }

    @Test
    fun countsWaitingCountsAProposedContractUnderAnArchivedStandIn() {
        val projects = tree(
            panes = listOf(pane("%1", "o1")),
            archivedIds = setOf("o1"),
            contracts = listOf(contract("c1", conversation = "o1")),
        )
        val convo = projects.single().convos.single()
        assertEquals(StandIn.ARCHIVED, convo.standIn)
        assertEquals(listOf("contract:c1"), convo.hung.map { it.key })
        assertEquals(1, countsOf(projects).waiting)
        assertEquals(1, projectWaiting(projects.single()))
    }

    // R4: the new chip counts unseen rows, each once, including rows behind the ended line.

    @Test
    fun countsFreshCountsEachUnseenRowOnceIncludingOneBehindTheEndedLine() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", newestReply = 5L),
                pane("%2", "o2", newestReply = 5L, seen = 5L),
                headless("h1", state = "stopped", newestReply = 5L),
            ),
            epics = listOf(epic("x", steering = "o1", live = false)),
        )
        assertEquals(2, countsOf(projects).fresh)
    }

    // R4: a folded project's waiting number is the chip's count over that project.

    @Test
    fun aFoldedProjectsWaitingNumberIsCountsOfOverIt() {
        val e = epic("e1", steering = "o3")
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", cwd = "/home/user/a", state = "waiting"),
                pane("%2", "o2", cwd = "/home/user/b", state = "waiting"),
                pane("%3", "o3", cwd = "/home/user/b"),
            ),
            epics = listOf(e),
            details = mapOf("e1" to detail(e, parked = "held")),
        )
        val a = projects.named("|/home/user/a")
        val b = projects.named("|/home/user/b")
        assertEquals(1, projectWaiting(a))
        assertEquals(2, projectWaiting(b))
        assertEquals(countsOf(listOf(b)).waiting, projectWaiting(b))
    }

    // R4: the waiting chip narrows to what waits, keeping each conversation as the parent of what waits under it.

    @Test
    fun narrowWaitingKeepsEachConversationAsTheParentOfWhatWaitsUnderIt() {
        val waiting = epic("e1", steering = "o1")
        val projects = tree(
            panes = listOf(pane("%1", "o1"), pane("%2", "o2"), pane("%3", "o3", state = "waiting")),
            epics = listOf(waiting, epic("e2", steering = "o1")),
            details = mapOf("e1" to detail(waiting, parked = "held")),
        )
        val kept = narrow(projects, Narrow.WAITING).single()
        assertEquals(setOf("pane:laptop:%1", "pane:laptop:%3"), keysOf(kept.convos))
        assertEquals(setOf("epic:e1"), kept.convo("pane:laptop:%1").hung.map { it.key }.toSet())
    }

    @Test
    fun narrowWaitingDropsTheEndedLine() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", state = "waiting")),
            epics = listOf(epic("x", steering = "o1", live = false)),
        )
        assertTrue(projects.single().ended.isNotEmpty())
        assertEquals(emptyList<Convo>(), narrow(projects, Narrow.WAITING).single().ended)
    }

    // R4: the new chip narrows to unseen rows, each with nothing hung under it.

    @Test
    fun narrowNewKeepsUnseenRowsWithNothingUnderThemIncludingThoseBehindTheEndedLine() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", newestReply = 5L),
                pane("%2", "o2", newestReply = 5L, seen = 5L),
                headless("h1", state = "stopped", newestReply = 5L),
            ),
            epics = listOf(epic("e1", steering = "o1")),
        )
        val kept = narrow(projects, Narrow.NEW).single()
        assertEquals(setOf("pane:laptop:%1", "session:h1"), keysOf(kept.convos))
        assertEquals(emptyList<Hung>(), kept.convo("pane:laptop:%1").hung)
        assertEquals(emptyList<Convo>(), kept.ended)
    }

    // R5: search matches every word, ignoring case, against a row's label, state and host, a project's name and path,
    // an epic's slug and outcome and a contract's title, and keeps a match's conversation and project.

    @Test
    fun searchIgnoresCase() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", label = "Fix Login"), pane("%2", "o2", label = "Billing")),
        )
        assertEquals(setOf("pane:laptop:%1"), keysOf(search(projects, "LOGIN").single().convos))
    }

    @Test
    fun searchNeedsEveryWordToMatch() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", label = "fix login"), pane("%2", "o2", label = "fix billing")),
        )
        assertEquals(setOf("pane:laptop:%1"), keysOf(search(projects, "fix login").single().convos))
    }

    @Test
    fun searchMatchesARowsStateAndReadsItsSummaryWhenItHasNoLabel() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", state = "waiting", label = "notes"),
                pane("%2", "o2", summary = "refactor parser"),
                pane("%3", "o3", label = "notes"),
            ),
        )
        assertEquals(setOf("pane:laptop:%1"), keysOf(search(projects, "waiting").single().convos))
        assertEquals(setOf("pane:laptop:%2"), keysOf(search(projects, "parser").single().convos))
    }

    @Test
    fun searchMatchesARowsHostAndDropsTheOtherHosts() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", label = "notes").copy(host = "devbox"),
                pane("%2", "o2", label = "notes"),
            ),
        )
        assertEquals(setOf("pane:devbox:%1"), keysOf(search(projects, "devbox").single().convos))
    }

    @Test
    fun searchKeepsAMatchingEpicWithItsConversationAndProjectAndDropsTheRest() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", label = "notes"),
                pane("%2", "o2", cwd = "/home/user/other", label = "other"),
            ),
            epics = listOf(epic("e1", steering = "o1", slug = "tide-alerts"), epic("e2", steering = "o1", slug = "cleanup")),
        )
        val kept = search(projects, "tide").single()
        assertEquals("|/home/user/repo", kept.key)
        assertEquals(setOf("epic:e1"), kept.convo("pane:laptop:%1").hung.map { it.key }.toSet())
    }

    @Test
    fun searchFindsAContractByItsTitleAndDropsItsNonMatchingConversations() {
        val projects = tree(
            panes = listOf(pane("%1", "o1", label = "notes"), pane("%2", "o2", label = "notes")),
            contracts = listOf(contract("c1", conversation = "o1", title = "Auth rewrite")),
        )
        val kept = search(projects, "rewrite").single()
        assertEquals(setOf("pane:laptop:%1"), keysOf(kept.convos))
        assertEquals(setOf("contract:c1"), kept.convo("pane:laptop:%1").hung.map { it.key }.toSet())
    }

    @Test
    fun searchKeepsEverythingHungUnderARowWhoseProjectMatches() {
        val projects = tree(
            panes = listOf(
                pane("%1", "o1", cwd = "/home/user/lighthouse", label = "a"),
                pane("%2", "o2", cwd = "/home/user/other", label = "b"),
            ),
            epics = listOf(epic("e1", steering = "o1", slug = "x"), epic("e2", steering = "o1", slug = "y")),
        )
        val kept = search(projects, "lighthouse").single()
        assertEquals(setOf("pane:laptop:%1"), keysOf(kept.convos))
        assertEquals(setOf("epic:e1", "epic:e2"), kept.convo("pane:laptop:%1").hung.map { it.key }.toSet())
    }

    @Test
    fun searchKeepsAMatchingConversationBehindTheEndedLine() {
        val projects = tree(
            panes = listOf(
                headless("h1", state = "stopped", label = "tide cleanup"),
                headless("h2", state = "stopped", label = "billing"),
            ),
        )
        assertEquals(setOf("session:h1"), keysOf(search(projects, "tide").single().ended))
    }

    @Test
    fun searchFindsNothingWhenNothingMatches() {
        val projects = tree(panes = listOf(pane("%1", "o1", label = "notes")))
        assertEquals(emptyList<Project>(), search(projects, "zzz"))
    }
}
