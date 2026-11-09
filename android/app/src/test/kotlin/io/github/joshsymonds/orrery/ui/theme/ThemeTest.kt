package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import io.github.joshsymonds.orrery.R
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

    @Test
    fun orreryMonoResolvesRegularBoldAndItalicResources() {
        assertEquals(
            FontFamily(
                Font(R.font.orrery_mono_regular, FontWeight.Normal, FontStyle.Normal),
                Font(R.font.orrery_mono_bold, FontWeight.Bold, FontStyle.Normal),
                Font(R.font.orrery_mono_italic, FontWeight.Normal, FontStyle.Italic),
            ),
            OrreryMono,
        )
    }

    @Test
    fun everyTypographyStyleInsideOrreryThemeUsesOrreryMono() {
        var styles: List<Pair<String, TextStyle>>? = null
        composeRule.setContent {
            OrreryTheme {
                val typography = MaterialTheme.typography
                styles = listOf(
                    "displayLarge" to typography.displayLarge,
                    "displayMedium" to typography.displayMedium,
                    "displaySmall" to typography.displaySmall,
                    "headlineLarge" to typography.headlineLarge,
                    "headlineMedium" to typography.headlineMedium,
                    "headlineSmall" to typography.headlineSmall,
                    "titleLarge" to typography.titleLarge,
                    "titleMedium" to typography.titleMedium,
                    "titleSmall" to typography.titleSmall,
                    "bodyLarge" to typography.bodyLarge,
                    "bodyMedium" to typography.bodyMedium,
                    "bodySmall" to typography.bodySmall,
                    "labelLarge" to typography.labelLarge,
                    "labelMedium" to typography.labelMedium,
                    "labelSmall" to typography.labelSmall,
                )
            }
        }
        composeRule.waitForIdle()
        val captured = checkNotNull(styles)
        assertEquals(15, captured.size)
        captured.forEach { (name, style) ->
            assertEquals(name, OrreryMono, style.fontFamily)
        }
    }
}
