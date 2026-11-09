package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.junit4.createComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ThemeTest {
    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun mochaSchemePinsBackgroundSurfacePrimaryAndOnSurface() {
        assertEquals(Color(0xFF1E1E2E), MochaColorScheme.background)
        assertEquals(Color(0xFF313244), MochaColorScheme.surface)
        assertEquals(Color(0xFFCBA6F7), MochaColorScheme.primary)
        assertEquals(Color(0xFFCDD6F4), MochaColorScheme.onSurface)
    }

    @Test
    fun orreryThemeProvidesMochaSchemeToContent() {
        var provided: ColorScheme? = null
        composeRule.setContent {
            OrreryTheme {
                provided = MaterialTheme.colorScheme
            }
        }
        composeRule.waitForIdle()
        assertSame(MochaColorScheme, provided)
    }
}
