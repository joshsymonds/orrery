package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

private const val NOW = 1_700_000_000L
private const val PANE_ID = "%3"
private const val QUESTION_HASH = "hash-1"

private fun pane(
    question: String = "",
    id: String = PANE_ID,
    state: String = "waiting",
    orreryId: String = "orrery-1",
    headless: Boolean = false,
    putOff: Boolean = false,
): Pane = Pane(
    id = id,
    host = "example-host",
    session = "example-session",
    window = 1,
    pane = 2,
    cwd = "/home/user/project",
    command = "claude",
    state = state,
    summary = "",
    label = "example-label",
    changed = NOW - 240,
    seen = 0L,
    question = question,
    questionHash = QUESTION_HASH,
    headless = headless,
    orreryId = orreryId,
    putOff = putOff,
)

private fun permission(tool: String, input: String): String =
    """{"tool":"$tool","input":{$input}}"""

private val permissionAnswers = listOf(
    AskOption(1, "Yes", "", recommended = false, why = "", answerable = true),
    AskOption(
        2,
        "Yes, and don't ask again",
        "for commands like this one in this session",
        recommended = false,
        why = "",
        answerable = true,
    ),
    AskOption(3, "No", "and say what to do instead, below", recommended = false, why = "", answerable = true),
)

private val epic = EpicRow(
    id = "repo-a/alpha",
    repository = "repo-a",
    slug = "alpha",
    outcome = "",
    changed = 0L,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
    path = "/home/user/repo-a",
    phaseAt = NOW - 500,
)

class AskStatesModelTest {
    @Test
    fun askUserQuestionDrawsItsOptionsAndQuestionOfTwo() {
        val stored = """
            {"tool":"AskUserQuestion","questions":[
             {"question":"Pick a synthetic option?","options":[
               {"label":"Synthetic one","description":"First choice."},
               {"label":"Synthetic two","description":""}]},
             {"question":"Second?","options":[{"label":"x","description":""}]}]}
        """.trimIndent()

        val model = paneAsk(pane(stored))!!

        assertEquals(AskKind.PANE, model.kind)
        assertEquals("project · example-label · question 1 of 2", model.who)
        assertEquals("Pick a synthetic option?", model.question)
        assertEquals(
            listOf(
                AskOption(1, "Synthetic one", "First choice.", recommended = false, why = "", answerable = true),
                AskOption(2, "Synthetic two", "", recommended = false, why = "", answerable = true),
            ),
            model.options,
        )
        assertEquals(PaneTarget(PANE_ID, QUESTION_HASH, NOW - 240), model.target)
        assertEquals(NOW - 240, model.asked)
    }

    @Test
    fun singleAskUserQuestionHasNoQuestionCount() {
        val options = (1..10).joinToString(",") { """{"label":"Option $it","description":""}""" }
        val stored = """{"tool":"AskUserQuestion","questions":[{"question":"Pick one?","options":[$options]}]}"""

        val model = paneAsk(pane(stored))!!

        assertEquals("project · example-label", model.who)
        assertEquals(10, model.options.size)
        assertTrue(model.options[8].answerable)
        assertFalse(model.options[9].answerable)
    }

    @Test
    fun bashPermissionDrawsItsCommandAndThreeAnswers() {
        val model = paneAsk(pane(permission("Bash", """"command":"echo synthetic"""")))!!

        assertEquals(AskKind.PERMISSION, model.kind)
        assertEquals("Run this command?", model.question)
        assertEquals(AskCommand("Bash", "echo synthetic"), model.command)
        assertEquals(permissionAnswers, model.options)
        assertEquals(PaneTarget(PANE_ID, QUESTION_HASH, NOW - 240), model.target)
    }

    @Test
    fun otherToolPermissionAsksToUseTheTool() {
        val model = paneAsk(pane(permission("Read", """"file_path":"/home/user/notes.md"""")))!!

        assertEquals("Use Read?", model.question)
        assertEquals(AskCommand("Read", "/home/user/notes.md"), model.command)
        assertEquals(permissionAnswers, model.options)
    }

    @Test
    fun plainTextPaneQuestionDrawsThreeAnswers() {
        val model = paneAsk(pane("Synthetic plain question?"))!!

        assertEquals(AskKind.PERMISSION, model.kind)
        assertEquals("Synthetic plain question?", model.question)
        assertNull(model.command)
        assertEquals(permissionAnswers, model.options)
    }

    @Test
    fun paneWithoutAWaitingQuestionHasNoAsk() {
        assertNull(paneAsk(pane(permission("Bash", """"command":"echo synthetic""""), state = "idle")))
        assertNull(paneAsk(pane("")))
    }

    @Test
    fun paneAskTakesPutOffFromThePane() {
        assertTrue(paneAsk(pane("Synthetic plain question?", putOff = true))!!.putOff)
        assertFalse(paneAsk(pane("Synthetic plain question?"))!!.putOff)
    }

    @Test
    fun tmuxPaneArchivesByOrreryIdAndHasNoArchiveWithout() {
        assertEquals(
            Pair("session", "orrery-1"),
            paneAsk(pane("Synthetic plain question?"))!!.archive,
        )
        assertNull(paneAsk(pane("Synthetic plain question?", orreryId = ""))!!.archive)
    }

    @Test
    fun sessionPermissionDrawsItsServedOptions() {
        val request = SessionRequest(
            id = "req-7",
            kind = "permission",
            question = "Bash",
            options = listOf("allow", "deny"),
        )
        val session = pane(id = "session-9", headless = true)

        val model = sessionAsk(session, request)

        assertEquals(AskKind.PERMISSION, model.kind)
        assertEquals("project · example-label", model.who)
        assertEquals("Use Bash?", model.question)
        assertNull(model.command)
        assertEquals(
            listOf(
                AskOption(1, "allow", "", recommended = false, why = "", answerable = true),
                AskOption(2, "deny", "", recommended = false, why = "", answerable = true),
            ),
            model.options,
        )
        assertEquals(HeadlessTarget("session-9", "req-7", listOf("allow", "deny")), model.target)
        assertEquals(Pair("headless", "session-9"), model.archive)
    }

    @Test
    fun sessionQuestionDrawsItsOwnWords() {
        val request = SessionRequest(
            id = "req-8",
            kind = "question",
            question = "Synthetic question?",
            options = listOf("Only"),
        )

        val model = sessionAsk(pane(id = "session-9", headless = true), request)

        assertEquals(AskKind.SESSION, model.kind)
        assertEquals("Synthetic question?", model.question)
    }

    @Test
    fun unanswerableRequestHasNoOptions() {
        val request = SessionRequest(
            id = "req-9",
            kind = "question",
            question = "Synthetic question?",
            options = emptyList(),
        )

        val model = sessionAsk(pane(id = "session-9", headless = true), request)

        assertTrue(model.options.isEmpty())
        assertEquals(HeadlessTarget("session-9", "req-9", emptyList()), model.target)
    }

    @Test
    fun sessionAskTakesPutOffFromTheRequestOrThePane() {
        val request = SessionRequest(
            id = "req-7",
            kind = "question",
            question = "Q?",
            options = listOf("a"),
            putOff = true,
        )
        assertTrue(sessionAsk(pane(id = "session-9", headless = true), request).putOff)

        val notPutOff = request.copy(putOff = false)
        assertTrue(sessionAsk(pane(id = "session-9", headless = true, putOff = true), notPutOff).putOff)
        assertFalse(sessionAsk(pane(id = "session-9", headless = true), notPutOff).putOff)
    }

    @Test
    fun epicQuestionDrawsChoicesWithItsRecommendation() {
        val question = EpicQuestionRow(
            id = "q-1",
            text = "Which path?",
            turn = 1L,
            asked = NOW - 3_600,
            choices = listOf(
                EpicQuestionChoiceRow("a", "Take path a."),
                EpicQuestionChoiceRow("b", "Take path b."),
            ),
            recommendedChoice = "b",
            recommendedWhy = "It is smaller.",
        )

        val model = epicAsk(epic, question)

        assertEquals(AskKind.EPIC, model.kind)
        assertEquals("repo-a · alpha", model.who)
        assertEquals("Which path?", model.question)
        assertEquals(NOW - 3_600, model.asked)
        assertEquals(
            listOf(
                AskOption(1, "a", "Take path a.", recommended = false, why = "", answerable = true),
                AskOption(2, "b", "Take path b.", recommended = true, why = "It is smaller.", answerable = true),
            ),
            model.options,
        )
        assertFalse(model.putOff)
        assertNull(model.archive)
        assertNull(model.target)
        assertEquals("repo-a/alpha", model.epicId)
        assertEquals("q-1", model.questionId)
    }

    @Test
    fun parkDrawsItsReasonWithRetry() {
        val model = parkAsk(epic, "Tests failed.")

        assertEquals(AskKind.PARK, model.kind)
        assertEquals("Tests failed.", model.question)
        assertEquals(NOW - 500, model.asked)
        assertEquals(
            listOf(
                AskOption(1, "Retry", "Run the parked work again", recommended = false, why = "", answerable = true),
            ),
            model.options,
        )
        assertEquals("repo-a/alpha", model.epicId)
        assertEquals("", model.questionId)
    }

    @Test
    fun staleWordsNamesOnlyWaitsOfADayOrMore() {
        assertNull(staleWords(NOW - (23 * 3_600 + 59 * 60), NOW))
        assertEquals("1 day", staleWords(NOW - 86_400, NOW))
        assertEquals("5 days", staleWords(NOW - 5 * 86_400, NOW))
    }
}
