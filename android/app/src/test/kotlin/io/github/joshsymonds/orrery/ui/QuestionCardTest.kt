package io.github.joshsymonds.orrery.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

// The stored question's reader, which the Ask's words are read from: the title, the options with their labels and
// descriptions, and whether the title and options are the assistant's markdown.
class QuestionCardTest {
    private val toolQuestion = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""

    private val askQuestion = """
        {"tool":"AskUserQuestion","questions":[{"question":"Pick a synthetic option?",
         "header":"Pick","options":[{"label":"Synthetic one","description":"d"},
         {"label":"Synthetic two","description":"d"}],"multiSelect":false}]}
    """.trimIndent()

    private val bareQuestion = """
        {"tool":"AskUserQuestion","questions":[{"question":"Pick a bare option?",
         "header":"Pick","options":[{"label":"Bare one"},
         {"label":"Bare two","description":"Synthetic detail"}],"multiSelect":false}]}
    """.trimIndent()

    @Test
    fun toolQuestionTitleIsTheCommandWithNoParsedOptions() {
        val parsed = parseQuestion(toolQuestion)

        assertEquals("Bash: echo synthetic", parsed.title)
        assertEquals(emptyList<QuestionOption>(), parsed.options)
        assertFalse(parsed.markdown)
    }

    @Test
    fun askUserQuestionTitleIsItsMarkdownAndOptionsAreTheirLabels() {
        val parsed = parseQuestion(askQuestion)

        assertEquals("Pick a synthetic option?", parsed.title)
        assertTrue(parsed.markdown)
        assertEquals(listOf("Synthetic one", "Synthetic two"), parsed.options.map { it.label })
    }

    @Test
    fun optionDescriptionIsKeptWhenThereIsOneAndNullWhenNot() {
        val described = parseQuestion(askQuestion)
        val bare = parseQuestion(bareQuestion)

        assertEquals(listOf("d", "d"), described.options.map { it.description })
        assertEquals(listOf(null, "Synthetic detail"), bare.options.map { it.description })
    }

    @Test
    fun truncatedMarkerTitleIsTheTool() {
        val parsed = parseQuestion("""{"tool":"Bash","truncated":true,"sha256":"synthetic"}""")

        assertEquals("Bash", parsed.title)
        assertEquals(emptyList<QuestionOption>(), parsed.options)
    }

    @Test
    fun plainTextIsItsOwnTitleWithNoParsedOptions() {
        val parsed = parseQuestion("Plain synthetic question?")

        assertEquals("Plain synthetic question?", parsed.title)
        assertEquals(emptyList<QuestionOption>(), parsed.options)
        assertFalse(parsed.markdown)
    }
}
