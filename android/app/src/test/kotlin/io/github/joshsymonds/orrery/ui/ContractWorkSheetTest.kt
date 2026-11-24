package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementTask
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementWork
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// An accepted contract's page on the round 8 phone sheet: the contract's link opens the sheet on its work, each requirement
// with its state and tasks from Contracts.Get and its red and green proof from the epic's runs in Epics.Get. Read through
// the semantics tree alone, with the records invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ContractWorkSheetTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val CONTRACT_NAME = "synthetic-contract"
        const val EPIC_SLUG = "synthetic-epic"
        const val REQUIREMENT_ID = "R1"
        const val TASK_ID = "T1"
        const val TASK_TITLE = "Synthetic task title"
        const val TASK_STATUS = "running"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val DESIGN_CONTRACT_TAG = "design-contract:$CONTRACT_ID"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val DESIGN_GET_PATH = "/orrery.v1.Designs/Get"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
        const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
        }
    }

    /** Streams the headless session [SESSION_ID], which has nothing running. */
    private fun serveSession(server: FakeOrreryServer) {
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/lighthouse")
            .setLabel("synthetic session")
            .setState("working")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(session)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(state)
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    /**
     * One design the conversation works, whose one contract is accepted and made the epic [CONTRACT_ID]. Designs.List and
     * Designs.Get both serve it, so the page draws the contract row that opens the contract.
     */
    private fun serveAcceptedDesign(server: FakeOrreryServer) {
        val contract = DesignContract.newBuilder()
            .setId(CONTRACT_ID)
            .setTitle("Synthetic contract")
            .setRepository("synthetic-owner")
            .setState("accepted")
            .setPosition(1L)
            .setEpic(CONTRACT_ID)
            .build()
        val design = Design.newBuilder()
            .setId(DESIGN_ID)
            .setTitle(DESIGN_TITLE)
            .setConversation(SESSION_ID)
            .setCreated(NOW - 600)
            .setOfOne(false)
            .setState("1 of 1 accepted")
            .addContracts(contract)
            .build()
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addDesigns(design).build())
        server.respond(DESIGN_GET_PATH, DesignsGetResponse.newBuilder().setDesign(design).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    /** The accepted contract, its one version, and its stage's work: [REQUIREMENT_ID] being worked by [TASK_ID]. */
    private fun serveContract(server: FakeOrreryServer) {
        val record = ContractsGetResponse.newBuilder()
            .setContract(
                Contract.newBuilder()
                    .setId(CONTRACT_ID)
                    .setTitle("Synthetic contract title")
                    .setCare("limited")
                    .setState("accepted")
                    .setVersion(1L)
                    .setConversation(SESSION_ID)
                    .build(),
            )
            .addVersions(
                ContractVersion.newBuilder()
                    .setVersion(1L)
                    .setContract(
                        EpicContract.newBuilder()
                            .setTitle("Synthetic contract title")
                            .setIntent("Synthetic intent text.")
                            .setCare("limited")
                            .setApproach("Synthetic approach text.")
                            .setChecks("Synthetic checks text.")
                            .build(),
                    )
                    .addRequirements(requirement())
                    .setWriter("synthetic writer")
                    .setTime(NOW - 300)
                    .build(),
            )
            .addWork(
                ContractRequirementWork.newBuilder()
                    .setId(REQUIREMENT_ID)
                    .setText("Synthetic requirement text")
                    .setState("being worked")
                    .addTasks(
                        ContractRequirementTask.newBuilder()
                            .setId(TASK_ID)
                            .setTitle(TASK_TITLE)
                            .setStatus(TASK_STATUS)
                            .setPlace("1")
                            .build(),
                    )
                    .build(),
            )
            .build()
        server.respond(CONTRACT_GET_PATH, record)
    }

    /** The epic the contract made, whose requirement has a red run that passed and a green run that passed. */
    private fun serveEpic(server: FakeOrreryServer) {
        val epic = Epic.newBuilder()
            .setId(CONTRACT_ID)
            .setRepository("/home/user/lighthouse")
            .setSlug(EPIC_SLUG)
            .setSource("orreryd")
            .setOutcome("accepted")
            .setChanged(NOW - 30)
            .setLive(false)
            .setEndedSteps(0)
            .build()
        val requirement = requirement()
            .toBuilder()
            .addRuns(evidenceRun("red"))
            .addRuns(evidenceRun("green"))
            .build()
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder().setEpic(epic).setRepository("/home/user/lighthouse").addRequirements(requirement).build(),
        )
    }

    private fun requirement(): EpicRequirement = EpicRequirement.newBuilder()
        .setId(REQUIREMENT_ID)
        .setText("Synthetic requirement text")
        .setEvidence("Synthetic requirement evidence")
        .setKind("check")
        .build()

    /** An ended run of [phase] that passed, so its proof reads as the phase's result. */
    private fun evidenceRun(phase: String): EvidenceRun = EvidenceRun.newBuilder()
        .setId("run-$phase")
        .setPhase(phase)
        .setSource("captured")
        .setState("ended")
        .setPassed(true)
        .setCreated(NOW - 200)
        .setEnded(NOW - 199)
        .build()

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    @Test
    fun anAcceptedContractsLinkOpensOnItsWorkWithEachRequirementsStateTasksAndProof() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveAcceptedDesign(server)
            serveContract(server)
            serveEpic(server)
            setSession(server)
            awaitTag(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
            awaitTag(DESIGN_CONTRACT_TAG)

            composeRule.onNodeWithTag(DESIGN_CONTRACT_TAG, useUnmergedTree = true).performScrollTo().performClick()

            awaitTag(CONTRACT_WORK_TAG)
            composeRule.onNodeWithTag("work-state:$REQUIREMENT_ID", useUnmergedTree = true).assertTextEquals("being worked")
            composeRule.onNode(
                hasTestTag("work-task:$REQUIREMENT_ID:$TASK_ID") and hasAnyDescendant(hasText(TASK_TITLE)),
                useUnmergedTree = true,
            ).assertExists()
            composeRule.onNode(
                hasTestTag("work-task:$REQUIREMENT_ID:$TASK_ID") and hasAnyDescendant(hasText(TASK_STATUS)),
                useUnmergedTree = true,
            ).assertExists()
            composeRule.onNodeWithTag("work-red:$REQUIREMENT_ID", useUnmergedTree = true).assertTextEquals("fails at base")
            composeRule.onNodeWithTag("work-green:$REQUIREMENT_ID", useUnmergedTree = true).assertTextEquals("passes")
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(CONTRACT_NAME)
        }
    }
}
