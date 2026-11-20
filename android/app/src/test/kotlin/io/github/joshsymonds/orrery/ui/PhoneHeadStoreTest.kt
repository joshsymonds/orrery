package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.runtime.MutableState
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PhoneHeadStoreTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private lateinit var context: Context

    @Before
    fun clearSettings() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
    fun aHiddenHeaderIsHiddenWhateverTheReader() {
        assertEquals(HeadFold.HIDDEN, headFold(hidden = true, atNewest = false, tapped = false))
        assertEquals(HeadFold.HIDDEN, headFold(hidden = true, atNewest = true, tapped = true))
    }

    @Test
    fun aHeaderIsReadingWhileTheReaderIsNotAtNewestAndNotTapped() {
        assertEquals(HeadFold.READING, headFold(hidden = false, atNewest = false, tapped = false))
    }

    @Test
    fun aHeaderIsOpenAtNewestOrAfterATap() {
        assertEquals(HeadFold.OPEN, headFold(hidden = false, atNewest = true, tapped = false))
        assertEquals(HeadFold.OPEN, headFold(hidden = false, atNewest = false, tapped = true))
    }

    @Test
    fun aHeaderHiddenInOneConversationStaysHiddenAfterARestart() {
        HeadStore(context).hidden = true

        assertTrue(HeadStore(context).hidden)
    }

    @Test
    fun aHeaderReadsAsShownBeforeAnyChoiceIsSaved() {
        assertFalse(HeadStore(context).hidden)
    }

    @Test
    fun aHeaderShownAgainIsNotHiddenAfterARestart() {
        HeadStore(context).hidden = true
        HeadStore(context).hidden = false

        assertFalse(HeadStore(context).hidden)
    }

    @Test
    fun rememberHeadHiddenStartsFromTheStoredChoice() {
        HeadStore(context).hidden = true
        var hidden: MutableState<Boolean>? = null

        composeRule.setContent { hidden = rememberHeadHidden() }

        composeRule.runOnIdle { assertTrue(hidden!!.value) }
    }

    @Test
    fun rememberHeadHiddenWritesAChangeThroughToTheStore() {
        var hidden: MutableState<Boolean>? = null
        composeRule.setContent { hidden = rememberHeadHidden() }

        composeRule.runOnIdle { hidden!!.value = true }

        composeRule.runOnIdle { assertTrue(hidden!!.value) }
        assertTrue(HeadStore(context).hidden)
    }
}
