package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private class RecordingUriHandler : UriHandler {
    val opened = mutableListOf<String>()

    override fun openUri(uri: String) {
        opened += uri
    }
}

private object NoAppUriHandler : UriHandler {
    override fun openUri(uri: String) {
        throw IllegalArgumentException("no app opens $uri")
    }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class MarkdownViewTest {
    @get:Rule
    val composeRule = createComposeRule()

    private fun setBody(markdown: String, uriHandler: UriHandler = RecordingUriHandler()) {
        val blocks = parseMarkdown(markdown)
        composeRule.setContent {
            MaterialTheme {
                CompositionLocalProvider(LocalUriHandler provides uriHandler) {
                    MarkdownBody(blocks)
                }
            }
        }
    }

    @Test
    fun headingShowsItsTextWithoutHashMarkers() {
        setBody("## Synthetic heading")

        composeRule.onNodeWithText("Synthetic heading").assertIsDisplayed()
        composeRule.onAllNodesWithText("#", substring = true).assertCountEquals(0)
    }

    @Test
    fun quoteShowsItsTextWithoutArrowMarker() {
        setBody("> synthetic quote")

        composeRule.onNodeWithText("synthetic quote").assertIsDisplayed()
        composeRule.onAllNodesWithText(">", substring = true).assertCountEquals(0)
    }

    @Test
    fun paragraphShowsItsText() {
        setBody("synthetic paragraph")

        composeRule.onNodeWithText("synthetic paragraph").assertIsDisplayed()
    }

    @Test
    fun fencedBlockWithLanguageShowsItsCodeAndLabel() {
        setBody("```kotlin\nval synthetic = 1\n```")

        composeRule.onNodeWithText("val synthetic = 1", substring = true).assertIsDisplayed()
        composeRule.onNodeWithText("kotlin").assertIsDisplayed()
        composeRule.onAllNodes(hasText("", substring = true)).assertCountEquals(2)
    }

    @Test
    fun fencedBlockWithoutLanguageShowsOnlyItsCode() {
        setBody("```\nval synthetic = 1\n```")

        composeRule.onNodeWithText("val synthetic = 1", substring = true).assertIsDisplayed()
        composeRule.onAllNodes(hasText("", substring = true)).assertCountEquals(1)
    }

    @Test
    fun tappedLinkHandsItsExactUrlToTheUriHandlerOnce() {
        val handler = RecordingUriHandler()
        setBody("[docs](https://example.com/a)", handler)

        composeRule.onNodeWithText("docs").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf("https://example.com/a"), handler.opened)
    }

    @Test
    fun linkThatNoAppOpensLeavesTheTextStanding() {
        setBody("[docs](https://example.com/a)", NoAppUriHandler)

        composeRule.onNodeWithText("docs").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("docs").assertIsDisplayed()
    }
}
