package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownTest {
    @Test
    fun inlineMarkupIsRemovedAndStyled() {
        val text = parseInline("run `make it` then **stop** and *rest* ~~not~~ here")
        assertEquals("run make it then stop and rest not here", text.text)
        val styles = text.spanStyles.associate { text.text.substring(it.start, it.end) to it.item }
        assertEquals(FontWeight.Bold, styles["stop"]?.fontWeight)
        assertEquals(FontStyle.Italic, styles["rest"]?.fontStyle)
        assertTrue(styles.containsKey("make it"))
        assertTrue(styles.containsKey("not"))
    }

    @Test
    fun unmatchedMarkersStayLiteral() {
        assertEquals("a **open and `tick and 2 * 3 and snake_case_name", parseInline("a **open and `tick and 2 * 3 and snake_case_name").text)
    }

    @Test
    fun linksAndBareUrlsAreTappable() {
        val text = parseInline("see [the docs](https://example.com/a) or https://example.org/b.")
        assertEquals("see the docs or https://example.org/b.", text.text)
        val urls = text.getLinkAnnotations(0, text.length).map { (it.item as LinkAnnotation.Url).url }
        assertEquals(listOf("https://example.com/a", "https://example.org/b"), urls)
    }

    @Test
    fun nestedListsGetLeadsUnderTheirParentText() {
        val blocks = parseMarkdown("- one\n  - inner\n- two\n\n9. nine\n10. ten")
        val items = blocks.map { it as MdBlock.Item }
        assertEquals(listOf("•", "◦", "•", " 9.", "10."), items.map { it.marker })
        assertEquals(listOf(0, 2, 0, 0, 0), items.map { it.lead })
    }

    @Test
    fun tableKeepsEveryCellAndPadsAShortRow() {
        val blocks = parseMarkdown("| a | b |\n|---|--:|\n| `x` | **y** |\n| cut")
        val table = blocks.single() as MdBlock.Table
        assertEquals(listOf(ColumnAlign.Start, ColumnAlign.End), table.alignments)
        assertEquals(listOf(listOf("a", "b"), listOf("x", "y"), listOf("cut", "")), table.rows.map { row -> row.map { it.text } })
    }

    @Test
    fun unclosedFenceRunsToTheEnd() {
        val blocks = parseMarkdown("before\n\n```kotlin\nval x = 1\nval y")
        assertEquals("before", (blocks[0] as MdBlock.Paragraph).text.text)
        assertEquals(MdBlock.Code("kotlin", "val x = 1\nval y"), blocks[1])
    }

    @Test
    fun headingsRulesAndQuotes() {
        val blocks = parseMarkdown("## Title ##\n\n---\n\n> quoted **bit**")
        assertEquals(2, (blocks[0] as MdBlock.Heading).level)
        assertEquals("Title", (blocks[0] as MdBlock.Heading).text.text)
        assertTrue(blocks[1] is MdBlock.Rule)
        assertEquals("quoted bit", ((blocks[2] as MdBlock.Quote).blocks.single() as MdBlock.Paragraph).text.text)
    }

    @Test
    fun repeatedTextReturnsTheStoredParse() {
        val cache = MarkdownCache()
        val first = cache.blocks("**hello** `world`")
        assertSame(first, cache.blocks("**hello** `world`"))
    }

    @Test
    fun leastRecentlyUsedTextIsParsedAgainOnceEvicted() {
        val cache = MarkdownCache(capacity = 2)
        val a = cache.blocks("alpha")
        cache.blocks("beta")
        val c = cache.blocks("gamma")
        assertNotSame(a, cache.blocks("alpha"))
        assertSame(c, cache.blocks("gamma"))
    }

    @Test
    fun readingATextKeepsItAndEvictsTheLeastRecentlyUsed() {
        val cache = MarkdownCache(capacity = 2)
        val a = cache.blocks("alpha")
        val b = cache.blocks("beta")
        assertSame(a, cache.blocks("alpha"))
        val c = cache.blocks("gamma")
        assertSame(a, cache.blocks("alpha"))
        assertSame(c, cache.blocks("gamma"))
        assertNotSame(b, cache.blocks("beta"))
    }

    @Test
    fun anyInputParsesWithoutThrowing() {
        val inputs = listOf("", "`", "**", "[", "[a](", "|", "|\n|", "```", "> ", "- ", "#", "***", "\\", "<http://", "http://", ">".repeat(50))
        inputs.forEach { parseMarkdown(it) }
        val alphabet = "*_~`[]()<>|#-+:1. \n\\hps/"
        val random = java.util.Random(7)
        repeat(3000) {
            val input = String(CharArray(random.nextInt(80)) { alphabet[random.nextInt(alphabet.length)] })
            parseMarkdown(input)
            parseInline(input)
        }
    }
}
