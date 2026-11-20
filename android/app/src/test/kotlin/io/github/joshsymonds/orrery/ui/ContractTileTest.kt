package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract as ContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val SESSION = "example-session"
private const val OTHER_SESSION = "other-session"
private const val REPO = "example-repo"
private const val REPO_ID = "example-repo-1a2b3c4"
private const val CONTRACT_ID = "example-owner/example-repo-tide-alerts"
private const val CONTRACT_TAG = "tile:contract:$CONTRACT_ID"
private const val ASK_TAG = "tile-ask:contract:$CONTRACT_ID"
private const val TITLE = "example-repo-tide-alerts: a contract waits for you"
private const val OWNER = "example-owner/"
private const val NOW = 1_700_000_000L
private const val EPIC_TAG = "tile:epic-alpha"
private const val CONTRACTS_LIST_PATH = "/orrery.v1.Contracts/List"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w800dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ContractTileTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private lateinit var server: FakeOrreryServer

    private val opened = mutableListOf<RailThing>()
    private val openedContracts = mutableListOf<String>()

    @Before
    fun startServer() {
        server = FakeOrreryServer()
    }

    @After
    fun stopServer() {
        server.close()
    }

    private fun contract(
        id: String = CONTRACT_ID,
        state: String = "proposed",
        conversation: String = SESSION,
        openDecisions: Long = 0L,
    ): ContractRow = ContractRow(
        id = id,
        repository = REPO_ID,
        title = "Tide alerts",
        care = "serious",
        state = state,
        version = 1L,
        openDecisions = openDecisions,
        conversation = conversation,
        created = 0L,
        changed = 0L,
    )

    private fun epic(id: String): EpicRow = EpicRow(
        id = id,
        repository = REPO,
        slug = "$REPO-alpha-work",
        outcome = "",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = "orreryd",
        phaseAt = 0L,
        seen = 0L,
        steering = SESSION,
        archived = false,
    )

    private fun groupsOf(
        contracts: List<ContractRow>,
        session: String = SESSION,
        epics: List<EpicRow> = emptyList(),
    ): List<RailGroup> = workGroupsOf(
            session = session,
            pane = false,
            epics = epics,
            details = emptyMap(),
            background = emptyList(),
            contracts = contracts,
        )

    private fun contractThingsOf(groups: List<RailGroup>): List<ContractThing> =
        groups.filter { it.kind == GroupKind.CONTRACT }
            .flatMap { group -> group.things.filterIsInstance<ContractThing>() }

    private fun setStrip(groups: List<RailGroup>) {
        composeRule.setContent {
            TileStrip(
                groups = groups,
                onOpen = { opened.add(it) },
                onOpenPicker = {},
            )
        }
    }

    @Test
    fun theContractGroupLeadsTheStripWithTheConversationsProposedContract() {
        val groups = groupsOf(listOf(contract()), epics = listOf(epic("e1")))

        assertEquals(listOf(GroupKind.CONTRACT, GroupKind.EPIC), groups.map { it.kind })
        assertEquals("contract", groups.first().label)
        assertEquals(listOf(CONTRACT_ID), contractThingsOf(groups).map { it.id })
    }

    @Test
    fun acceptedSetAsideAndOtherConversationsContractsAreAbsent() {
        val groups = groupsOf(
            listOf(
                contract(id = "example-owner/accepted", state = "accepted"),
                contract(id = "example-owner/set-aside", state = "set_aside"),
                contract(id = "example-owner/elsewhere", conversation = OTHER_SESSION),
            ),
        )

        assertEquals(emptyList<ContractThing>(), contractThingsOf(groups))
        assertTrue(groups.none { it.kind == GroupKind.CONTRACT })
    }

    @Test
    fun anEmptySessionHasNoContractGroup() {
        val groups = groupsOf(listOf(contract(conversation = "")), session = "")

        assertTrue(groups.none { it.kind == GroupKind.CONTRACT })
    }

    @Test
    fun theCapIsTheShortNameAndTheTitleIsTheSlugWords() {
        val thing = contractThingsOf(groupsOf(listOf(contract()))).single()

        assertEquals("tide", thing.short)
        assertEquals("example-repo-tide-alerts", thing.slug)
        assertEquals(TITLE, thing.title)
    }

    @Test
    fun aTileAsksWhileADecisionIsOpen() {
        val thing = contractThingsOf(groupsOf(listOf(contract(openDecisions = 2L)))).single()

        assertTrue(thing.asks)
    }

    @Test
    fun aTileDoesNotAskWhenNoDecisionIsOpen() {
        val thing = contractThingsOf(groupsOf(listOf(contract(openDecisions = 0L)))).single()

        assertFalse(thing.asks)
    }

    @Test
    fun aTileWithAnOpenDecisionDrawsItsQuestionMark() {
        setStrip(groupsOf(listOf(contract(openDecisions = 2L))))

        composeRule.onAllNodesWithTag(ASK_TAG).assertCountEquals(1)
    }

    @Test
    fun aTileWithNoOpenDecisionDrawsNoQuestionMark() {
        setStrip(groupsOf(listOf(contract(openDecisions = 0L))))

        composeRule.onAllNodesWithTag(ASK_TAG).assertCountEquals(0)
    }

    @Test
    fun theTileShowsItsCapAndItsDescriptionWords() {
        setStrip(groupsOf(listOf(contract())))

        composeRule.onNodeWithTag(CONTRACT_TAG).assertTextEquals("tide")
        composeRule.onNodeWithTag(CONTRACT_TAG).assert(hasContentDescription(TITLE))
    }

    @Test
    fun pressingTheTileOpensTheContractWithItsId() {
        setStrip(groupsOf(listOf(contract())))

        composeRule.onNodeWithTag(CONTRACT_TAG).performClick()

        assertEquals(listOf(CONTRACT_ID), opened.filterIsInstance<ContractThing>().map { it.id })
    }

    private fun contractProto(
        id: String,
        state: String = "proposed",
        conversation: String = SESSION,
    ): ContractProto = ContractProto.newBuilder()
        .setId(id)
        .setRepository(REPO_ID)
        .setTitle("Tide alerts")
        .setCare("serious")
        .setState(state)
        .setVersion(1L)
        .setOpenDecisions(0L)
        .setConversation(conversation)
        .setCreated(0L)
        .setChanged(0L)
        .build()

    private fun serveContracts(vararg contracts: ContractProto) {
        server.respond(
            CONTRACTS_LIST_PATH,
            ContractsListResponse.newBuilder().addAllContracts(contracts.toList()).build(),
        )
    }

    // The one epic the conversation steers. The strip writes its epics and contracts together, so a test waits on this
    // tile for the contracts too.
    private fun serveSteeredEpic() {
        val epic = Epic.newBuilder()
            .setId("epic-alpha")
            .setRepository(REPO)
            .setSlug("$REPO-alpha")
            .setLive(true)
            .setSteering(SESSION)
            .build()
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addAllEpics(listOf(epic)).build())
    }

    private fun setConversationStrip() {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                ConversationStrip(
                    conversation = SESSION,
                    pane = false,
                    background = emptyList(),
                    now = NOW,
                    client = client,
                    onOpenEpic = {},
                    onOpenContract = { openedContracts.add(it) },
                )
            }
        }
    }

    private fun shown(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()

    @Test
    fun theStripLoadsTheConversationsProposedContractsBesideItsEpicsWithTheContractGroupFirst() {
        serveContracts(
            contractProto(CONTRACT_ID),
            contractProto("${OWNER}accepted", state = "accepted"),
            contractProto("${OWNER}set-aside", state = "set_aside"),
            contractProto("${OWNER}elsewhere", conversation = OTHER_SESSION),
        )
        serveSteeredEpic()
        setConversationStrip()

        composeRule.awaitUntil("the strip's epic tile") { shown(EPIC_TAG) }
        assertTrue(server.requests.any { it.rawPath == CONTRACTS_LIST_PATH })
        composeRule.onAllNodesWithTag("rail-group:contract", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onAllNodesWithTag(CONTRACT_TAG, useUnmergedTree = true).assertCountEquals(1)
        composeRule.onAllNodesWithTag("tile:contract:${OWNER}accepted", useUnmergedTree = true)
            .assertCountEquals(0)
        composeRule.onAllNodesWithTag("tile:contract:${OWNER}set-aside", useUnmergedTree = true)
            .assertCountEquals(0)
        composeRule.onAllNodesWithTag("tile:contract:${OWNER}elsewhere", useUnmergedTree = true)
            .assertCountEquals(0)
        val contractLeft = composeRule.onNodeWithTag("rail-group:contract", useUnmergedTree = true)
            .getBoundsInRoot().left
        val epicLeft = composeRule.onNodeWithTag("rail-group:epic", useUnmergedTree = true).getBoundsInRoot().left
        assertTrue(contractLeft < epicLeft)
    }

    @Test
    fun aFailedContractListGivesNoContractGroupAndTheEpicsStillShow() {
        server.fail(CONTRACTS_LIST_PATH, Code.UNKNOWN, "synthetic failure")
        serveSteeredEpic()
        setConversationStrip()

        composeRule.awaitUntil("the strip's epic tile") { shown(EPIC_TAG) }
        composeRule.onAllNodesWithTag("rail-group:contract", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun aServerWithNoContractListGivesNoContractGroupAndTheEpicsStillShow() {
        server.fail(CONTRACTS_LIST_PATH, Code.UNIMPLEMENTED)
        serveSteeredEpic()
        setConversationStrip()

        composeRule.awaitUntil("the strip's epic tile") { shown(EPIC_TAG) }
        composeRule.onAllNodesWithTag("rail-group:contract", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun pressingTheContractTileInTheStripOpensTheContractWithItsId() {
        serveContracts(contractProto(CONTRACT_ID))
        serveSteeredEpic()
        setConversationStrip()
        composeRule.awaitUntil("the contract tile") { shown(CONTRACT_TAG) }

        composeRule.onNodeWithTag(CONTRACT_TAG).performClick()

        assertEquals(listOf(CONTRACT_ID), openedContracts)
    }

    @Test
    fun aContractGroupOfMoreThanThreeOpensAPickerWhoseRowOpensTheContract() {
        val ids = listOf(
            "${OWNER}alpha",
            "${OWNER}bravo",
            "${OWNER}charlie",
            "${OWNER}delta",
        )
        serveContracts(*ids.map { contractProto(it) }.toTypedArray())
        serveSteeredEpic()
        setConversationStrip()
        composeRule.awaitUntil("the contract dots tile") { shown("tile-dots:contract") }

        composeRule.onNodeWithTag("tile-dots:contract").performClick()
        ids.forEach { id ->
            composeRule.onAllNodesWithTag("picker-row:$id", useUnmergedTree = true).assertCountEquals(1)
        }
        composeRule.onNodeWithTag("picker-row:${ids[2]}").performClick()

        assertEquals(listOf(ids[2]), openedContracts)
    }
}
