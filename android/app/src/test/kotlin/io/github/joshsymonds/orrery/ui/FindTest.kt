package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertContentDescriptionEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private val ENTRIES = listOf("build", "rebuild the BUILD")

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class FindTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun findMatchesFindsEveryMatchAcrossEntriesInOrderCaseInsensitively() {
        val texts = listOf("Build passed", "rebuild the BUILD", "lint")

        assertEquals(
            listOf(FindMatch(0, 0..4), FindMatch(1, 2..6), FindMatch(1, 12..16)),
            findMatches(texts, "build"),
        )
    }

    @Test
    fun findMatchesDoesNotOverlapItselfAsTheWebCountsIt() {
        assertEquals(listOf(FindMatch(0, 0..1), FindMatch(0, 2..3)), findMatches(listOf("aaaa"), "aa"))
    }

    @Test
    fun findMatchesOfAnEmptyQueryIsEmpty() {
        assertEquals(emptyList<FindMatch>(), findMatches(listOf("build"), ""))
    }

    @Test
    fun stepFindMovesToTheNextMatchAndWrapsPastTheLast() {
        assertEquals(1, stepFind(current = 0, count = 3, step = 1))
        assertEquals(0, stepFind(current = 2, count = 3, step = 1))
    }

    @Test
    fun stepFindMovesToThePreviousMatchAndWrapsPastTheFirst() {
        assertEquals(0, stepFind(current = 1, count = 3, step = -1))
        assertEquals(2, stepFind(current = 0, count = 3, step = -1))
    }

    @Test
    fun stepFindWithNoMatchesHasNoCurrentMatch() {
        assertEquals(-1, stepFind(current = -1, count = 0, step = 1))
        assertEquals(-1, stepFind(current = -1, count = 0, step = -1))
    }

    @Test
    fun findCountWordsReadsTheCurrentMatchOfTheCount() {
        assertEquals("2 of 3", findCountWords(query = "b", count = 3, current = 1))
    }

    @Test
    fun findCountWordsReadsNoMatchesWhenAQueryFindsNone() {
        assertEquals("No matches", findCountWords(query = "z", count = 0, current = -1))
    }

    @Test
    fun findCountWordsReadsNothingForAnEmptyQuery() {
        assertEquals("", findCountWords(query = "", count = 0, current = -1))
    }

    @Test
    fun markedRunsSplitTheTextAtEachMatchOfItsEntryWithTheCurrentOneSolid() {
        val matches = findMatches(ENTRIES, "build")

        assertEquals(
            listOf(
                MarkedRun("re", FindMark.Plain),
                MarkedRun("build", FindMark.Match),
                MarkedRun(" the ", FindMark.Plain),
                MarkedRun("BUILD", FindMark.Current),
            ),
            markedRuns(ENTRIES[1], entry = 1, matches = matches, current = 2),
        )
    }

    @Test
    fun markedRunsMarkNoMatchSolidWhenTheCurrentMatchIsInAnotherEntry() {
        val matches = findMatches(ENTRIES, "build")

        assertEquals(
            listOf(
                MarkedRun("re", FindMark.Plain),
                MarkedRun("build", FindMark.Match),
                MarkedRun(" the ", FindMark.Plain),
                MarkedRun("BUILD", FindMark.Match),
            ),
            markedRuns(ENTRIES[1], entry = 1, matches = matches, current = 0),
        )
    }

    @Test
    fun findHighlightTintsEachMatchAndFillsTheCurrentOne() {
        val matches = findMatches(ENTRIES, "build")
        val highlight = findHighlight(ENTRIES[1], entry = 1, matches = matches, current = 2)

        assertEquals(ENTRIES[1], highlight.text)
        assertEquals(listOf(2 to 7, 12 to 17), highlight.spanStyles.map { it.start to it.end })
        assertEquals(
            listOf(OrreryTokens.Role.markLine, OrreryTokens.Role.mark),
            highlight.spanStyles.map { it.item.background },
        )
    }

    @Test
    fun findBarReadsItsCountFromTheModelAndItsControlsCallTheirCallbacks() {
        var next = 0
        var previous = 0
        var closed = 0
        composeRule.setContent {
            FindBar(
                query = "b",
                count = 3,
                current = 1,
                onQuery = {},
                onNext = { next++ },
                onPrevious = { previous++ },
                onClose = { closed++ },
            )
        }

        composeRule.onNodeWithTag("find-bar").assertIsDisplayed()
        composeRule.onNodeWithTag("find-count").assertTextEquals("2 of 3")
        composeRule.onNodeWithTag("find-next").assertContentDescriptionEquals("Next match").performClick()
        composeRule.onNodeWithTag("find-previous").assertContentDescriptionEquals("Previous match").performClick()
        composeRule.onNodeWithTag("find-close").assertContentDescriptionEquals("Close find").performClick()

        assertEquals(1, next)
        assertEquals(1, previous)
        assertEquals(1, closed)
    }

    @Test
    fun findBarSaysNoMatchesForAQueryThatFindsNone() {
        composeRule.setContent { FindBar(query = "z", count = 0, current = -1, onQuery = {}, onNext = {}, onPrevious = {}, onClose = {}) }

        composeRule.onNodeWithTag("find-count").assertTextEquals("No matches")
    }

    @Test
    fun findBarShowsNoCountForAnEmptyQuery() {
        composeRule.setContent { FindBar(query = "", count = 0, current = -1, onQuery = {}, onNext = {}, onPrevious = {}, onClose = {}) }

        composeRule.onNodeWithTag("find-count").assertTextEquals("")
    }

    @Test
    fun findBarPassesTypedTextToOnQuery() {
        val typed = mutableStateOf("")
        composeRule.setContent {
            FindBar(query = typed.value, count = 0, current = -1, onQuery = { typed.value = it }, onNext = {}, onPrevious = {}, onClose = {})
        }

        composeRule.onNode(hasSetTextAction()).performTextInput("build")

        assertEquals("build", typed.value)
    }

    @Test
    fun findBarStepsToTheNextMatchFromTheSearchAction() {
        var next = 0
        composeRule.setContent {
            FindBar(query = "b", count = 3, current = 0, onQuery = {}, onNext = { next++ }, onPrevious = {}, onClose = {})
        }

        composeRule.onNode(hasSetTextAction()).performImeAction()

        assertEquals(1, next)
    }
}
