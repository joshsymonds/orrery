package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStage
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsAnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineDesign
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.RecordMaker
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.time.ZoneId
import java.util.concurrent.atomic.AtomicReference
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// A question the Director asked about a design is one thread, drawn in the conversation where it was asked and on the design
// page, from the same record: an answer given in either place is sent once and shows settled in both. Read through the
// semantics tree alone, with the session, its design and the answers invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class QuestionThreadPlacesTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ASKED = NOW - 120
        const val SESSION_ID = "synthetic-session"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val QUESTION_KEY = "Q1"
        const val QUESTION_NUMBER = 1L
        const val QUESTION = "Synthetic question text"
        const val KEEP = "Synthetic keep option"
        const val SPLIT = "Synthetic split option"
        const val TYPED = "synthetic words typed"
        const val THREAD_TAG = "question-thread:$QUESTION_KEY"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val DESIGN_BODY_TAG = "sheet-body:design:$DESIGN_ID"
        const val CONVERSATION_TAG = "conversation-list"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val DESIGN_GET_PATH = "/orrery.v1.Designs/Get"
        const val ANSWER_PATH = "/orrery.v1.Designs/Answer"
        const val EPICS_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
        const val ASKED_IN_CONVERSATION = "asked here"
        const val ASKED_ON_DESIGN = "asked in the conversation"
        const val ACCEPTED = "accepted the recommendation"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The question as Designs.Get serves it now: open until an answer lands, then settled by that answer. */
    private val stored = AtomicReference(openQuestion())

    private fun openQuestion(): DesignQuestion = DesignQuestion.newBuilder()
        .setId(QUESTION_NUMBER)
        .setText(QUESTION)
        .addOptions(KEEP)
        .addOptions(SPLIT)
        .setRecommendation(SPLIT)
        .setAsked(ASKED)
        .build()

    /** The question once an answer of [words] lands: the recommendation when [accept], else the words, made through serve. */
    private fun settledBy(words: String, accept: Boolean): DesignQuestion = openQuestion().toBuilder()
        .setAnswer(if (accept) SPLIT else words)
        .setAccepted(accept)
        .setMaker(RecordMaker.newBuilder().setKind("owner_serve"))
        .setAnswered(NOW - 60)
        .build()

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
        }
    }

    /**
     * Streams the headless session [SESSION_ID], whose transcript holds the Director's asked record of question Q1 about the
     * design, and serves the design the conversation works with its question open. An answer that lands settles it.
     */
    private fun serveSessionAndDesign(server: FakeOrreryServer) {
        val asked = ConversationItemProto.newBuilder()
            .setKind("design")
            .setTime(ASKED)
            .setDesign(EngineDesign.newBuilder().setDesign(DESIGN_ID).setWhat("asked").addIds(QUESTION_KEY))
            .build()
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/lighthouse")
            .setLabel("synthetic session")
            .setState("working")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(ConversationState.newBuilder().setOrigin("synthetic origin").setSession(session).build())
            .addItems(asked)
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }

        val design = Design.newBuilder()
            .setId(DESIGN_ID)
            .setTitle(DESIGN_TITLE)
            .setConversation(SESSION_ID)
            .setCreated(NOW - 600)
            .setOfOne(false)
            .setState("0 of 1 settled")
            .build()
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addDesigns(design).build())
        server.route(DESIGN_GET_PATH) { _ ->
            Reply.Message(
                DesignsGetResponse.newBuilder()
                    .setDesign(design)
                    .setStage(DesignStage.newBuilder().addQuestions(stored.get()))
                    .build(),
                0L,
            )
        }
        server.route(ANSWER_PATH) { request ->
            val sent = DesignsAnswerRequest.parseFrom(request.body)
            stored.set(settledBy(sent.answer, sent.accept))
            Reply.Message(DesignsAnswerResponse.newBuilder().setAnswer(stored.get().answer).build(), 0L)
        }
        server.respond(EPICS_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** The node tagged [body] holds a node tagged [tag] somewhere under it. */
    private fun assertBodyHolds(body: String, tag: String) {
        composeRule.onNode(hasTestTag(body) and hasAnyDescendant(hasTestTag(tag)), useUnmergedTree = true).assertExists()
    }

    /** Opens the design's page in the sheet, which waits for Designs.Get to answer and draw it. */
    private fun openDesignPage() {
        awaitTag(DESIGN_LINK_TAG)
        composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
        awaitTag("design-page")
    }

    /** Closes the phone sheet, which returns the conversation to the screen. */
    private fun closeSheet() {
        composeRule.onNodeWithTag(SHEET_CLOSE_TAG, useUnmergedTree = true).performClick()
        awaitTag(CONVERSATION_TAG)
    }

    /** The answers the screen has sent to Designs.Answer, in order. */
    private fun answersSent(server: FakeOrreryServer): List<DesignsAnswerRequest> =
        server.requests.filter { it.rawPath == ANSWER_PATH }.map { DesignsAnswerRequest.parseFrom(it.body) }

    /** The place [body] draws the question's thread, which is open: it has its own box and Accept. */
    private fun assertOpenThreadHasBoxAndAccept(body: String) {
        assertBodyHolds(body, THREAD_TAG)
        assertBodyHolds(THREAD_TAG, QUESTION_BOX_TAG)
        assertBodyHolds(THREAD_TAG, QUESTION_ACCEPT_TAG)
    }

    /** The settled thread the place draws shows [answer] and its maker, with no box, Send or Accept. */
    private fun assertSettledThread(answer: String) {
        composeRule.onNodeWithTag(QUESTION_ANSWER_TAG).assertTextEquals(answer)
        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assert(SemanticsMatcher.expectValue(MakerKey, "you"))
        composeRule.onAllNodesWithTag(QUESTION_BOX_TAG, useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodesWithTag(QUESTION_SEND_TAG, useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodesWithTag(QUESTION_ACCEPT_TAG, useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun theConversationDrawsTheDirectorsQuestionAsItsOwnThreadWithBoxAndAccept() {
        FakeOrreryServer().use { server ->
            serveSessionAndDesign(server)
            setSession(server)

            awaitTag(THREAD_TAG)

            assertOpenThreadHasBoxAndAccept(CONVERSATION_TAG)
            composeRule.onNodeWithTag(QUESTION_ASKED_TAG).assert(hasText(ASKED_IN_CONVERSATION))
        }
    }

    @Test
    fun theDesignPageDrawsTheSameQuestionAsItsOwnThreadWithBoxAndAccept() {
        FakeOrreryServer().use { server ->
            serveSessionAndDesign(server)
            setSession(server)

            openDesignPage()

            assertOpenThreadHasBoxAndAccept(DESIGN_BODY_TAG)
            composeRule.onNodeWithTag(QUESTION_ASKED_TAG).assert(hasText(ASKED_ON_DESIGN))
        }
    }

    @Test
    fun acceptInTheConversationSendsOneAnswerAndSettlesBothPlaces() {
        FakeOrreryServer().use { server ->
            serveSessionAndDesign(server)
            setSession(server)
            awaitTag(THREAD_TAG)

            composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).performScrollTo().performClick()
            awaitTag(QUESTION_ANSWER_TAG)

            val answers = answersSent(server)
            assertEquals(1, answers.size)
            assertEquals(DESIGN_ID, answers.single().design)
            assertEquals(QUESTION_NUMBER, answers.single().question)
            assertTrue(answers.single().accept)
            assertSettledThread(ACCEPTED)

            openDesignPage()

            assertEquals(1, answersSent(server).size)
            awaitTag(QUESTION_ANSWER_TAG)
            assertSettledThread(ACCEPTED)
            composeRule.onNodeWithTag(QUESTION_ASKED_TAG).assert(hasText(ASKED_ON_DESIGN))

            closeSheet()

            assertEquals(1, answersSent(server).size)
            assertSettledThread(ACCEPTED)
        }
    }

    @Test
    fun wordsTypedOnTheDesignPageSendOneAnswerAndSettleBothPlaces() {
        FakeOrreryServer().use { server ->
            serveSessionAndDesign(server)
            setSession(server)
            openDesignPage()
            awaitTag(QUESTION_BOX_TAG)

            composeRule.onNodeWithTag(QUESTION_BOX_TAG).performTextReplacement(TYPED)
            composeRule.onNodeWithTag(QUESTION_SEND_TAG).performScrollTo().performClick()
            awaitTag(QUESTION_ANSWER_TAG)

            val answers = answersSent(server)
            assertEquals(1, answers.size)
            assertEquals(DESIGN_ID, answers.single().design)
            assertEquals(QUESTION_NUMBER, answers.single().question)
            assertEquals(TYPED, answers.single().answer)
            assertFalse(answers.single().accept)
            assertSettledThread(TYPED)

            closeSheet()

            assertEquals(1, answersSent(server).size)
            awaitTag(QUESTION_ANSWER_TAG)
            assertSettledThread(TYPED)
        }
    }

    /** A design item of the transcript, its record saying [what] about [ids]. */
    private fun designItem(what: String, ids: List<String>) = ConversationItem(
        kind = "design",
        time = ASKED,
        text = "",
        tool = "",
        input = "",
        id = "",
        error = false,
        design = DESIGN_ID,
        designWhat = what,
        designIds = ids,
    )

    @Test
    fun anAskedDesignItemBecomesADesignQuestionEntryAndOtherDesignItemsDrawNothing() {
        val entries = foldConversation(
            listOf(
                designItem(what = "asked", ids = listOf(QUESTION_KEY)),
                designItem(what = "answered", ids = listOf(QUESTION_KEY)),
                designItem(what = "asked", ids = listOf("example-repo/write-first")),
            ),
            now = ASKED,
            zone = ZoneId.of("UTC"),
        )

        assertEquals(listOf(ConvoEntry.DesignQuestion(DESIGN_ID, QUESTION_KEY, ASKED)), entries)
        assertEquals(listOf("design-question:$DESIGN_ID:$QUESTION_KEY"), entryKeys(entries))
    }
}
