package io.github.joshsymonds.orrery.ui

/** A tool in plain words, and whether it is a wait on background work. */
data class ToolWords(val text: String, val background: Boolean)

private val waiting = ToolWords("waiting on background work", background = true)

private val wordsByShortName: Map<String, ToolWords> = mapOf(
    "Bash" to ToolWords("running a command", background = false),
    "Read" to ToolWords("reading a file", background = false),
    "Write" to ToolWords("writing a file", background = false),
    "Edit" to ToolWords("editing a file", background = false),
    "MultiEdit" to ToolWords("editing a file", background = false),
    "NotebookEdit" to ToolWords("editing a file", background = false),
    "Grep" to ToolWords("searching code", background = false),
    "Glob" to ToolWords("finding files", background = false),
    "WebFetch" to ToolWords("reading a web page", background = false),
    "WebSearch" to ToolWords("searching the web", background = false),
    "Agent" to ToolWords("running an agent", background = false),
    "Task" to ToolWords("running an agent", background = false),
    "TodoWrite" to ToolWords("updating its plan", background = false),
    "Skill" to ToolWords("loading a skill", background = false),
    "ToolSearch" to ToolWords("loading tools", background = false),
    "AskUserQuestion" to ToolWords("asking you", background = false),
    "step_wait" to waiting,
    "Monitor" to waiting,
)

/**
 * The tool named in plain words. An MCP name such as `mcp__orrery__step_wait`
 * is read by its short name, the part after its last `__`; a tool not in the
 * table answers its short name.
 */
fun toolWords(name: String): ToolWords {
    val shortName = name.substringAfterLast("__")
    return wordsByShortName[shortName] ?: ToolWords(shortName, background = false)
}
