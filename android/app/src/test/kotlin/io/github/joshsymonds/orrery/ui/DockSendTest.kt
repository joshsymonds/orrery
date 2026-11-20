package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SHELL_PANE_ID = "%4"
private const val COMMAND = "ls -la"
private const val ABOUT = "the parser"
private const val ABOUT_LINE = "about the parser"
private const val PLACEHOLDER = "Type a command…"
private const val ABOUT_TAG = "dock-about"
private const val NOT_ABOUT_TAG = "dock-not-about"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockSendTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val server = FakeOrreryServer()
    private val sent = CopyOnWriteArrayList<String>()

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun shellDockSaysTypeACommandAndSendsThroughSend() {
        val draft = DockDraft().also { it.append(listOf(COMMAND)) }
        setDock(draft = draft, facts = SHELL_FACTS, paneId = SHELL_PANE_ID, about = null)
        composeRule.waitForIdle()

        assertEquals(PLACEHOLDER, editField().hint.toString())
        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil("the command reached send") { sent == listOf(COMMAND) }

        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun aboutLineShowsTheAboutTextUntilNotAboutThisClearsIt() {
        setDock(draft = DockDraft(), facts = SESSION_FACTS, sessionId = SESSION_ID, about = ABOUT)

        composeRule.onNodeWithTag(ABOUT_TAG).assertTextEquals(ABOUT_LINE)
        composeRule.onNodeWithTag(NOT_ABOUT_TAG).assertIsDisplayed()

        composeRule.onNodeWithTag(NOT_ABOUT_TAG).performClick()
        composeRule.awaitUntil("the about line is gone") {
            composeRule.onAllNodesWithTag(ABOUT_TAG).fetchSemanticsNodes().isEmpty()
        }
        composeRule.onAllNodesWithTag(NOT_ABOUT_TAG).assertCountEquals(0)
    }

    private fun setDock(
        draft: DockDraft,
        facts: DockFacts,
        sessionId: String? = null,
        paneId: String? = null,
        about: String?,
    ) {
        composeRule.setContent {
            MaterialTheme {
                Dock(
                    facts = facts,
                    nowSeconds = NOW,
                    client = OrreryClient(server.baseUrl),
                    draft = draft,
                    send = { sent += it },
                    sessionId = sessionId,
                    paneId = paneId,
                    about = about,
                )
            }
        }
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(composeRule.activity.window.decorView)) { "the dock box must host an EditText" }

    private fun findEditText(view: View): EditText? {
        if (view is EditText) return view
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                findEditText(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }

    private companion object {
        const val SESSION_ID = "synthetic-session-1"
        const val NOW = 1_000_000L
        val SHELL_FACTS = DockFacts(kind = DockKind.SHELL, name = "synthetic shell", state = "")
        val SESSION_FACTS = DockFacts(kind = DockKind.SESSION, name = "synthetic session", state = "done")
    }
}
