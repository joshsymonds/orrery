package io.github.joshsymonds.orrery.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class ToolWordsTest {
    @Test
    fun everyTableRowReadsInPlainWords() {
        val rows = listOf(
            "Bash" to "running a command",
            "Read" to "reading a file",
            "Write" to "writing a file",
            "Edit" to "editing a file",
            "MultiEdit" to "editing a file",
            "NotebookEdit" to "editing a file",
            "Grep" to "searching code",
            "Glob" to "finding files",
            "WebFetch" to "reading a web page",
            "WebSearch" to "searching the web",
            "Agent" to "running an agent",
            "Task" to "running an agent",
            "TodoWrite" to "updating its plan",
            "Skill" to "loading a skill",
            "ToolSearch" to "loading tools",
            "AskUserQuestion" to "asking you",
        )
        for ((name, text) in rows) {
            assertEquals(name, ToolWords(text, background = false), toolWords(name))
        }
    }

    @Test
    fun mcpNameReadsAsItsShortNameRow() {
        assertEquals(ToolWords("reading a file", background = false), toolWords("mcp__orrery__Read"))
    }

    @Test
    fun backgroundWaitsReadApartFromActiveWork() {
        val waits = listOf("step_wait", "Monitor", "mcp__orrery__step_wait", "mcp__orrery__Monitor")
        for (name in waits) {
            assertEquals(name, ToolWords("waiting on background work", background = true), toolWords(name))
        }
    }

    @Test
    fun unknownMcpToolAnswersItsShortName() {
        assertEquals(ToolWords("frobnicate", background = false), toolWords("mcp__other__frobnicate"))
    }

    @Test
    fun unknownPlainToolAnswersItsName() {
        assertEquals(ToolWords("Frobnicate", background = false), toolWords("Frobnicate"))
    }
}
