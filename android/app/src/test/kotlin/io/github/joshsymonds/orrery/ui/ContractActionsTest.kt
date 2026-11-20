package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.MainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractDecision
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractStateChange
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsAcceptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsReopenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsSetAsideRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsSetAsideResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicReference

/** The id of the contract every record test reads. Its epic has the same id. */
internal const val RECORD_ID = "synthetic-owner/synthetic-contract"

private const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
private const val ACCEPT_PATH = "/orrery.v1.Contracts/Accept"
private const val SET_ASIDE_PATH = "/orrery.v1.Contracts/SetAside"
private const val REOPEN_PATH = "/orrery.v1.Contracts/Reopen"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val SET_ASIDE_WORDS = "Synthetic set aside words"
private const val NO_LIVE_LINE = "Its conversation has ended: you can accept it or set it aside, but not change it."

private val REQUIREMENT_PROTO: EpicRequirement = EpicRequirement.newBuilder()
    .setId("R1")
    .setText("Synthetic requirement text")
    .setEvidence("Synthetic requirement evidence")
    .setKind("check")
    .build()

/** One version of the record's contract, at [care], written by [writer] at [time] with [words] and what it changed. */
internal fun contractVersionProto(
    version: Long,
    care: String = "limited",
    time: Long = ACCEPTED,
    writer: String = "synthetic writer",
    words: String = "Synthetic version words",
    decisions: List<ContractDecision> = emptyList(),
    changedRows: List<String> = emptyList(),
): ContractVersion = ContractVersion.newBuilder()
    .setVersion(version)
    .setContract(epicContractProto(care))
    .addRequirements(REQUIREMENT_PROTO)
    .addAllDecisions(decisions)
    .setWords(words)
    .setWriter(writer)
    .setTime(time)
    .addAllChangedRows(changedRows)
    .build()

/** A state change of the record: [state] at [version], made by [by] with [words] at [ACCEPTED]. */
internal fun contractStateProto(state: String, version: Long, by: String = "owner", words: String = ""): ContractStateChange =
    ContractStateChange.newBuilder()
        .setState(state)
        .setVersion(version)
        .setBy(by)
        .setWords(words)
        .setTime(ACCEPTED)
        .build()

/**
 * The record Contracts.Get answers for [RECORD_ID]: its contract at [state], at the care of its newest version, with its
 * [versions] oldest first and its [states].
 */
internal fun contractRecordProto(
    state: String,
    versions: List<ContractVersion>,
    states: List<ContractStateChange> = emptyList(),
): ContractsGetResponse {
    val newest = versions.last()
    return ContractsGetResponse.newBuilder()
        .setContract(
            Contract.newBuilder()
                .setId(RECORD_ID)
                .setTitle("Synthetic contract title")
                .setCare(newest.contract.care)
                .setState(state)
                .setVersion(newest.version)
                .setConversation("synthetic-session")
                .build(),
        )
        .addAllVersions(versions)
        .addAllStates(states)
        .build()
}

/** An open decision on the record's rows, as the newest version asks it. */
internal fun openDecisionProto(): ContractDecision = ContractDecision.newBuilder()
    .setId("D1")
    .setQuestion("Synthetic decision question")
    .addOptions("Synthetic option")
    .setRecommendation("Synthetic recommendation")
    .setReason("Synthetic decision reason")
    .setRow("F1")
    .setAnswer("")
    .build()

/** Draws the contract screen for [RECORD_ID], live unless [live] is false, with [writer] naming each writer. */
internal fun MainThreadAndroidComposeRule<ComponentActivity>.setContractScreen(
    server: FakeOrreryServer,
    live: Boolean = true,
    writer: (String) -> String = { it },
    onTalk: (() -> Unit)? = null,
) {
    val client = OrreryClient(server.baseUrl)
    setContent {
        MaterialTheme {
            ContractScreen(
                client = client,
                epicId = RECORD_ID,
                onBack = {},
                onHome = {},
                live = live,
                writer = writer,
                onAbout = {},
                onTalk = onTalk,
            )
        }
    }
}

/** Scrolls the contract body until the row or section tagged [tag] is in view. */
internal fun MainThreadAndroidComposeRule<ComponentActivity>.scrollToRow(tag: String) {
    onNodeWithTag("contract-body").performScrollToNode(hasTestTag(tag))
}

/** Answers Contracts.Get with whatever [record] holds, so a write that moves the record shows on the next read. */
internal fun FakeOrreryServer.serveContractRecord(record: AtomicReference<ContractsGetResponse>) {
    route(CONTRACT_GET_PATH) { Reply.Message(record.get(), 0L) }
}

private fun epicContractProto(care: String): EpicContract = EpicContract.newBuilder()
    .setTitle("Synthetic contract title")
    .setApproved("Synthetic approved line")
    .setIntent("Synthetic intent text.")
    .addFailures(
        EpicFailure.newBuilder()
            .setId("F1")
            .setText("Synthetic failure text")
            .setRating(care)
            .setResponse("Synthetic failure response")
            .build(),
    )
    .setCare(care)
    .addPremises(
        EpicPremise.newBuilder()
            .setId("P1")
            .setText("Synthetic premise text")
            .setIfWrong("Synthetic premise if wrong")
            .build(),
    )
    .setApproach("Synthetic approach text.")
    .setReleaseThen("Synthetic release then text.")
    .setChecks("Synthetic checks text.")
    .build()

private fun proposedRecord(
    care: String = "limited",
    decisions: List<ContractDecision> = emptyList(),
): ContractsGetResponse = contractRecordProto("proposed", listOf(contractVersionProto(1, care = care, decisions = decisions)))

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractActionsTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private var originalZone: TimeZone? = null

    @Before
    fun useUtc() {
        originalZone = TimeZone.getDefault()
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun acceptIsDisabledWhileADecisionIsOpen() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord(decisions = listOf(openDecisionProto()))))
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))
            composeRule.onNodeWithTag("contract-accept").assertIsNotEnabled()
        }
    }

    @Test
    fun acceptIsDisabledAtSevereCareUntilTheIrreversibleSectionIsShown() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord(care = "severe")))
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))
            composeRule.onNodeWithTag("contract-accept").assertIsNotEnabled()
            composeRule.scrollToRow("contract-section:7")
            composeRule.onNodeWithTag("contract-accept").assertIsEnabled()
        }
    }

    @Test
    fun acceptIsDisabledOnAnOlderVersion() {
        FakeOrreryServer().use { server ->
            val versions = listOf(contractVersionProto(1), contractVersionProto(2, time = ACCEPTED + 60))
            server.serveContractRecord(AtomicReference(contractRecordProto("proposed", versions)))
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-versions-toggle"))
            composeRule.onNodeWithTag("contract-versions-toggle").performClick()
            composeRule.scrollToRow("contract-version:1")
            composeRule.onNodeWithTag("contract-version:1").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-older"))
            composeRule.onNodeWithTag("contract-accept").assertIsNotEnabled()
        }
    }

    @Test
    fun pressingAcceptRereadsThenSendsOneAccept() {
        FakeOrreryServer().use { server ->
            val record = AtomicReference(proposedRecord())
            server.serveContractRecord(record)
            server.route(ACCEPT_PATH) {
                val accepted = contractRecordProto("accepted", record.get().versionsList, listOf(contractStateProto("accepted", 1)))
                record.set(accepted)
                Reply.Message(ContractsAcceptResponse.newBuilder().setContract(accepted.contract).build(), 0L)
            }
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))
            composeRule.onNodeWithTag("contract-accept").performClick()
            composeRule.awaitNone(hasTestTag("contract-accept"))

            assertEquals(
                listOf(CONTRACT_GET_PATH, CONTRACT_GET_PATH, ACCEPT_PATH, CONTRACT_GET_PATH, EPICS_GET_PATH),
                server.requests.map { it.rawPath },
            )
        }
    }

    @Test
    fun aPressThatFindsANewerVersionSendsNothingAndDrawsIt() {
        FakeOrreryServer().use { server ->
            val record = AtomicReference(proposedRecord())
            server.serveContractRecord(record)
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

            val versions = listOf(contractVersionProto(1), contractVersionProto(2, time = ACCEPTED + 60))
            record.set(contractRecordProto("proposed", versions))
            composeRule.onNodeWithTag("contract-accept").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-newer"))
            composeRule.onNodeWithTag("contract-newer")
                .assertTextEquals("A newer version came in. Read it before you accept.")
            composeRule.awaitUntil("the newer version to be drawn") {
                composeRule.onAllNodesWithText("v2 proposed", substring = true).fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("contract-accept").assertIsEnabled()
            assertEquals(0, server.requests.count { it.rawPath == ACCEPT_PATH })
        }
    }

    @Test
    fun setAsideSendsOneSetAsideWithItsWords() {
        FakeOrreryServer().use { server ->
            val record = AtomicReference(proposedRecord())
            server.serveContractRecord(record)
            server.route(SET_ASIDE_PATH) {
                val setAside = contractRecordProto(
                    "set_aside",
                    record.get().versionsList,
                    listOf(contractStateProto("set_aside", 1, words = SET_ASIDE_WORDS)),
                )
                record.set(setAside)
                Reply.Message(ContractsSetAsideResponse.newBuilder().setContract(setAside.contract).build(), 0L)
            }
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-set-aside"))
            composeRule.onNodeWithTag("contract-set-aside").performClick()
            composeRule.onNodeWithTag("contract-set-aside-words").performTextInput(SET_ASIDE_WORDS)
            composeRule.onNodeWithTag("contract-set-aside-confirm").performClick()

            composeRule.awaitUntil("the set aside to be drawn") {
                composeRule.onAllNodesWithText("v1 set aside by you", substring = true).fetchSemanticsNodes().isNotEmpty()
            }
            val sent = server.requests.filter { it.rawPath == SET_ASIDE_PATH }
            assertEquals(1, sent.size)
            assertEquals(SET_ASIDE_WORDS, ContractsSetAsideRequest.parseFrom(sent.single().body).words)
        }
    }

    @Test
    fun reopenSendsOneReopen() {
        FakeOrreryServer().use { server ->
            val setAside = contractRecordProto("set_aside", listOf(contractVersionProto(1)), listOf(contractStateProto("set_aside", 1, words = SET_ASIDE_WORDS)))
            val record = AtomicReference(setAside)
            server.serveContractRecord(record)
            server.route(REOPEN_PATH) {
                val reopened = contractRecordProto("proposed", record.get().versionsList)
                record.set(reopened)
                Reply.Message(ContractsReopenResponse.newBuilder().setContract(reopened.contract).build(), 0L)
            }
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-reopen"))
            composeRule.onNodeWithTag("contract-reopen").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))
            assertEquals(1, server.requests.count { it.rawPath == REOPEN_PATH })
        }
    }

    @Test
    fun actionsAreDisabledWhileAnAcceptIsPending() {
        val release = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val record = AtomicReference(proposedRecord())
                server.serveContractRecord(record)
                server.route(ACCEPT_PATH) {
                    release.await()
                    val accepted = contractRecordProto("accepted", record.get().versionsList, listOf(contractStateProto("accepted", 1)))
                    record.set(accepted)
                    Reply.Message(ContractsAcceptResponse.newBuilder().setContract(accepted.contract).build(), 0L)
                }
                composeRule.setContractScreen(server)
                composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

                composeRule.onNodeWithTag("contract-accept").performClick()
                composeRule.awaitUntil("the accept to reach the server") {
                    server.requests.any { it.rawPath == ACCEPT_PATH }
                }

                composeRule.onNodeWithTag("contract-accept").assertIsNotEnabled()
                composeRule.onNodeWithTag("contract-set-aside").assertIsNotEnabled()
                release.countDown()
                composeRule.awaitNone(hasTestTag("contract-accept"))
            }
        } finally {
            release.countDown()
        }
    }

    @Test
    fun aRefusedAcceptShowsItsReason() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            server.fail(ACCEPT_PATH, Code.FAILED_PRECONDITION, "Synthetic refusal reason")
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

            composeRule.onNodeWithTag("contract-accept").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-refusal"))
            composeRule.onNodeWithTag("contract-refusal").assertTextContains("Synthetic refusal reason")
            composeRule.onNodeWithTag("contract-accept").assertIsEnabled()
        }
    }

    @Test
    fun anAcceptedContractSaysWhoAcceptedItAndWhen() {
        FakeOrreryServer().use { server ->
            val accepted = contractRecordProto("accepted", listOf(contractVersionProto(1)), listOf(contractStateProto("accepted", 1)))
            server.serveContractRecord(AtomicReference(accepted))
            composeRule.setContractScreen(server)

            composeRule.awaitUntil("the accepted line to be drawn") {
                composeRule.onAllNodesWithText("v1 accepted by you 22:13").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.awaitNone(hasTestTag("contract-accept"))
        }
    }

    @Test
    fun theNoLiveLineShowsAndRowControlsAreOffWhenTheConversationEnded() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            composeRule.setContractScreen(server, live = false)

            composeRule.awaitExactlyOne(hasTestTag("contract-no-live"))
            composeRule.onNodeWithTag("contract-no-live").assertTextEquals(NO_LIVE_LINE)
            assertEquals(0, composeRule.onAllNodesWithTag("contract-talk:F1").fetchSemanticsNodes().size)
        }
    }

    @Test
    fun talkAboutItCallsOnTalk() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            var talks = 0
            composeRule.setContractScreen(server, onTalk = { talks++ })

            composeRule.awaitExactlyOne(hasTestTag("contract-talk-about"))
            composeRule.onNodeWithTag("contract-talk-about").performClick()

            assertEquals(1, talks)
        }
    }

    @Test
    fun theRecordIsOneTaggedRoot() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-record"))
            composeRule.onNodeWithTag("contract-record").assertIsDisplayed()
        }
    }

    @Test
    fun aSecondPressBeforeTheFirstIsDrawnSendsNothing() {
        val release = CountDownLatch(1)
        try {
            FakeOrreryServer().use { server ->
                val record = AtomicReference(proposedRecord())
                server.serveContractRecord(record)
                server.route(ACCEPT_PATH) {
                    release.await()
                    val accepted = contractRecordProto("accepted", record.get().versionsList, listOf(contractStateProto("accepted", 1)))
                    record.set(accepted)
                    Reply.Message(ContractsAcceptResponse.newBuilder().setContract(accepted.contract).build(), 0L)
                }
                composeRule.setContractScreen(server)
                composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

                val press = composeRule.onNodeWithTag("contract-accept").fetchSemanticsNode().config[SemanticsActions.OnClick].action
                press?.invoke()
                press?.invoke()
                composeRule.awaitUntil("the accept to reach the server") {
                    server.requests.any { it.rawPath == ACCEPT_PATH }
                }
                release.countDown()
                composeRule.awaitNone(hasTestTag("contract-accept"))

                assertEquals(1, server.requests.count { it.rawPath == ACCEPT_PATH })
            }
        } finally {
            release.countDown()
        }
    }

    @Test
    fun aRefusedSetAsideKeepsItsWordsAndShowsItsReason() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            server.fail(SET_ASIDE_PATH, Code.FAILED_PRECONDITION, "Synthetic refusal reason")
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-set-aside"))

            composeRule.onNodeWithTag("contract-set-aside").performClick()
            composeRule.onNodeWithTag("contract-set-aside-words").performTextInput(SET_ASIDE_WORDS)
            composeRule.onNodeWithTag("contract-set-aside-confirm").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-refusal"))
            composeRule.onNodeWithTag("contract-refusal").assertTextContains("Synthetic refusal reason")
            composeRule.onNodeWithTag("contract-set-aside-words").assertTextContains(SET_ASIDE_WORDS)
            composeRule.onNodeWithTag("contract-set-aside-confirm").assertIsEnabled()
            composeRule.onNodeWithTag("contract-accept").assertIsEnabled()
            assertEquals(1, server.requests.count { it.rawPath == SET_ASIDE_PATH })
        }
    }

    @Test
    fun aRefusedReopenShowsItsReason() {
        FakeOrreryServer().use { server ->
            val setAside = contractRecordProto(
                "set_aside",
                listOf(contractVersionProto(1)),
                listOf(contractStateProto("set_aside", 1, words = SET_ASIDE_WORDS)),
            )
            server.serveContractRecord(AtomicReference(setAside))
            server.fail(REOPEN_PATH, Code.FAILED_PRECONDITION, "Synthetic refusal reason")
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-reopen"))

            composeRule.onNodeWithTag("contract-reopen").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-refusal"))
            composeRule.onNodeWithTag("contract-refusal").assertTextContains("Synthetic refusal reason")
            composeRule.onNodeWithTag("contract-reopen").assertIsEnabled()
            assertEquals(1, server.requests.count { it.rawPath == REOPEN_PATH })
        }
    }

    @Test
    fun aFailedAcceptShowsTheServersMessage() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(proposedRecord()))
            server.fail(ACCEPT_PATH, Code.INTERNAL_ERROR, "Synthetic server message")
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

            composeRule.onNodeWithTag("contract-accept").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-refusal"))
            composeRule.onNodeWithTag("contract-refusal").assertTextContains("Synthetic server message")
        }
    }

    @Test
    fun aPressThatFindsTheContractGoneShowsContractUnavailable() {
        FakeOrreryServer().use { server ->
            val record = AtomicReference<ContractsGetResponse?>(proposedRecord())
            server.route(CONTRACT_GET_PATH) { record.get()?.let { Reply.Message(it, 0L) } ?: Reply.Failure(Code.NOT_FOUND, "", 0L) }
            composeRule.setContractScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("contract-accept"))

            record.set(null)
            composeRule.onNodeWithTag("contract-accept").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-refusal"))
            composeRule.onNodeWithTag("contract-refusal").assertTextEquals("contract unavailable")
            assertEquals(0, server.requests.count { it.rawPath == ACCEPT_PATH })
        }
    }

    @Test
    fun setAsideIsOfferedOnAnAcceptedContractWithNoEpic() {
        FakeOrreryServer().use { server ->
            val accepted = contractRecordProto("accepted", listOf(contractVersionProto(1)), listOf(contractStateProto("accepted", 1)))
            server.serveContractRecord(AtomicReference(accepted))
            composeRule.setContractScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("contract-set-aside"))
            composeRule.onNodeWithTag("contract-set-aside").assertIsEnabled()
        }
    }

    @Test
    fun setAsideIsNotOfferedOnAnAcceptedContractWithAnEpic() {
        FakeOrreryServer().use { server ->
            val accepted = contractRecordProto("accepted", listOf(contractVersionProto(1)), listOf(contractStateProto("accepted", 1)))
            server.serveContractRecord(AtomicReference(accepted))
            server.route(EPICS_GET_PATH) {
                Reply.Message(
                    EpicsGetResponse.newBuilder().setEpic(Epic.newBuilder().setId(RECORD_ID).setSlug("synthetic-contract")).build(),
                    0L,
                )
            }
            composeRule.setContractScreen(server)

            composeRule.awaitUntil("the accepted line to be drawn") {
                composeRule.onAllNodesWithText("v1 accepted by you", substring = true).fetchSemanticsNodes().isNotEmpty()
            }
            assertEquals(0, composeRule.onAllNodesWithTag("contract-set-aside").fetchSemanticsNodes().size)
        }
    }
}
