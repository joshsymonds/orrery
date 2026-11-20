package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone
import java.util.concurrent.atomic.AtomicReference

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractVersionsTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private var originalZone: TimeZone? = null

    @Before
    fun useUtc() {
        originalZone = TimeZone.getDefault()
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    /** Two versions: v1 written at [ACCEPTED], v2 a minute later, changing F1. */
    private fun twoVersions(): List<ContractVersion> = listOf(
        contractVersionProto(1, writer = "Synthetic first writer", words = "Synthetic first words"),
        contractVersionProto(
            2,
            time = ACCEPTED + 60,
            writer = "Synthetic second writer",
            words = "Synthetic second words",
            changedRows = listOf("F1"),
        ),
    )

    private fun openVersionList() {
        composeRule.awaitExactlyOne(hasTestTag("contract-versions-toggle"))
        composeRule.onNodeWithTag("contract-versions-toggle").performClick()
    }

    @Test
    fun theVersionsListShowsNewestFirstWithTimeWriterAndWhatChanged() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(contractRecordProto("proposed", twoVersions())))
            composeRule.setContractScreen(server, writer = { "named $it" })

            composeRule.awaitExactlyOne(hasTestTag("contract-versions-toggle"))
            composeRule.onNodeWithTag("contract-versions-toggle").assertTextContains("2 versions")
            openVersionList()
            composeRule.scrollToRow("contract-version:1")

            val newest = composeRule.onNodeWithTag("contract-version:2")
            newest.assertTextContains("v2")
            newest.assertTextContains("22:14")
            newest.assertTextContains("named Synthetic second writer")
            newest.assertTextContains("F1")
            newest.assertTextContains("Synthetic second words")
            val oldest = composeRule.onNodeWithTag("contract-version:1")
            oldest.assertTextContains("22:13")
            oldest.assertTextContains("named Synthetic first writer")
            assertTrue(newest.fetchSemanticsNode().positionInRoot.y < oldest.fetchSemanticsNode().positionInRoot.y)
        }
    }

    @Test
    fun anOlderVersionIsReadOnlyAndItsBannerSaysSo() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(contractRecordProto("proposed", twoVersions())))
            composeRule.setContractScreen(server)
            openVersionList()
            composeRule.scrollToRow("contract-row:F1")
            composeRule.onNodeWithTag("contract-talk:F1").assertIsDisplayed()

            composeRule.scrollToRow("contract-version:1")
            composeRule.onNodeWithTag("contract-version:1").performClick()

            composeRule.awaitExactlyOne(hasTestTag("contract-older"))
            composeRule.onNodeWithTag("contract-older").assertTextContains("v1 of 2 · not the newest")
            composeRule.onNodeWithTag("contract-accept").assertIsNotEnabled()
            composeRule.scrollToRow("contract-row:F1")
            assertEquals(0, composeRule.onAllNodesWithTag("contract-talk:F1").fetchSemanticsNodes().size)
        }
    }

    @Test
    fun readTheNewestReturnsToTheNewestVersion() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(AtomicReference(contractRecordProto("proposed", twoVersions())))
            composeRule.setContractScreen(server)
            openVersionList()
            composeRule.scrollToRow("contract-version:1")
            composeRule.onNodeWithTag("contract-version:1").performClick()
            composeRule.awaitExactlyOne(hasTestTag("contract-read-newest"))

            composeRule.onNodeWithTag("contract-read-newest").performClick()

            composeRule.awaitNone(hasTestTag("contract-older"))
            composeRule.onNodeWithTag("contract-accept").assertIsEnabled()
        }
    }
}
