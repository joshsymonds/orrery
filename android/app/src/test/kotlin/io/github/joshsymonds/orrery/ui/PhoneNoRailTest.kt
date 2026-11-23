package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The phone's inbox draws every design in full at every width: each design's tab and well, and every kid row under the
// well, with no collapsed rail or rail tab (round 8 draws the rail only on the web, and the phone is unchanged). Drawn from
// invented rows at a fixed clock, and read through the semantics tree alone.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PhoneNoRailTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val DIR = "/home/user/alpha"
        const val REPOSITORY = "synthetic-owner"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val DESIGNS_LIST = "/orrery.v1.Designs/List"
        const val SESSION_A = "synthetic-session-a"
        const val SESSION_B = "synthetic-session-b"
        const val DESIGN_A = "synthetic-design-a"
        const val DESIGN_B = "synthetic-design-b"
        const val TITLE_A = "Synthetic design A"
        const val TITLE_B = "Synthetic design B"
        const val CONTRACT_A = "synthetic-owner/contract-a"
        const val CONTRACT_B = "synthetic-owner/contract-b"
        const val CONTRACT_EPIC = "synthetic-owner/contract-epic"
        const val EPIC_ID = "synthetic-owner/epic-b"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun headless(id: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir(DIR)
        .setLabel("synthetic-working")
        .setState("working")
        .setChanged(NOW - 60)
        .setSeen(NOW - 60)
        .setSummary("Synthetic working session.")
        .setNewestReply(NOW - 60)
        .build()

    private fun liveEpic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(REPOSITORY)
        .setSlug("epic-b")
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setProblem("")
        .setSteering(SESSION_B)
        .build()

    private fun contract(id: String, conversation: String): Contract = Contract.newBuilder()
        .setId(id)
        .setRepository(REPOSITORY)
        .setTitle("Synthetic contract")
        .setCare("serious")
        .setState("proposed")
        .setVersion(1L)
        .setOpenDecisions(0L)
        .setConversation(conversation)
        .setCreated(NOW - 600)
        .setChanged(NOW - 600)
        .build()

    private fun designContract(id: String, position: Long, state: String, epic: String = ""): DesignContract =
        DesignContract.newBuilder()
            .setId(id)
            .setTitle("Synthetic contract")
            .setRepository(REPOSITORY)
            .setState(state)
            .setPosition(position)
            .setEpic(epic)
            .build()

    private fun design(id: String, title: String, conversation: String, vararg contracts: DesignContract): Design =
        Design.newBuilder()
            .setId(id)
            .setTitle(title)
            .setConversation(conversation)
            .setCreated(NOW - 600)
            .setOfOne(false)
            .setState("working")
            .addAllContracts(contracts.toList())
            .build()

    /**
     * Two headless conversations in one project, each owning one design. Design A holds one contract. Design B holds
     * two: the first has the epic, the second is a plain contract, so B's well holds both its epic and its contract row.
     */
    private fun serveTwoDesigns(server: FakeOrreryServer) {
        server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
        server.respond(
            LIST_HEADLESS,
            ListHeadlessResponse.newBuilder()
                .addSessions(headless(SESSION_A))
                .addSessions(headless(SESSION_B))
                .build(),
        )
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        server.respond(
            CONTRACTS_LIST,
            ContractsListResponse.newBuilder()
                .addContracts(contract(CONTRACT_A, SESSION_A))
                .addContracts(contract(CONTRACT_B, SESSION_B))
                .build(),
        )
        server.respond(
            DESIGNS_LIST,
            DesignsListResponse.newBuilder()
                .addDesigns(design(DESIGN_A, TITLE_A, SESSION_A, designContract(CONTRACT_A, 1L, "proposed")))
                .addDesigns(
                    design(
                        DESIGN_B,
                        TITLE_B,
                        SESSION_B,
                        designContract(CONTRACT_EPIC, 1L, "accepted", epic = EPIC_ID),
                        designContract(CONTRACT_B, 2L, "proposed"),
                    ),
                )
                .build(),
        )
    }

    /** The whole phone inbox, at the width the test's qualifiers set, at the fixed clock [NOW]. */
    private fun setScreen(server: FakeOrreryServer) {
        // Made once, outside the composition, so a recomposition does not hand the screen a new client.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
                onOpenPane = {},
                onOpenPaneId = {},
                onStartedSession = {},
                onOpenSettings = {},
                now = { NOW },
            )
        }
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** The design's tab is drawn with its title, and its well with each of [kids] under it. */
    private fun assertDesignDrawnInFull(designId: String, title: String, kids: List<String>) {
        composeRule.onNodeWithTag("design-tab:$designId", useUnmergedTree = true).assertExists()
        composeRule.onNode(
            hasText(title) and hasAnyAncestor(hasTestTag("design-tab:$designId")),
            useUnmergedTree = true,
        ).assertExists()
        composeRule.onNodeWithTag("design-well:$designId", useUnmergedTree = true).assertExists()
        for (key in kids) {
            composeRule.onNode(
                hasTestTag("hung:$key") and hasAnyAncestor(hasTestTag("design-well:$designId")),
                useUnmergedTree = true,
            ).assertExists()
        }
    }

    /** A node whose test tag names a rail: the rail tab, or an inbox or collapsed rail. */
    private fun railTag(): SemanticsMatcher = SemanticsMatcher("a test tag naming a rail") { node ->
        val tag = node.config.getOrNull(SemanticsProperties.TestTag)
        tag != null &&
            ("RailTab" in tag || "rail-tab" in tag || tag.startsWith("inbox-rail") || tag.startsWith("collapsed-rail"))
    }

    /** Draws the two designs at the test's width, then holds each one in full and no rail anywhere in the tree. */
    private fun assertInboxDrawnInFullWithNoRail(server: FakeOrreryServer) {
        serveTwoDesigns(server)
        setScreen(server)
        awaitNode(hasTestTag("design-well:$DESIGN_B"))

        assertDesignDrawnInFull(DESIGN_A, TITLE_A, listOf("contract:$CONTRACT_A"))
        assertDesignDrawnInFull(DESIGN_B, TITLE_B, listOf("contract:$CONTRACT_B", "epic:$EPIC_ID"))
        composeRule.onAllNodes(railTag(), useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    @Config(sdk = [35], qualifiers = "w320dp-h640dp")
    fun phoneNoRailAtW320dpDrawsEveryDesignInFull() {
        FakeOrreryServer().use { server -> assertInboxDrawnInFullWithNoRail(server) }
    }

    @Test
    @Config(sdk = [35], qualifiers = "w411dp-h891dp")
    fun phoneNoRailAtW411dpDrawsEveryDesignInFull() {
        FakeOrreryServer().use { server -> assertInboxDrawnInFullWithNoRail(server) }
    }

    @Test
    @Config(sdk = [35], qualifiers = "w600dp-h960dp")
    fun phoneNoRailAtW600dpDrawsEveryDesignInFull() {
        FakeOrreryServer().use { server -> assertInboxDrawnInFullWithNoRail(server) }
    }

    @Test
    @Config(sdk = [35], qualifiers = "w891dp-h411dp-land")
    fun phoneNoRailAtW891dpLandscapeDrawsEveryDesignInFull() {
        FakeOrreryServer().use { server -> assertInboxDrawnInFullWithNoRail(server) }
    }

    @Test
    @Config(sdk = [35], qualifiers = "w1280dp-h800dp")
    fun phoneNoRailAtW1280dpDrawsEveryDesignInFull() {
        FakeOrreryServer().use { server -> assertInboxDrawnInFullWithNoRail(server) }
    }
}
