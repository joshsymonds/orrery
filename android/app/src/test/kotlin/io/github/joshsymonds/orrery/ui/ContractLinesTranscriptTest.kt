package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.time.ZoneId
import java.util.TimeZone

// Unix seconds: 2023-11-14 22:13:20 UTC, which reads 22:13 in UTC.
private const val ACCEPTED_TIME = 1_700_000_000L
private const val CONTRACT_ID = "tidepool/tide-alerts"
private const val TITLE = "Synthetic contract title"
private const val FAILED_OUTPUT = "synthetic failure"
private const val OK_OUTPUT = "synthetic output"

private val UTC_ZONE: ZoneId = ZoneId.of("UTC")

private fun item(
    kind: String,
    id: String,
    tool: String = "Write",
    error: Boolean = false,
    contract: String = "",
) = ConversationItem(
    kind = kind,
    time = 0L,
    text = "",
    tool = tool,
    input = "synthetic input",
    id = id,
    error = error,
    contract = contract,
)

private fun result(id: String, text: String, error: Boolean = false) = ConversationItem(
    kind = "result",
    time = 0L,
    text = text,
    tool = "",
    input = "",
    id = id,
    error = error,
)

private fun plain(id: String) = item("tool", id)

private fun contractRow(state: String, care: String = "serious", changed: Long = 0L) = ContractRow(
    id = CONTRACT_ID,
    repository = "synthetic-repository",
    title = TITLE,
    care = care,
    state = state,
    version = 1L,
    openDecisions = 0L,
    conversation = "synthetic-session",
    created = 0L,
    changed = changed,
)

private fun toolCall(id: String, contract: String = "", result: String? = null, error: Boolean = false) =
    ToolCall(id, "Write", "synthetic input", result, error, contract = contract)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractLinesTranscriptTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun aContractCallBetweenOtherCallsIsNotFoldedIntoAGroup() {
        val entries = foldConversation(
            listOf(
                plain("a"),
                plain("b"),
                item("tool", "k", contract = CONTRACT_ID),
                result("k", OK_OUTPUT),
                plain("c"),
                plain("d"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Tool(toolCall("a")),
                ConvoEntry.Tool(toolCall("b")),
                ConvoEntry.ContractLine(toolCall("k", contract = CONTRACT_ID, result = OK_OUTPUT)),
                ConvoEntry.Tool(toolCall("c")),
                ConvoEntry.Tool(toolCall("d")),
            ),
            entries,
        )
    }

    @Test
    fun aFailedContractCallFoldsLikeAnyOtherCall() {
        val entries = foldConversation(
            listOf(
                plain("a"),
                plain("b"),
                item("tool", "k", contract = CONTRACT_ID),
                result("k", FAILED_OUTPUT, error = true),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Group(
                    listOf(
                        toolCall("a"),
                        toolCall("b"),
                        toolCall("k", contract = CONTRACT_ID, result = FAILED_OUTPUT, error = true),
                    ),
                ),
            ),
            entries,
        )
    }

    @Test
    fun aPendingContractCallFoldsLikeAnyOtherCall() {
        val entries = foldConversation(
            listOf(
                plain("a"),
                item("tool", "k", contract = CONTRACT_ID),
                plain("b"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Group(
                    listOf(
                        toolCall("a"),
                        toolCall("k", contract = CONTRACT_ID),
                        toolCall("b"),
                    ),
                ),
            ),
            entries,
        )
    }

    @Test
    fun proposedContractWaitsForYou() {
        assertEquals(
            ContractLineWords(ContractLineTone.Attention, TITLE, " · serious care · waits for you"),
            contractLineWords(CONTRACT_ID, contractRow("proposed"), UTC_ZONE),
        )
    }

    @Test
    fun acceptedContractShowsItsClockAndCare() {
        assertEquals(
            ContractLineWords(ContractLineTone.Ok, TITLE, " · accepted 22:13 · serious care"),
            contractLineWords(CONTRACT_ID, contractRow("accepted", changed = ACCEPTED_TIME), UTC_ZONE),
        )
    }

    @Test
    fun acceptedContractWithNoTimeReadsNoClock() {
        assertEquals(
            ContractLineWords(ContractLineTone.Ok, TITLE, " · accepted  · serious care"),
            contractLineWords(CONTRACT_ID, contractRow("accepted", changed = 0L), UTC_ZONE),
        )
    }

    @Test
    fun setAsideContractReadsSetAside() {
        assertEquals(
            ContractLineWords(ContractLineTone.Quiet, TITLE, " · set aside"),
            contractLineWords(CONTRACT_ID, contractRow("set_aside"), UTC_ZONE),
        )
    }

    @Test
    fun contractTheListDoesNotHoldDrawsItsIdAlone() {
        assertEquals(
            ContractLineWords(ContractLineTone.Quiet, CONTRACT_ID, ""),
            contractLineWords(CONTRACT_ID, null, UTC_ZONE),
        )
    }

    private fun setTranscript(
        items: List<ConversationItem>,
        contracts: List<ContractRow> = emptyList(),
        onOpen: (String) -> Unit = {},
    ) {
        val entries = foldConversation(items)
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = entries, contracts = contracts, onOpenContract = onOpen)
            }
        }
    }

    @Test
    fun aProposedContractLineDrawsItsWaitingWordsAndAPressOpensIt() {
        val opened = mutableListOf<String>()
        setTranscript(
            listOf(item("tool", "call-1", contract = CONTRACT_ID), result("call-1", OK_OUTPUT)),
            contracts = listOf(contractRow("proposed")),
            onOpen = { opened += it },
        )

        composeRule.onNodeWithText("$TITLE · serious care · waits for you").assertIsDisplayed()
        composeRule.onNodeWithTag("contract-line:$CONTRACT_ID").performClick()

        assertEquals(listOf(CONTRACT_ID), opened)
    }

    @Test
    fun anAcceptedContractLineDrawsItsAcceptedWordsWithTheClock() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        setTranscript(
            listOf(item("tool", "call-1", contract = CONTRACT_ID), result("call-1", OK_OUTPUT)),
            contracts = listOf(contractRow("accepted", changed = ACCEPTED_TIME)),
        )

        composeRule.onNodeWithText("$TITLE · accepted 22:13 · serious care").assertIsDisplayed()
    }

    @Test
    fun anUnheldContractLineDrawsItsIdAlone() {
        setTranscript(listOf(item("tool", "call-1", contract = CONTRACT_ID), result("call-1", OK_OUTPUT)))

        composeRule.onNodeWithText(CONTRACT_ID).assertIsDisplayed()
    }

    @Test
    fun aFailedContractCallDrawsItsToolRowAndNoContractLine() {
        setTranscript(
            listOf(
                item("tool", "call-1", contract = CONTRACT_ID),
                result("call-1", FAILED_OUTPUT, error = true),
            ),
            contracts = listOf(contractRow("proposed")),
        )

        composeRule.onNodeWithTag("line:tool").assertIsDisplayed()
        composeRule.onAllNodesWithTag("contract-line:$CONTRACT_ID").assertCountEquals(0)
    }
}
