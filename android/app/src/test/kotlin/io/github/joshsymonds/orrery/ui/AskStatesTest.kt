package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The Ask in each of its states, drawn from the Ask models for a session's request, a tool permission, a pane's
// question and an epic's question and park. Checked through the node tree and the test tags, against a synthetic
// clock: NOW is the instant every age is measured from.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskStatesTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val DAY = 86_400L
        const val SESSION_ID = "00000000000000000000000000000005"
        const val PANE_ID = "%3"
        const val EPIC_ID = "example-repo/example-epic"
        const val BASH_COMMAND = "ls -la"
        const val SESSION_QUESTION = "Pick a stack?"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val answers = mutableListOf<Int>()
    private var notNows = 0
    private var bringBacks = 0
    private var declines = 0
    private var archives = 0
    private val confirms = mutableListOf<Pair<String, String>>()

    private fun pane(
        question: String = "",
        changed: Long = NOW - 60,
        putOff: Boolean = false,
        orreryId: String = "",
    ) = Pane(
        id = PANE_ID,
        host = "example-host",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = "/home/user/alpha",
        command = "claude",
        state = "waiting",
        summary = "",
        label = "label-alpha",
        changed = changed,
        seen = 0L,
        question = question,
        questionHash = "hash-1",
        orreryId = orreryId,
        putOff = putOff,
    )

    private fun askUserQuestion(options: String, count: Int = 1): String {
        val questions = List(count) { """{"question":"Which?","options":[$options]}""" }
        return """{"questions":[${questions.joinToString(",")}]}"""
    }

    private val twoOptions = """
        {"label":"Alpha","description":"the first one"},
        {"label":"Beta","description":"the second one"}
    """.trimIndent()

    private fun epicOf(): EpicRow = EpicRow(
        id = EPIC_ID,
        repository = "example-repo",
        slug = "example-epic",
        outcome = "",
        changed = NOW - 30,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        path = "/home/user/example-repo",
    )

    private fun epicQuestion(): EpicQuestionRow = EpicQuestionRow(
        id = "question-1",
        text = "Which way?",
        turn = 1L,
        asked = NOW - 120,
        choices = listOf(
            EpicQuestionChoiceRow("keep", "keep the current layout"),
            EpicQuestionChoiceRow("split", "split the screen in two"),
        ),
        recommendedChoice = "split",
        recommendedWhy = "it reads better on a phone",
    )

    private fun setAsk(
        model: AskModel,
        phase: AskPhase = AskPhase.OPEN,
        picked: Int? = null,
        note: String = "",
        onConfirm: (suspend (String, String) -> String)? = null,
    ) {
        composeRule.setContent {
            Ask(
                model = model,
                now = NOW,
                phase = phase,
                picked = picked,
                note = note,
                onAnswer = { answers += it },
                onNotNow = { notNows++ },
                onBringBack = { bringBacks++ },
                onDecline = { declines++ },
                onArchive = { archives++ },
                onConfirm = onConfirm,
            )
        }
    }

    private fun sessionPermission(): AskModel = sessionAsk(
        pane(changed = NOW - 60),
        SessionRequest(SESSION_ID, "permission", "Edit", listOf("allow", "deny")),
    )

    @Test
    fun sessionPermissionDrawsItsLabelWhoAgeAndQuestion() {
        val model = sessionPermission()
        setAsk(model)

        composeRule.onNodeWithTag("ask").assertExists()
        composeRule.onNodeWithTag("ask-label").assertTextEquals("ASKS YOU")
        composeRule.onNodeWithTag("ask-who").assertTextEquals(model.who)
        composeRule.onNodeWithTag("ask-age").assertTextEquals("· 1m ago")
        composeRule.onNodeWithTag("ask-question").assertTextEquals("Use Edit?")
        composeRule.onNodeWithText("permission").assertExists()
        composeRule.onNodeWithTag("ask-option:1").assertTextContains("allow", substring = true)
        composeRule.onNodeWithTag("ask-option:2").assertTextContains("deny", substring = true)
    }

    @Test
    fun tappingAnOptionAnswersOnceWithItsNumber() {
        setAsk(sessionPermission())

        composeRule.onNodeWithTag("ask-option:2").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(2), answers)
    }

    @Test
    fun whileSendingFurtherTapsSendNothing() {
        setAsk(sessionPermission(), phase = AskPhase.SENDING, picked = 1)

        composeRule.onNodeWithTag("ask-sending").assertExists()
        composeRule.onNodeWithTag("ask-option:1").assertIsSelected()
        composeRule.onNodeWithTag("ask-option:2").performClick()
        composeRule.waitForIdle()

        assertEquals(emptyList<Int>(), answers)
        composeRule.onNodeWithTag("ask-not-now").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-more").assertDoesNotExist()
    }

    @Test
    fun notNowTakesAnOpenAskOffTheList() {
        setAsk(sessionPermission())

        composeRule.onNodeWithTag("ask-label").assertTextEquals("ASKS YOU")
        composeRule.onNodeWithTag("ask-bring-back").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-not-now").performClick()
        composeRule.waitForIdle()

        assertEquals(1, notNows)
    }

    @Test
    fun bringBackReplacesNotNowOnAPutOffAsk() {
        setAsk(
            sessionAsk(
                pane(putOff = true),
                SessionRequest(SESSION_ID, "permission", "Edit", listOf("allow", "deny")),
            ),
        )

        composeRule.onNodeWithTag("ask-label").assertTextEquals("PUT OFF")
        composeRule.onNodeWithTag("ask-not-now").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-bring-back").performClick()
        composeRule.waitForIdle()

        assertEquals(1, bringBacks)
    }

    @Test
    fun declineIsReachableOnlyAfterMoreOpensItsSheet() {
        setAsk(sessionPermission())

        composeRule.onNodeWithTag("ask-decline").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-more").performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag("ask-decline").assertTextContains("Decline and end the turn", substring = true)
        composeRule.onNodeWithTag("ask-decline", useUnmergedTree = true)
            .performSemanticsAction(SemanticsActions.OnClick)
        composeRule.waitForIdle()

        assertEquals(1, declines)
        assertEquals(emptyList<Int>(), answers)
    }

    @Test
    fun paneQuestionDrawsItsCountAndNumberedOptions() {
        val model = paneAsk(pane(question = askUserQuestion(twoOptions, count = 2)))!!
        setAsk(model)

        composeRule.onNodeWithTag("ask-who").assertTextContains("question 1 of 2", substring = true)
        composeRule.onNodeWithTag("ask-option:1").assertTextContains("Alpha", substring = true)
        composeRule.onNodeWithTag("ask-option:1").assertTextContains("the first one", substring = true)
        composeRule.onNodeWithTag("ask-option:2").assertTextContains("Beta", substring = true)
        composeRule.onNodeWithTag("ask-option:2").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(2), answers)
    }

    @Test
    fun bashPermissionDrawsItsCommandBlock() {
        val model = paneAsk(pane(question = """{"tool":"Bash","input":{"command":"$BASH_COMMAND"}}"""))!!
        setAsk(model)

        composeRule.onNodeWithTag("ask-question").assertTextEquals("Run this command?")
        composeRule.onNodeWithText("Bash").assertExists()
        composeRule.onNodeWithText(BASH_COMMAND).assertExists()
        composeRule.onNodeWithTag("ask-option:3").assertTextContains("No", substring = true)
    }

    @Test
    fun unanswerableQuestionPointsAtTheInputAndDrawsNoOption() {
        val model = paneAsk(pane(question = askUserQuestion("")))!!
        setAsk(model)

        composeRule.onNodeWithTag("ask-unanswerable").assertTextEquals(
            "This question cannot be answered from here. Reply in words below.",
        )
        composeRule.onNodeWithTag("ask-option:1").assertDoesNotExist()
    }

    @Test
    fun staleQuestionDrawsItsAgeAndArchivesThisSession() {
        val model = sessionAsk(
            pane(changed = NOW - 5 * DAY, orreryId = "orrery-1"),
            SessionRequest(SESSION_ID, "question", SESSION_QUESTION, listOf("Kotlin", "Swift")),
        )
        setAsk(model)

        composeRule.onNodeWithTag("ask-stale").assertTextEquals("Asked 5 days ago and still waiting.")
        composeRule.onNodeWithTag("ask-archive").performClick()
        composeRule.waitForIdle()

        assertEquals(1, archives)
    }

    @Test
    fun putOffQuestionDrawsNoStaleNote() {
        val model = sessionAsk(
            pane(changed = NOW - 5 * DAY, putOff = true, orreryId = "orrery-1"),
            SessionRequest(SESSION_ID, "question", SESSION_QUESTION, listOf("Kotlin", "Swift")),
        )
        setAsk(model)

        composeRule.onNodeWithTag("ask-stale").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-archive").assertDoesNotExist()
    }

    @Test
    fun refusedAnswerDrawsItsNote() {
        setAsk(sessionPermission(), note = "Not answered: the question changed before your answer arrived.")

        composeRule.onNodeWithTag("ask-note").assertTextContains("Not answered: the question changed", substring = true)
    }

    @Test
    fun epicQuestionMarksItsRecommendationAndAnswersOnlyOnConfirm() {
        val model = epicAsk(epicOf(), epicQuestion())
        setAsk(model, onConfirm = { choice, text ->
            confirms += choice to text
            ""
        })

        composeRule.onNodeWithTag("ask-who").assertTextEquals("example-repo · example-epic")
        composeRule.onNodeWithTag("ask-recommended:2", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("ask-option:2").assertTextContains("it reads better on a phone", substring = true)
        composeRule.onNodeWithTag("ask-not-now").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-more").assertDoesNotExist()

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.waitForIdle()
        assertEquals(emptyList<Pair<String, String>>(), confirms)

        composeRule.onNodeWithTag("ask-confirm").assertTextEquals("Answer with keep")
        composeRule.onNodeWithTag("ask-record").assertTextContains("owner: keep the current layout", substring = true)
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf("keep" to "keep the current layout"), confirms)
        assertEquals(emptyList<Int>(), answers)
    }

    @Test
    fun epicSomethingElseSendsTheTypedText() {
        setAsk(epicAsk(epicOf(), epicQuestion()), onConfirm = { choice, text ->
            confirms += choice to text
            ""
        })

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-other-text").performTextInput("  go with both  ")
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf("other" to "go with both"), confirms)
    }

    @Test
    fun epicConfirmStaysDisabledUntilSomethingElseHasText() {
        setAsk(epicAsk(epicOf(), epicQuestion()), onConfirm = { _, _ -> "" })

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-confirm").assertIsNotEnabled()
        composeRule.onNodeWithTag("ask-other-text").performTextInput("x")
        composeRule.onNodeWithTag("ask-confirm").assertIsEnabled()
    }

    @Test
    fun epicErrorStaysOnTheAskWithItsWords() {
        setAsk(epicAsk(epicOf(), epicQuestion()), onConfirm = { _, _ -> "Not answered. Your answer is kept." })

        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("ask-note").assertTextContains("Not answered. Your answer is kept.", substring = true)
        composeRule.onNodeWithTag("ask-confirm").assertExists()
    }

    @Test
    fun parkDrawsItsReasonWithOnlyRetry() {
        setAsk(parkAsk(epicOf(), "the tests failed"), onConfirm = { choice, text ->
            confirms += choice to text
            ""
        })

        composeRule.onNodeWithTag("ask-question").assertTextEquals("the tests failed")
        composeRule.onNodeWithTag("ask-option:1").assertTextContains("Retry", substring = true)
        composeRule.onNodeWithTag("ask-other").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-record").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf("Retry" to "Run the parked work again"), confirms)
    }

    @Test
    fun answeredLineNamesTheOptionAndTheQuestion() {
        composeRule.setContent {
            AskAnsweredLine(n = 2, name = "Beta", question = "Which?")
        }

        composeRule.onNodeWithTag("ask-answered").assertExists()
        composeRule.onNode(hasText("You answered", ignoreCase = true) and hasAnyAncestor(hasTestTag("ask-answered")))
            .assertExists()
        composeRule.onNode(hasText("Beta") and hasAnyAncestor(hasTestTag("ask-answered"))).assertExists()
        composeRule.onNode(hasText("· Which?") and hasAnyAncestor(hasTestTag("ask-answered"))).assertExists()
    }

    @Test
    fun settledLineNamesTheEpic() {
        composeRule.setContent {
            AskSettledLine(who = "example-repo · example-epic")
        }

        composeRule.onNodeWithTag("ask-settled").assertExists()
        composeRule.onNode(hasText("This question is settled.") and hasAnyAncestor(hasTestTag("ask-settled")))
            .assertExists()
        composeRule.onNode(hasText("· example-repo · example-epic") and hasAnyAncestor(hasTestTag("ask-settled")))
            .assertExists()
    }
}
