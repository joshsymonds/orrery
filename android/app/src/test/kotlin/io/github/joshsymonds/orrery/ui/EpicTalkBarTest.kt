package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
class EpicTalkBarTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val CHANGED = 4_102_444_800L
        const val STEERING = "example-session"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setBar(steering: String, onOpen: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                EpicTalkBar(steering = steering, onOpen = onOpen)
            }
        }
    }

    private fun epic(steering: String): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome("")
            .setChanged(CHANGED)
            .setLive(true)
            .setSteering(steering)
        return EpicsGetResponse.newBuilder().setEpic(epic).build()
    }

    @Test
    fun tappingTheBarCallsOnOpenOnce() {
        val opened = AtomicInteger()
        setBar(steering = STEERING, onOpen = { opened.incrementAndGet() })

        composeRule.onNodeWithTag("epic-talk-bar")
            .assertTextEquals("Talk to the session that steers it")
            .performClick()

        assertEquals(1, opened.get())
    }

    @Test
    fun anEpicWithNoSteererDrawsTheNoConversationLineAndNoBar() {
        setBar(steering = "")

        composeRule.onNodeWithTag("epic-talk-none")
            .assertTextEquals("No conversation steers this epic")
        composeRule.onAllNodesWithTag("epic-talk-bar").assertCountEquals(0)
    }

    @Test
    fun theEpicScreenDrawsItsFootUnderTheLoadedPageWithTheEpicsSteering() {
        val steered = AtomicReference<String?>(null)
        val opened = AtomicInteger()
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, epic(steering = STEERING))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            composeRule.setContent {
                MaterialTheme {
                    EpicScreen(
                        client = OrreryClient(server.baseUrl),
                        epicId = EPIC_ID,
                        onBack = {},
                        onOpenStep = {},
                        foot = { detail ->
                            steered.set(detail.epic.steering)
                            EpicTalkBar(steering = detail.epic.steering, onOpen = { opened.incrementAndGet() })
                        },
                    )
                }
            }
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("epic-section:now", useUnmergedTree = true)
                    .fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("epic-talk-bar").assertIsDisplayed().performClick()

            assertEquals(STEERING, steered.get())
            assertEquals(1, opened.get())
        }
    }
}
