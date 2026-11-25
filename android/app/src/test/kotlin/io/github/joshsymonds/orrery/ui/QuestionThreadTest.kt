package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.TimeZone
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A question's thread as round 8 draws it: its options with the recommendation marked, its own box, Send and Accept while it
// is open, and once settled the answer with the maker that settled it and no box or Accept. The asked clock is read in UTC.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuestionThreadTest {
    private companion object {
        const val ASKED = 1_700_000_000L
        const val ASKED_CLOCK = "22:13"
        const val QUESTION_KEY = "Q7"
        const val QUESTION = "Which way should the layout go?"
        const val KEEP = "keep the current layout"
        const val SPLIT = "split the screen in two"
        const val TYPED = "go with both"
        const val REFUSED = "the server refused the answer"
        const val QUOTE = "use the split for now"
        const val REASON = "it reads better on a phone"
        const val DELEGATION = "decide the layout yourself"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()
    private val answers = mutableListOf<String>()
    private var accepts = 0
    private val sends = SendState()

    private class SendState {
        var value: ThreadSend by mutableStateOf(ThreadSend.Idle)
    }

    @Before
    fun useUtc() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    private fun question(
        recommendation: String = SPLIT,
        answer: String = "",
        accepted: Boolean = false,
        maker: RecordMakerRow? = null,
    ): DesignQuestionRow = DesignQuestionRow(
        design = "example-design",
        id = 7L,
        text = QUESTION,
        options = listOf(KEEP, SPLIT),
        recommendation = recommendation,
        asked = ASKED,
        answer = answer,
        accepted = accepted,
        maker = maker,
        answered = if (answer.isEmpty()) 0L else ASKED + 60L,
    )

    private fun setThread(question: DesignQuestionRow, place: ThreadPlace = ThreadPlace.CONVERSATION) {
        composeRule.setContent {
            MaterialTheme {
                QuestionThread(
                    question = question,
                    place = place,
                    send = sends.value,
                    onAnswer = { answers.add(it) },
                    onAccept = { accepts++ },
                )
            }
        }
    }

    private fun setSend(send: ThreadSend) {
        composeRule.runOnIdle { sends.value = send }
        composeRule.waitForIdle()
    }

    @Test
    fun anOpenQuestionDrawsItsIdTextAndEachOptionWithTheRecommendationMarked() {
        setThread(question())

        composeRule.onNodeWithTag(QUESTION_THREAD_TAG_PREFIX + QUESTION_KEY).assertExists()
        composeRule.onNodeWithText(QUESTION_KEY).assertExists()
        composeRule.onNodeWithText(QUESTION).assertExists()
        composeRule.onNodeWithTag(QUESTION_OPTION_TAG_PREFIX + 0).assertTextEquals(KEEP)
        composeRule.onNodeWithTag(QUESTION_OPTION_TAG_PREFIX + 1).assertTextEquals(SPLIT, "recommended")
    }

    @Test
    fun aQuestionWithNoRecommendationMarksNoOptionAndDrawsNoAccept() {
        setThread(question(recommendation = ""))

        composeRule.onNodeWithTag(QUESTION_OPTION_TAG_PREFIX + 0).assertTextEquals(KEEP)
        composeRule.onNodeWithTag(QUESTION_OPTION_TAG_PREFIX + 1).assertTextEquals(SPLIT)
        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).assertDoesNotExist()
    }

    @Test
    fun anOpenQuestionDrawsItsOwnBoxSendAndAcceptWithTheRecommendationNamed() {
        setThread(question())

        composeRule.onNodeWithTag(QUESTION_BOX_TAG).assertIsEnabled()
        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).assertIsEnabled().assertTextEquals("Accept: $SPLIT")
    }

    @Test
    fun theThreadSaysTheDirectorAskedHereAndWhen() {
        setThread(question(), ThreadPlace.CONVERSATION)

        composeRule.onNodeWithTag(QUESTION_ASKED_TAG).assertTextEquals("the Director", "asked here", "· $ASKED_CLOCK")
    }

    @Test
    fun onTheDesignTheThreadSaysTheDirectorAskedInTheConversation() {
        setThread(question(), ThreadPlace.DESIGN)

        composeRule.onNodeWithTag(QUESTION_ASKED_TAG).assertTextEquals("the Director", "asked in the conversation", "· $ASKED_CLOCK")
    }

    @Test
    fun typedWordsAnswerOnceWithThoseWordsTrimmed() {
        setThread(question())

        composeRule.onNodeWithTag(QUESTION_BOX_TAG).performTextReplacement("  $TYPED  ")
        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertIsEnabled().performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(TYPED), answers)
        assertEquals(0, accepts)
    }

    @Test
    fun sendStaysDisabledUntilWordsAreTyped() {
        setThread(question())

        composeRule.onNodeWithTag(QUESTION_BOX_TAG).performTextReplacement("   ")

        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertIsNotEnabled()
        assertEquals(emptyList<String>(), answers)
    }

    @Test
    fun acceptTakesTheRecommendationOnceAndAnswersNothing() {
        setThread(question())

        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(1, accepts)
        assertEquals(emptyList<String>(), answers)
    }

    @Test
    fun whileSendingTheBoxSendAndAcceptAreDisabledAndSendingShows() {
        setThread(question())
        composeRule.onNodeWithTag(QUESTION_BOX_TAG).performTextReplacement(TYPED)

        setSend(ThreadSend.Sending)

        composeRule.onNodeWithTag(QUESTION_BOX_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(QUESTION_SENDING_TAG).assertTextEquals("sending…")
    }

    @Test
    fun aFailedSendShowsItsReasonAndEnablesThemAgainWithTheWordsKept() {
        setThread(question())
        composeRule.onNodeWithTag(QUESTION_BOX_TAG).performTextReplacement(TYPED)

        setSend(ThreadSend.Failed(REFUSED))

        composeRule.onNodeWithTag(QUESTION_ERROR_TAG).assertTextEquals(REFUSED)
        composeRule.onNodeWithTag(QUESTION_BOX_TAG).assertIsEnabled()
        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertIsEnabled().performClick()
        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).assertIsEnabled()
        composeRule.waitForIdle()
        assertEquals(listOf(TYPED), answers)
    }

    @Test
    fun aSettledQuestionAcceptedShowsTheRecommendationAcceptedAndNoBoxOrAccept() {
        setThread(question(answer = SPLIT, accepted = true, maker = RecordMakerRow(kind = "owner_serve")))

        composeRule.onNodeWithTag(QUESTION_ANSWER_TAG).assertTextEquals("accepted the recommendation")
        composeRule.onNodeWithTag(QUESTION_BOX_TAG).assertDoesNotExist()
        composeRule.onNodeWithTag(QUESTION_SEND_TAG).assertDoesNotExist()
        composeRule.onNodeWithTag(QUESTION_ACCEPT_TAG).assertDoesNotExist()
    }

    @Test
    fun aSettledQuestionAnsweredInWordsShowsThoseWords() {
        setThread(question(answer = TYPED, maker = RecordMakerRow(kind = "owner_serve")))

        composeRule.onNodeWithTag(QUESTION_ANSWER_TAG).assertTextEquals(TYPED)
    }

    @Test
    fun anAnswerGivenThroughServeIsMadeByYouHere() {
        setThread(question(answer = TYPED, maker = RecordMakerRow(kind = "owner_serve")))

        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assert(SemanticsMatcher.expectValue(MakerKey, "you"))
        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assertTextEquals("you", "here")
    }

    @Test
    fun anAnswerGivenInTheConversationIsMadeByYouWithTheQuote() {
        setThread(question(answer = TYPED, maker = RecordMakerRow(kind = "owner_conversation", quote = QUOTE)))

        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assert(SemanticsMatcher.expectValue(MakerKey, "you"))
        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assertTextEquals("you", "in the conversation · “$QUOTE”")
    }

    @Test
    fun aDirectorsAnswerIsMadeByTheDirectorForYouWithItsReasonAndDelegation() {
        setThread(
            question(
                answer = TYPED,
                maker = RecordMakerRow(kind = "conversation", reason = REASON, delegation = DELEGATION),
            ),
        )

        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assert(SemanticsMatcher.expectValue(MakerKey, "director"))
        composeRule.onNodeWithTag(QUESTION_MAKER_TAG).assertTextEquals(
            "the Director",
            "for you · $REASON · under your words “$DELEGATION”",
        )
    }
}
