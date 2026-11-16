package io.github.joshsymonds.orrery

import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.File

/** Compose test rule factories that run composition on the default UnconfinedTestDispatcher. */
private val DEFAULT_RULES = Regex("""\b(createComposeRule|createAndroidComposeRule|createEmptyComposeRule)\b""")

/**
 * Every Compose test builds its rule through ComposeRules.kt, whose rules keep composition and its coroutines on
 * the main thread. The factories' default UnconfinedTestDispatcher resumes a coroutine on whatever thread finished
 * its wait, so app code after an IO call ran beside the main thread's layout: the crashes in
 * MainActivityInsetsTest and PoolsScreenTest, and ConversationView's scrolls judged by an order the app never sees.
 */
class ComposeRulesUseTest {
    @Test
    fun composeTestsMakeTheirRulesOnlyThroughComposeRules() {
        val sources = File("src/test/kotlin").walk().filter { it.isFile && it.extension == "kt" }.toList()
        check(sources.isNotEmpty()) { "no test sources under ${File("src/test/kotlin").absolutePath}" }

        val uses = sources
            .filter { it.name != "ComposeRules.kt" && it.name != "ComposeRulesUseTest.kt" }
            .flatMap { file ->
                file.readLines().mapIndexedNotNull { index, line ->
                    val code = line.substringBefore("//").trim()
                    if (code.startsWith("*") || code.startsWith("/*")) return@mapIndexedNotNull null
                    if (DEFAULT_RULES.containsMatchIn(code)) "${file.name}:${index + 1}: $code" else null
                }
            }

        assertEquals("make these rules through ComposeRules.kt", emptyList<String>(), uses)
    }
}
