package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val KEYS_PATH = "/v1/panes/%253/keys"
private const val NOT_SENT = "key not sent"
private const val TIMEOUT_MILLIS = 5_000L

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuickKeysTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val labelToKey = listOf(
        "1" to "1",
        "2" to "2",
        "3" to "3",
        "Esc" to "Escape",
        "Ctrl-C" to "C-c",
        "←" to "Left",
        "↑" to "Up",
        "↓" to "Down",
        "→" to "Right",
        "Tab" to "Tab",
    )

    @Test
    fun eachButtonPostsItsKeyWithoutExpectQuestion() {
        FakeOrreryServer().use { server ->
            server.respond("POST", KEYS_PATH, 200)
            setQuickKeys(server)

            labelToKey.forEachIndexed { index, (label, key) ->
                composeRule.onNodeWithText(label).performScrollTo().performClick()
                composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == index + 1 }

                val request = server.requests[index]
                assertEquals("POST", request.method)
                assertEquals(KEYS_PATH, request.rawPath)
                val json = JSONObject(request.body)
                val keys = json.getJSONArray("keys")
                assertEquals("keys for $label", 1, keys.length())
                assertEquals("keys for $label", key, keys.getString(0))
                assertFalse("expect_question for $label", json.has("expect_question"))
            }
        }
    }

    @Test
    fun showsKeyNotSentWhenServerFails() {
        FakeOrreryServer().use { server ->
            server.respond("POST", KEYS_PATH, 500)
            setQuickKeys(server)

            composeRule.onNodeWithText("Tab").performScrollTo().performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }
            composeRule.onNodeWithText(NOT_SENT).assertIsDisplayed()
        }
    }

    @Test
    fun showsKeyNotSentWhenPromptChanged() {
        FakeOrreryServer().use { server ->
            server.respond("POST", KEYS_PATH, 409)
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }
            composeRule.onNodeWithText(NOT_SENT).assertIsDisplayed()
        }
    }

    private fun notSentShown(): Boolean =
        composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()

    private fun setQuickKeys(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent { QuickKeys(paneId = PANE_ID, client = client) }
    }
}
