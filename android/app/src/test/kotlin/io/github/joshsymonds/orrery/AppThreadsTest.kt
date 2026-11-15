package io.github.joshsymonds.orrery

import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.File

/** Ways of starting work on a thread of the app's own that the tests could not see. */
private val THREAD_STARTS = Regex("""\bThread\s*[({]|\bthread\s*[({]|\bExecutors\.|\bHandlerThread\b|\bTimer\s*\(""")

/**
 * Every plain thread the app starts goes through [BackgroundWork], which the tests count as in flight until its work
 * returns. A thread started any other way is invisible to them until it runs app code, so a wait can judge "nothing in
 * flight" before it has started (ReplyReceiverTest's blank reply, before BackgroundWork).
 */
class AppThreadsTest {
    @Test
    fun appCodeStartsThreadsOnlyThroughBackgroundWork() {
        val sources = File("src/main/kotlin").walk().filter { it.isFile && it.extension == "kt" }.toList()
        check(sources.isNotEmpty()) { "no app sources under ${File("src/main/kotlin").absolutePath}" }

        val starts = sources
            .filter { it.name != "BackgroundWork.kt" && it.path.replace('\\', '/').contains("/gen/").not() }
            .flatMap { file ->
                file.readLines().mapIndexedNotNull { index, line ->
                    val code = line.substringBefore("//").trim()
                    if (code.startsWith("*") || code.startsWith("/*")) return@mapIndexedNotNull null
                    if (THREAD_STARTS.containsMatchIn(code)) "${file.name}:${index + 1}: $code" else null
                }
            }

        assertEquals("start these through BackgroundWork.run", emptyList<String>(), starts)
    }
}
