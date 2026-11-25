package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractDecision
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractLeft
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractLength
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRefusal
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementTask
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementWork
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRowWrite
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractShown
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractStateChange
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractWorking
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsKeepRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsKeepResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsShownRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsShownResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsStrikeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsStrikeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignApproach
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignChoice
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignDecision
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignDelegation
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignLedgerItem
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignLogLine
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStage
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStep
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsAnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineDesign
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.gen.orrery.v1.RecordMaker
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertNull
import org.junit.Test
import java.io.IOException

class OrreryClientStageTest {
    private val ownerMaker: RecordMaker = RecordMaker.newBuilder().setKind("owner_serve").build()

    private val conversationMaker: RecordMaker = RecordMaker.newBuilder()
        .setKind("conversation")
        .setReason("Example reason.")
        .setDelegation("Example delegation words.")
        .setSession("example-session-2")
        .build()

    private val ownerConversationMaker: RecordMaker = RecordMaker.newBuilder()
        .setKind("owner_conversation")
        .setQuote("Example quote.")
        .build()

    private val designProto: Design = Design.newBuilder()
        .setId("example-design-1")
        .setTitle("Example design title")
        .setConversation("example-session-1")
        .setCreated(4102444000L)
        .setOfOne(false)
        .setState("1 of 2 accepted")
        .addContracts(
            DesignContract.newBuilder()
                .setId("example-repo/write-first")
                .setTitle("Example first contract")
                .setRepository("example-repo")
                .setState("accepted")
                .setPosition(1L)
                .setEpic("example-epic-1")
                .setEpicPhase("build")
                .build(),
        )
        .build()

    private val designRow = DesignRow(
        id = "example-design-1",
        title = "Example design title",
        conversation = "example-session-1",
        created = 4102444000L,
        ofOne = false,
        state = "1 of 2 accepted",
        contracts = listOf(
            DesignContractRow(
                id = "example-repo/write-first",
                title = "Example first contract",
                repository = "example-repo",
                state = "accepted",
                position = 1L,
                after = emptyList(),
                epic = "example-epic-1",
                epicPhase = "build",
            ),
        ),
    )

    private val approachOneProto: DesignApproach = DesignApproach.newBuilder()
        .setSeq(1L)
        .setName("Example approach one")
        .setShape("Example shape one.")
        .setCost("Example cost one.")
        .setWins("Example wins one.")
        .setLoses("Example loses one.")
        .setReconsider("Example reconsider one.")
        .addResearch("example-step-1")
        .setRecommended(true)
        .setReason("Example reason one.")
        .setTime(4102444010L)
        .build()

    private val approachTwoProto: DesignApproach = DesignApproach.newBuilder()
        .setSeq(2L)
        .setName("Example approach two")
        .setShape("Example shape two.")
        .setCost("Example cost two.")
        .setWins("Example wins two.")
        .setLoses("Example loses two.")
        .setReconsider("Example reconsider two.")
        .setRecommended(false)
        .setReason("Example reason two.")
        .setTime(4102444020L)
        .build()

    private val choiceProto: DesignChoice = DesignChoice.newBuilder()
        .setApproach(1L)
        .setWords("Example choice words.")
        .setMaker(ownerMaker)
        .setTime(4102444030L)
        .build()

    private val logOneProto: DesignLogLine = DesignLogLine.newBuilder()
        .setSeq(1L)
        .setTime(4102444040L)
        .setMaker(conversationMaker)
        .setSession("example-session-2")
        .setWhat("asked")
        .addIds("Q1")
        .setWords("Example question words.")
        .build()

    private val logTwoProto: DesignLogLine = DesignLogLine.newBuilder()
        .setSeq(2L)
        .setTime(4102444050L)
        .setWhat("answered")
        .addIds("Q1")
        .setWords("Option A")
        .build()

    private val delegationProto: DesignDelegation = DesignDelegation.newBuilder()
        .setSeq(1L)
        .addKinds("question")
        .addKinds("choice")
        .setMessage("Example delegating message.")
        .setSession("example-session-1")
        .setTime(4102444060L)
        .addDecisions(
            DesignDecision.newBuilder()
                .setKind("question")
                .setRef("Q1")
                .setText("Example decided text.")
                .setReason("Example decision reason.")
                .setTime(4102444070L)
                .build(),
        )
        .build()

    private val stepOneProto: DesignStep = DesignStep.newBuilder()
        .setStep("example-step-1")
        .setKind("research")
        .setSubject("Example research question?")
        .setCreated(4102444080L)
        .setStatus("done")
        .setReport("Example report.")
        .setCounted(true)
        .build()

    private val stepTwoProto: DesignStep = DesignStep.newBuilder()
        .setStep("example-step-2")
        .setKind("steelman")
        .setSubject("Example packet subject.")
        .setCreated(4102444090L)
        .setStatus("done")
        .setVerdict("holds")
        .setCounted(false)
        .build()

    private val answeredQuestionProto: DesignQuestion = DesignQuestion.newBuilder()
        .setId(1L)
        .setContract("example-repo/write-first")
        .setText("Example question?")
        .addOptions("Option A")
        .addOptions("Option B")
        .setRecommendation("Option A")
        .setAsked(4102444100L)
        .setAnswer("Option A")
        .setAccepted(true)
        .setMaker(conversationMaker)
        .setAnswered(4102444110L)
        .build()

    private val openQuestionProto: DesignQuestion = DesignQuestion.newBuilder()
        .setId(2L)
        .setText("Example open question?")
        .addOptions("Yes")
        .addOptions("No")
        .setRecommendation("No")
        .setAsked(4102444120L)
        .build()

    private val answeredLedgerProto: DesignLedgerItem = DesignLedgerItem.newBuilder()
        .setId("1")
        .setContract("example-repo/write-first")
        .setOrigin("discovery")
        .setImpact("Example impact.")
        .setEvidence("Example evidence.")
        .setChange("Example change.")
        .setDisposition("adopted")
        .setMaker(ownerMaker)
        .setClosureWord("held")
        .setClosureNote("Example closure note.")
        .build()

    private val openLedgerProto: DesignLedgerItem = DesignLedgerItem.newBuilder()
        .setId("2")
        .setOrigin("closure")
        .setImpact("Example open impact.")
        .setEvidence("Example open evidence.")
        .setChange("Example open change.")
        .setDisposition("open")
        .build()

    private val stageProto: DesignStage = DesignStage.newBuilder()
        .addSteps(stepOneProto)
        .addSteps(stepTwoProto)
        .addQuestions(answeredQuestionProto)
        .addQuestions(openQuestionProto)
        .addLedger(answeredLedgerProto)
        .addLedger(openLedgerProto)
        .setFrozen(true)
        .setClosureSkipped(false)
        .setDiscoveryCounted(true)
        .setClosureCounted(false)
        .build()

    private val recordMakerRow = RecordMakerRow(
        kind = "conversation",
        reason = "Example reason.",
        delegation = "Example delegation words.",
        session = "example-session-2",
    )

    private val answeredQuestionRow = DesignQuestionRow(
        design = "example-design-1",
        id = 1L,
        contract = "example-repo/write-first",
        text = "Example question?",
        options = listOf("Option A", "Option B"),
        recommendation = "Option A",
        asked = 4102444100L,
        answer = "Option A",
        accepted = true,
        maker = recordMakerRow,
        answered = 4102444110L,
    )

    private val designRecordProto: DesignsGetResponse = DesignsGetResponse.newBuilder()
        .setDesign(designProto)
        .addApproaches(approachOneProto)
        .addApproaches(approachTwoProto)
        .setChoice(choiceProto)
        .addLog(logOneProto)
        .addLog(logTwoProto)
        .addDelegations(delegationProto)
        .setStage(stageProto)
        .build()

    private val designRecordRow = DesignRecordRow(
        design = designRow,
        approaches = listOf(
            DesignApproachRow(
                seq = 1L,
                name = "Example approach one",
                shape = "Example shape one.",
                cost = "Example cost one.",
                wins = "Example wins one.",
                loses = "Example loses one.",
                reconsider = "Example reconsider one.",
                research = listOf("example-step-1"),
                recommended = true,
                reason = "Example reason one.",
                time = 4102444010L,
            ),
            DesignApproachRow(
                seq = 2L,
                name = "Example approach two",
                shape = "Example shape two.",
                cost = "Example cost two.",
                wins = "Example wins two.",
                loses = "Example loses two.",
                reconsider = "Example reconsider two.",
                research = emptyList(),
                recommended = false,
                reason = "Example reason two.",
                time = 4102444020L,
            ),
        ),
        choice = DesignChoiceRow(
            approach = 1L,
            words = "Example choice words.",
            maker = RecordMakerRow(kind = "owner_serve"),
            time = 4102444030L,
        ),
        log = listOf(
            DesignLogRow(
                seq = 1L,
                time = 4102444040L,
                maker = recordMakerRow,
                session = "example-session-2",
                what = "asked",
                ids = listOf("Q1"),
                words = "Example question words.",
            ),
            DesignLogRow(
                seq = 2L,
                time = 4102444050L,
                maker = null,
                session = "",
                what = "answered",
                ids = listOf("Q1"),
                words = "Option A",
            ),
        ),
        delegations = listOf(
            DesignDelegationRow(
                seq = 1L,
                kinds = listOf("question", "choice"),
                message = "Example delegating message.",
                session = "example-session-1",
                time = 4102444060L,
                decisions = listOf(
                    DesignDecisionRow(
                        kind = "question",
                        ref = "Q1",
                        text = "Example decided text.",
                        reason = "Example decision reason.",
                        time = 4102444070L,
                    ),
                ),
            ),
        ),
        stage = DesignStageRow(
            steps = listOf(
                DesignStepRow(
                    step = "example-step-1",
                    kind = "research",
                    subject = "Example research question?",
                    created = 4102444080L,
                    status = "done",
                    report = "Example report.",
                    verdict = "",
                    counted = true,
                ),
                DesignStepRow(
                    step = "example-step-2",
                    kind = "steelman",
                    subject = "Example packet subject.",
                    created = 4102444090L,
                    status = "done",
                    report = "",
                    verdict = "holds",
                    counted = false,
                ),
            ),
            questions = listOf(
                answeredQuestionRow,
                DesignQuestionRow(
                    design = "example-design-1",
                    id = 2L,
                    contract = "",
                    text = "Example open question?",
                    options = listOf("Yes", "No"),
                    recommendation = "No",
                    asked = 4102444120L,
                    answer = "",
                    accepted = false,
                    maker = null,
                    answered = 0L,
                ),
            ),
            ledger = listOf(
                DesignLedgerItemRow(
                    id = "1",
                    contract = "example-repo/write-first",
                    origin = "discovery",
                    impact = "Example impact.",
                    evidence = "Example evidence.",
                    change = "Example change.",
                    disposition = "adopted",
                    reason = "",
                    boundary = "",
                    maker = RecordMakerRow(kind = "owner_serve"),
                    closureWord = "held",
                    closureNote = "Example closure note.",
                ),
                DesignLedgerItemRow(
                    id = "2",
                    contract = "",
                    origin = "closure",
                    impact = "Example open impact.",
                    evidence = "Example open evidence.",
                    change = "Example open change.",
                    disposition = "open",
                    reason = "",
                    boundary = "",
                    maker = null,
                    closureWord = "",
                    closureNote = "",
                ),
            ),
            frozen = true,
            closureSkipped = false,
            discoveryCounted = true,
            closureCounted = false,
        ),
    )

    private val answeredTypedProto: DesignQuestion = DesignQuestion.newBuilder()
        .setId(1L)
        .setContract("example-repo/write-first")
        .setText("Example question?")
        .addOptions("Option A")
        .addOptions("Option B")
        .setRecommendation("Option A")
        .setAsked(4102444100L)
        .setAnswer("Example typed answer.")
        .setAccepted(false)
        .setMaker(ownerMaker)
        .setAnswered(4102444130L)
        .build()

    private val contractProto: Contract = Contract.newBuilder()
        .setId("example-repo/example-contract")
        .setRepository("example-repo")
        .setTitle("Example contract title")
        .setCare("serious")
        .setState("proposed")
        .setVersion(3L)
        .setOpenDecisions(1L)
        .setConversation("example-session-1")
        .setCreated(4102444000L)
        .setChanged(4102444800L)
        .build()

    private val contractRow = ContractRow(
        id = "example-repo/example-contract",
        repository = "example-repo",
        title = "Example contract title",
        care = "serious",
        state = "proposed",
        version = 3L,
        openDecisions = 1L,
        conversation = "example-session-1",
        created = 4102444000L,
        changed = 4102444800L,
    )

    private val workingProto: ContractWorking = ContractWorking.newBuilder()
        .setVersion(3L)
        .setContract(
            EpicContract.newBuilder()
                .setTitle("Example working title")
                .setCare("serious")
                .setApproach("Example working approach.")
                .build(),
        )
        .addRequirements(
            EpicRequirement.newBuilder()
                .setId("R1")
                .setText("Example requirement.")
                .setEvidence("example evidence")
                .setKind("check")
                .build(),
        )
        .addDecisions(
            ContractDecision.newBuilder()
                .setId("D1")
                .setQuestion("Example question?")
                .addOptions("Option A")
                .addOptions("Option B")
                .setRecommendation("Option A")
                .setReason("Example reason.")
                .setRow("R1")
                .setAnswer("")
                .build(),
        )
        .setWriter("example-session-3")
        .setUpdated(4102444800L)
        .addChangedRows("R1")
        .build()

    private val refusalProto: ContractRefusal = ContractRefusal.newBuilder()
        .setSeq(1L)
        .setVersion(3L)
        .setField("approach")
        .setText("Example refused text.")
        .setSession("example-session-3")
        .setTime(4102444810L)
        .build()

    private val writeWithMakerProto: ContractRowWrite = ContractRowWrite.newBuilder()
        .setSeq(1L)
        .setVersion(3L)
        .setRow("R1")
        .setAction("write")
        .setDepends("")
        .setWords("Example write words.")
        .setMaker(ownerConversationMaker)
        .setTime(4102444820L)
        .build()

    private val writeWithoutMakerProto: ContractRowWrite = ContractRowWrite.newBuilder()
        .setSeq(2L)
        .setVersion(3L)
        .setRow("P1")
        .setAction("keep")
        .setDepends("R1")
        .setWords("")
        .setTime(4102444830L)
        .build()

    private val leftProto: ContractLeft = ContractLeft.newBuilder()
        .setKind("decision")
        .setRef("D1")
        .setText("Example left text.")
        .build()

    private val proofProto: EvidenceRun = EvidenceRun.newBuilder()
        .setId("example-run-1")
        .setPhase("green")
        .setSource("captured")
        .setPassed(true)
        .setRevision("0123456789abcdef0123456789abcdef01234567")
        .setCreated(4102444840L)
        .setEnded(4102444850L)
        .setLogBytes(12L)
        .build()

    private val workProto: ContractRequirementWork = ContractRequirementWork.newBuilder()
        .setId("R1")
        .setText("Example requirement.")
        .setState("proven")
        .addTasks(
            ContractRequirementTask.newBuilder()
                .setId("T1")
                .setTitle("Example task")
                .setStatus("done")
                .setPlace("build")
                .build(),
        )
        .setProof(proofProto)
        .build()

    private val shownProto: ContractShown = ContractShown.newBuilder()
        .setVersion(3L)
        .setRow("R1")
        .setTime(4102444860L)
        .build()

    private val lengthProto: ContractLength = ContractLength.newBuilder()
        .setLines(120L)
        .setWarn(150L)
        .setStrong(300L)
        .build()

    private val stateProto: ContractStateChange = ContractStateChange.newBuilder()
        .setState("accepted")
        .setVersion(3L)
        .setBy("owner")
        .setWords("Example accept words.")
        .setTime(4102444870L)
        .setMaker(ownerMaker)
        .build()

    private val contractRecordProto: ContractsGetResponse = ContractsGetResponse.newBuilder()
        .setContract(contractProto)
        .addStates(stateProto)
        .setWorking(workingProto)
        .addRefusals(refusalProto)
        .addRows(writeWithMakerProto)
        .addRows(writeWithoutMakerProto)
        .addLeft(leftProto)
        .addWork(workProto)
        .addShown(shownProto)
        .setLength(lengthProto)
        .build()

    @Test
    fun designMapsEveryPartOfItsRecordWithItsMakersAndItsStage() {
        FakeOrreryServer().use { server ->
            server.respond(DESIGNS_GET, designRecordProto)

            val record = OrreryClient(server.baseUrl).design("example-design-1")

            assertEquals(designRecordRow, record)
            val request = server.requests.single()
            assertConnectRequest(DESIGNS_GET, request)
            assertEquals("example-design-1", DesignsGetRequest.parseFrom(request.body).id)
        }
    }

    @Test
    fun designWithNoStageMapsWithANullStageAndNoRecords() {
        FakeOrreryServer().use { server ->
            server.respond(DESIGNS_GET, DesignsGetResponse.newBuilder().setDesign(designProto).build())

            val record = OrreryClient(server.baseUrl).design("example-design-1")

            assertEquals(
                DesignRecordRow(
                    design = designRow,
                    approaches = emptyList(),
                    choice = null,
                    log = emptyList(),
                    delegations = emptyList(),
                    stage = null,
                ),
                record,
            )
        }
    }

    @Test
    fun designOfAnUnknownIdIsNull() {
        FakeOrreryServer().use { server ->
            server.fail(DESIGNS_GET, Code.NOT_FOUND, "not_found")

            assertNull(OrreryClient(server.baseUrl).design("example-design-missing"))
        }
    }

    @Test
    fun acceptedAnswerToADesignQuestionSendsItsFieldsAndAnswersDoneWithTheQuestionReadBack() {
        FakeOrreryServer().use { server ->
            server.respond(DESIGNS_ANSWER, DesignsAnswerResponse.newBuilder().setAnswer("Option A").build())
            server.respond(
                DESIGNS_GET,
                DesignsGetResponse.newBuilder()
                    .setDesign(designProto)
                    .setStage(DesignStage.newBuilder().addQuestions(answeredQuestionProto).build())
                    .build(),
            )

            val result = OrreryClient(server.baseUrl).answerDesignQuestion(
                "example-design-1",
                1L,
                "",
                accept = true,
            )

            assertEquals(DesignAnswerResult.Done(answeredQuestionRow), result)
            assertEquals(listOf(DESIGNS_ANSWER, DESIGNS_GET), server.requests.map { it.rawPath })
            val answerRequest = server.requests.first()
            assertConnectRequest(DESIGNS_ANSWER, answerRequest)
            val sent = DesignsAnswerRequest.parseFrom(answerRequest.body)
            assertEquals("example-design-1", sent.design)
            assertEquals(1L, sent.question)
            assertEquals("", sent.answer)
            assertEquals(true, sent.accept)
            assertEquals("example-design-1", DesignsGetRequest.parseFrom(server.requests[1].body).id)
        }
    }

    @Test
    fun answerDesignQuestionReadsTheQuestionBackAsTheStageHoldsItAfterTheAnswer() {
        FakeOrreryServer().use { server ->
            server.respond(DESIGNS_ANSWER, DesignsAnswerResponse.newBuilder().setAnswer("Example typed answer.").build())
            server.respond(
                DESIGNS_GET,
                DesignsGetResponse.newBuilder()
                    .setDesign(designProto)
                    .setStage(DesignStage.newBuilder().addQuestions(answeredTypedProto).build())
                    .build(),
            )

            val result = OrreryClient(server.baseUrl).answerDesignQuestion(
                "example-design-1",
                1L,
                "Example typed answer.",
                accept = false,
            )

            assertEquals(
                DesignAnswerResult.Done(
                    DesignQuestionRow(
                        design = "example-design-1",
                        id = 1L,
                        contract = "example-repo/write-first",
                        text = "Example question?",
                        options = listOf("Option A", "Option B"),
                        recommendation = "Option A",
                        asked = 4102444100L,
                        answer = "Example typed answer.",
                        accepted = false,
                        maker = RecordMakerRow(kind = "owner_serve"),
                        answered = 4102444130L,
                    ),
                ),
                result,
            )
        }
    }

    @Test
    fun refusedAnswerToADesignQuestionAnswersRefusedWithTheServerMessage() {
        val refusals = listOf(
            Code.FAILED_PRECONDITION,
            Code.INVALID_ARGUMENT,
            Code.PERMISSION_DENIED,
            Code.NOT_FOUND,
        )
        for (code in refusals) {
            FakeOrreryServer().use { server ->
                val message = "example refusal ${code.name}"
                server.fail(DESIGNS_ANSWER, code, message)

                val result = OrreryClient(server.baseUrl).answerDesignQuestion(
                    "example-design-1",
                    2L,
                    "Example typed answer.",
                    accept = false,
                )

                assertEquals(DesignAnswerResult.Refused(message), result)
                val sent = DesignsAnswerRequest.parseFrom(server.requests.single().body)
                assertEquals("example-design-1", sent.design)
                assertEquals(2L, sent.question)
                assertEquals("Example typed answer.", sent.answer)
                assertEquals(false, sent.accept)
            }
        }
    }

    @Test
    fun unavailableOnAnswerThrowsItsCallException() {
        FakeOrreryServer().use { server ->
            server.fail(DESIGNS_ANSWER, Code.UNAVAILABLE, "unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).answerDesignQuestion("example-design-1", 1L, "", accept = true)
            }

            assertEquals(Code.UNAVAILABLE, error.code)
        }
    }

    @Test
    fun answeredDesignWhoseStageHoldsNoSuchQuestionThrows() {
        FakeOrreryServer().use { server ->
            server.respond(DESIGNS_ANSWER, DesignsAnswerResponse.newBuilder().setAnswer("Option A").build())
            server.respond(DESIGNS_GET, DesignsGetResponse.newBuilder().setDesign(designProto).build())

            assertThrows(IOException::class.java) {
                OrreryClient(server.baseUrl).answerDesignQuestion("example-design-1", 1L, "", accept = true)
            }
        }
    }

    @Test
    fun contractCarriesItsStageRecordsItsWorkingCopyAndItsLength() {
        FakeOrreryServer().use { server ->
            server.respond(CONTRACTS_GET, contractRecordProto)

            val record = OrreryClient(server.baseUrl).contract("example-repo/example-contract")

            assertEquals(
                ContractRecordRow(
                    contract = contractRow,
                    versions = emptyList(),
                    states = listOf(
                        ContractStateRow(
                            state = "accepted",
                            version = 3L,
                            by = "owner",
                            session = "",
                            words = "Example accept words.",
                            time = 4102444870L,
                            maker = RecordMakerRow(kind = "owner_serve"),
                        ),
                    ),
                    stage = ContractStageRow(
                        working = ContractWorkingRow(
                            version = 3L,
                            contract = EpicContractRow(
                                title = "Example working title",
                                approved = "",
                                intent = "",
                                failures = emptyList(),
                                care = "serious",
                                additions = emptyList(),
                                premises = emptyList(),
                                exclusions = emptyList(),
                                approach = "Example working approach.",
                                alternatives = emptyList(),
                                releaseSteps = emptyList(),
                                releaseThen = "",
                                checks = "",
                                base = "",
                                accepted = 0L,
                            ),
                            requirements = listOf(
                                EpicRequirementRow(
                                    id = "R1",
                                    text = "Example requirement.",
                                    evidence = "example evidence",
                                    kind = "check",
                                ),
                            ),
                            decisions = listOf(
                                ContractDecisionRow(
                                    id = "D1",
                                    question = "Example question?",
                                    options = listOf("Option A", "Option B"),
                                    recommendation = "Option A",
                                    reason = "Example reason.",
                                    row = "R1",
                                    answer = "",
                                ),
                            ),
                            writer = "example-session-3",
                            updated = 4102444800L,
                            changedRows = listOf("R1"),
                        ),
                        refusals = listOf(
                            ContractRefusalRow(
                                seq = 1L,
                                version = 3L,
                                field = "approach",
                                text = "Example refused text.",
                                session = "example-session-3",
                                time = 4102444810L,
                            ),
                        ),
                        writes = listOf(
                            ContractRowWriteRow(
                                seq = 1L,
                                version = 3L,
                                row = "R1",
                                action = "write",
                                depends = "",
                                words = "Example write words.",
                                maker = RecordMakerRow(kind = "owner_conversation", quote = "Example quote."),
                                time = 4102444820L,
                            ),
                            ContractRowWriteRow(
                                seq = 2L,
                                version = 3L,
                                row = "P1",
                                action = "keep",
                                depends = "R1",
                                words = "",
                                maker = null,
                                time = 4102444830L,
                            ),
                        ),
                        left = listOf(ContractLeftRow(kind = "decision", ref = "D1", text = "Example left text.")),
                        work = listOf(
                            ContractRequirementWorkRow(
                                id = "R1",
                                text = "Example requirement.",
                                state = "proven",
                                tasks = listOf(
                                    ContractRequirementTaskRow(
                                        id = "T1",
                                        title = "Example task",
                                        status = "done",
                                        place = "build",
                                    ),
                                ),
                                proof = EvidenceRunRow(
                                    id = "example-run-1",
                                    phase = "green",
                                    source = "captured",
                                    state = "",
                                    exit = 0,
                                    timedOut = false,
                                    notCaptured = "",
                                    verdict = "",
                                    judgeReason = "",
                                    passed = true,
                                    revision = "0123456789abcdef0123456789abcdef01234567",
                                    created = 4102444840L,
                                    ended = 4102444850L,
                                    logBytes = 12L,
                                    stderrBytes = 0L,
                                    artifacts = emptyList(),
                                ),
                            ),
                        ),
                        shown = listOf(ContractShownRow(version = 3L, row = "R1", time = 4102444860L)),
                        length = ContractLengthRow(lines = 120L, warn = 150L, strong = 300L),
                    ),
                ),
                record,
            )
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_GET, request)
        }
    }

    @Test
    fun keepAdditionSendsItsIdAdditionAndWordsAndAnswersDone() {
        FakeOrreryServer().use { server ->
            server.respond(CONTRACTS_KEEP, ContractsKeepResponse.newBuilder().setContract(contractProto).build())

            val result = OrreryClient(server.baseUrl).keepAddition(
                "example-repo/example-contract",
                "A1",
                "Example keep words.",
            )

            assertEquals(ContractCallResult.Done(contractRow), result)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_KEEP, request)
            val sent = ContractsKeepRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals("A1", sent.addition)
            assertEquals("Example keep words.", sent.words)
        }
    }

    @Test
    fun strikeAdditionSendsItsIdAdditionAndEmptyWordsByDefaultAndAnswersDone() {
        FakeOrreryServer().use { server ->
            val struck = ContractsStrikeResponse.newBuilder()
                .setContract(contractProto)
                .addRemoved("A2")
                .addRemoved("R2")
                .build()
            server.respond(CONTRACTS_STRIKE, struck)

            val result = OrreryClient(server.baseUrl).strikeAddition("example-repo/example-contract", "A2")

            assertEquals(ContractCallResult.Done(contractRow), result)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_STRIKE, request)
            val sent = ContractsStrikeRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals("A2", sent.addition)
            assertEquals("", sent.words)
        }
    }

    @Test
    fun refusedKeepAndStrikeAnswerRefusedWithTheServerMessage() {
        val refusals = listOf(
            Code.FAILED_PRECONDITION,
            Code.INVALID_ARGUMENT,
            Code.ABORTED,
            Code.PERMISSION_DENIED,
            Code.NOT_FOUND,
        )
        for (code in refusals) {
            FakeOrreryServer().use { server ->
                val message = "example refusal ${code.name}"
                server.fail(CONTRACTS_KEEP, code, message)
                server.fail(CONTRACTS_STRIKE, code, message)
                val client = OrreryClient(server.baseUrl)

                assertEquals(
                    ContractCallResult.Refused(message),
                    client.keepAddition("example-repo/example-contract", "A1"),
                )
                assertEquals(
                    ContractCallResult.Refused(message),
                    client.strikeAddition("example-repo/example-contract", "A1"),
                )
            }
        }
    }

    @Test
    fun markShownSendsItsIdVersionAndRowsAndAnswersTheRowsTheServerMarked() {
        FakeOrreryServer().use { server ->
            server.respond(CONTRACTS_SHOWN, ContractsShownResponse.newBuilder().addRows("R1").addRows("P1").build())

            val marked = OrreryClient(server.baseUrl)
                .markShown("example-repo/example-contract", 3L, listOf("R1", "P1", "F1"))

            assertEquals(listOf("R1", "P1"), marked)
            val request = server.requests.single()
            assertConnectRequest(CONTRACTS_SHOWN, request)
            val sent = ContractsShownRequest.parseFrom(request.body)
            assertEquals("example-repo/example-contract", sent.id)
            assertEquals(3L, sent.version)
            assertEquals(listOf("R1", "P1", "F1"), sent.rowsList)
        }
    }

    @Test
    fun refusedMarkShownAnswersNoRows() {
        FakeOrreryServer().use { server ->
            server.fail(CONTRACTS_SHOWN, Code.FAILED_PRECONDITION, "example refusal")

            val marked = OrreryClient(server.baseUrl).markShown("example-repo/example-contract", 3L, listOf("R1"))

            assertEquals(emptyList<String>(), marked)
        }
    }

    @Test
    fun aConversationItemsDesignIdWhatAndIdsReadFromItsEngineDesign() {
        val response = ConversationResponse.newBuilder()
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("design")
                    .setTime(4102444900L)
                    .setText("answered")
                    .setDesign(
                        EngineDesign.newBuilder()
                            .setDesign("example-design-1")
                            .setWhat("answered")
                            .addIds("Q1")
                            .addIds("example-repo/write-first")
                            .setWords("Option A")
                            .setMaker(ownerMaker)
                            .setSession("example-session-1")
                            .build(),
                    ),
            )
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("prompt")
                    .setTime(4102444910L)
                    .setText("Example owner prompt."),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val items = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items

            val design = items[0]
            assertEquals("example-design-1", design.design)
            assertEquals("answered", design.designWhat)
            assertEquals(listOf("Q1", "example-repo/write-first"), design.designIds)
            val prompt = items[1]
            assertEquals("", prompt.design)
            assertEquals("", prompt.designWhat)
            assertEquals(emptyList<String>(), prompt.designIds)
        }
    }

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val DESIGNS_GET = "/orrery.v1.Designs/Get"
        const val DESIGNS_ANSWER = "/orrery.v1.Designs/Answer"
        const val CONTRACTS_GET = "/orrery.v1.Contracts/Get"
        const val CONTRACTS_KEEP = "/orrery.v1.Contracts/Keep"
        const val CONTRACTS_STRIKE = "/orrery.v1.Contracts/Strike"
        const val CONTRACTS_SHOWN = "/orrery.v1.Contracts/Shown"
        const val CONVERSATION = "/orrery.v1.Panes/Conversation"
    }
}
