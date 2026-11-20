package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractDecision
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractStateChange
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsAcceptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsAcceptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsReopenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsReopenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsSetAsideRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsSetAsideResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAlternative
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientContractTest {
    private val contractProto: Contract = Contract.newBuilder()
        .setId("example-repo/example-contract")
        .setRepository("example-repo")
        .setTitle("Example contract title")
        .setCare("serious")
        .setState("accepted")
        .setVersion(2L)
        .setOpenDecisions(0L)
        .setConversation("example-session-1")
        .setCreated(4102444000L)
        .setChanged(4102444950L)
        .build()

    private val contractRow = ContractRow(
        id = "example-repo/example-contract",
        repository = "example-repo",
        title = "Example contract title",
        care = "serious",
        state = "accepted",
        version = 2L,
        openDecisions = 0L,
        conversation = "example-session-1",
        created = 4102444000L,
        changed = 4102444950L,
    )

    private fun bodyProto(title: String): EpicContract =
        EpicContract.newBuilder()
            .setTitle(title)
            .setApproved("Example approval sentence.")
            .setIntent("Example intent.")
            .addFailures(
                EpicFailure.newBuilder()
                    .setId("F1")
                    .setText("Example failure one.")
                    .setRating("serious")
                    .setResponse("Example response one."),
            )
            .setCare("serious")
            .addAdditions(
                EpicAddition.newBuilder()
                    .setLocation("example location")
                    .setWhat("Example addition.")
                    .setWhy("Example why.")
                    .setCost("Example cost."),
            )
            .addPremises(
                EpicPremise.newBuilder()
                    .setId("P1")
                    .setText("Example premise.")
                    .setIfWrong("Example consequence."),
            )
            .addExclusions("Example exclusion.")
            .setApproach("Example approach.")
            .addAlternatives(
                EpicAlternative.newBuilder()
                    .setAlternative("Example alternative.")
                    .setWhyNot("Example reason not.")
                    .setReconsider("Example reconsider."),
            )
            .addReleaseSteps(
                EpicReleaseStep.newBuilder()
                    .setStep("Example release step.")
                    .setTarget("example target")
                    .setUndo("Example undo."),
            )
            .setReleaseThen("Example then line.")
            .setChecks("example checks")
            .setBase(BUILD_REVISION)
            .build()

    private fun bodyRow(title: String): EpicContractRow =
        EpicContractRow(
            title = title,
            approved = "Example approval sentence.",
            intent = "Example intent.",
            failures = listOf(
                EpicFailureRow(id = "F1", text = "Example failure one.", rating = "serious", response = "Example response one."),
            ),
            care = "serious",
            additions = listOf(
                EpicAdditionRow(location = "example location", what = "Example addition.", why = "Example why.", cost = "Example cost."),
            ),
            premises = listOf(EpicPremiseRow(id = "P1", text = "Example premise.", ifWrong = "Example consequence.")),
            exclusions = listOf("Example exclusion."),
            approach = "Example approach.",
            alternatives = listOf(
                EpicAlternativeRow(alternative = "Example alternative.", whyNot = "Example reason not.", reconsider = "Example reconsider."),
            ),
            releaseSteps = listOf(
                EpicReleaseStepRow(step = "Example release step.", target = "example target", undo = "Example undo."),
            ),
            releaseThen = "Example then line.",
            checks = "example checks",
            base = BUILD_REVISION,
            accepted = 0L,
        )

    private val requirementProto: EpicRequirement = EpicRequirement.newBuilder()
        .setId("R1")
        .setText("Example requirement.")
        .setEvidence("example evidence")
        .setKind("check")
        .setConclusion("example conclusion")
        .build()

    private val requirementRow = EpicRequirementRow(
        id = "R1",
        text = "Example requirement.",
        evidence = "example evidence",
        kind = "check",
        conclusion = "example conclusion",
    )

    private val decisionProto: ContractDecision = ContractDecision.newBuilder()
        .setId("D1")
        .setQuestion("Example question?")
        .addOptions("Option A")
        .addOptions("Option B")
        .setRecommendation("Option A")
        .setReason("Example reason.")
        .setRow("R1")
        .setAnswer("")
        .build()

    private val decisionRow = ContractDecisionRow(
        id = "D1",
        question = "Example question?",
        options = listOf("Option A", "Option B"),
        recommendation = "Option A",
        reason = "Example reason.",
        row = "R1",
        answer = "",
    )

    private val firstVersionProto: ContractVersion = ContractVersion.newBuilder()
        .setVersion(1L)
        .setContract(bodyProto("Example first title"))
        .setWords("Example first words.")
        .setWriter("example-session-1")
        .setTime(4102444000L)
        .build()

    private val firstVersionRow = ContractVersionRow(
        version = 1L,
        contract = bodyRow("Example first title"),
        requirements = emptyList(),
        decisions = emptyList(),
        words = "Example first words.",
        writer = "example-session-1",
        time = 4102444000L,
        changedRows = emptyList(),
        changedSections = emptyList(),
    )

    private val newestVersionProto: ContractVersion = ContractVersion.newBuilder()
        .setVersion(2L)
        .setContract(bodyProto("Example contract title"))
        .addRequirements(requirementProto)
        .addDecisions(decisionProto)
        .setWords("Example newest words.")
        .setWriter("example-session-2")
        .setTime(4102444900L)
        .addChangedRows("R1")
        .addChangedRows("D1")
        .addChangedSections("requirements")
        .build()

    private val newestVersionRow = ContractVersionRow(
        version = 2L,
        contract = bodyRow("Example contract title"),
        requirements = listOf(requirementRow),
        decisions = listOf(decisionRow),
        words = "Example newest words.",
        writer = "example-session-2",
        time = 4102444900L,
        changedRows = listOf("R1", "D1"),
        changedSections = listOf("requirements"),
    )

    private val proposedStateProto: ContractStateChange = ContractStateChange.newBuilder()
        .setState("proposed")
        .setVersion(1L)
        .setBy("session")
        .setSession("example-session-1")
        .setWords("")
        .setTime(4102444000L)
        .build()

    private val acceptedStateProto: ContractStateChange = ContractStateChange.newBuilder()
        .setState("accepted")
        .setVersion(2L)
        .setBy("owner")
        .setWords("Example accept words.")
        .setTime(4102444950L)
        .build()

    private val recordProto: ContractsGetResponse = ContractsGetResponse.newBuilder()
        .setContract(contractProto)
        .addVersions(firstVersionProto)
        .addVersions(newestVersionProto)
        .addStates(proposedStateProto)
        .addStates(acceptedStateProto)
        .build()

    @Test
    fun contractsThrowAnyOtherError() {
        for (code in listOf(Code.UNAVAILABLE, Code.INTERNAL_ERROR)) {
            FakeOrreryServer().use { server ->
                server.fail(CONTRACTS_LIST, code)

                assertThrows(OrreryCallException::class.java) { OrreryClient(server.baseUrl).contracts() }
            }
        }
    }

    @Test
    fun contractMapsTheRecordItsVersionsAndItsStatesAsSent() {
        FakeOrreryServer().use { server ->
            server.respond(CONTRACTS_GET, recordProto)

            val record = OrreryClient(server.baseUrl).contract("example-repo/example-contract")

            assertEquals(
                ContractRecordRow(
                    contract = contractRow,
                    versions = listOf(firstVersionRow, newestVersionRow),
                    states = listOf(
                        ContractStateRow(
                            state = "proposed",
                            version = 1L,
                            by = "session",
                            session = "example-session-1",
                            words = "",
                            time = 4102444000L,
                        ),
                        ContractStateRow(
                            state = "accepted",
                            version = 2L,
                            by = "owner",
                            session = "",
                            words = "Example accept words.",
                            time = 4102444950L,
                        ),
                    ),
                ),
                record,
            )
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_GET, request)
            assertEquals("example-repo/example-contract", ContractsGetRequest.parseFrom(request.body).id)
        }
    }

    @Test
    fun contractNotFoundIsNull() {
        FakeOrreryServer().use { server ->
            server.fail(CONTRACTS_GET, Code.NOT_FOUND, "not_found")

            assertNull(OrreryClient(server.baseUrl).contract("example-repo/missing"))
        }
    }

    @Test
    fun acceptContractSendsTheIdWithEmptyWordsAndAnswersDone() {
        FakeOrreryServer().use { server ->
            server.respond(CONTRACTS_ACCEPT, ContractsAcceptResponse.newBuilder().setContract(contractProto).build())

            val result = OrreryClient(server.baseUrl).acceptContract("example-repo/example-contract")

            assertEquals(ContractCallResult.Done(contractRow), result)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_ACCEPT, request)
            val sent = ContractsAcceptRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals("", sent.words)
        }
    }

    @Test
    fun setAsideContractSendsItsWordsAndAnswersDone() {
        FakeOrreryServer().use { server ->
            val setAside = contractProto.toBuilder().setState("set_aside").build()
            server.respond(CONTRACTS_SET_ASIDE, ContractsSetAsideResponse.newBuilder().setContract(setAside).build())

            val result = OrreryClient(server.baseUrl).setAsideContract("example-repo/example-contract", "Example set aside words.")

            assertEquals(ContractCallResult.Done(contractRow.copy(state = "set_aside")), result)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_SET_ASIDE, request)
            val sent = ContractsSetAsideRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals("Example set aside words.", sent.words)
        }
    }

    @Test
    fun reopenContractSendsEmptyWordsAndAnswersDone() {
        FakeOrreryServer().use { server ->
            val proposed = contractProto.toBuilder().setState("proposed").build()
            server.respond(CONTRACTS_REOPEN, ContractsReopenResponse.newBuilder().setContract(proposed).build())

            val result = OrreryClient(server.baseUrl).reopenContract("example-repo/example-contract")

            assertEquals(ContractCallResult.Done(contractRow.copy(state = "proposed")), result)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_REOPEN, request)
            val sent = ContractsReopenRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals("", sent.words)
        }
    }

    @Test
    fun refusedAcceptSetAsideAndReopenAnswerRefusedWithTheServerMessage() {
        for (code in listOf(Code.FAILED_PRECONDITION, Code.INVALID_ARGUMENT, Code.ABORTED, Code.PERMISSION_DENIED, Code.NOT_FOUND)) {
            FakeOrreryServer().use { server ->
                val message = "example refusal ${code.name}"
                server.fail(CONTRACTS_ACCEPT, code, message)
                server.fail(CONTRACTS_SET_ASIDE, code, message)
                server.fail(CONTRACTS_REOPEN, code, message)
                val client = OrreryClient(server.baseUrl)

                assertEquals(
                    ContractCallResult.Refused(message),
                    client.acceptContract("example-repo/example-contract"),
                )
                assertEquals(
                    ContractCallResult.Refused(message),
                    client.setAsideContract("example-repo/example-contract", "Example set aside words."),
                )
                assertEquals(
                    ContractCallResult.Refused(message),
                    client.reopenContract("example-repo/example-contract"),
                )
            }
        }
    }

    @Test
    fun unavailableOnAcceptThrowsItsCallException() {
        FakeOrreryServer().use { server ->
            server.fail(CONTRACTS_ACCEPT, Code.UNAVAILABLE, "unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).acceptContract("example-repo/example-contract")
            }

            assertEquals(Code.UNAVAILABLE, error.code)
        }
    }

    @Test
    fun aStreamItemsContractFieldReachesConversationItemContract() {
        FakeOrreryServer().use { server ->
            val response = WatchConversationResponse.newBuilder()
                .setTranscript("example-transcript")
                .setSeq(1L)
                .addItems(
                    ConversationItemProto.newBuilder()
                        .setKind("tool")
                        .setTime(4102444100L)
                        .setTool("contract_propose")
                        .setId("example-tool-use")
                        .setContract("example-repo/example-contract"),
                )
                .build()
            server.route(WATCH_CONVERSATION) { Reply.Stream(listOf(response), StreamEnd.Clean) }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchConversation("%3", "", 0L) { received.add(it) }
            }

            assertEquals("example-repo/example-contract", received.single().items.single().contract)
        }
    }

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val CONTRACTS_GET = "/orrery.v1.Contracts/Get"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val CONTRACTS_ACCEPT = "/orrery.v1.Contracts/Accept"
        const val CONTRACTS_SET_ASIDE = "/orrery.v1.Contracts/SetAside"
        const val CONTRACTS_REOPEN = "/orrery.v1.Contracts/Reopen"
        const val WATCH_CONVERSATION = "/orrery.v1.Panes/WatchConversation"
        const val BUILD_REVISION = "0123456789abcdef0123456789abcdef01234567"
    }
}
