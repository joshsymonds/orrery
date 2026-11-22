package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.LocalMinimumInteractiveComponentSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.mainThreadComposeRule
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
    val composeRule = mainThreadComposeRule()

    @Test
    fun colorSchemeIsBuiltFromTokens() {
        assertEquals(OrreryTokens.Surface.page, OrreryColorScheme.background)
        assertEquals(OrreryTokens.Surface.raised, OrreryColorScheme.surface)
        assertEquals(OrreryTokens.Role.primary, OrreryColorScheme.primary)
        assertEquals(OrreryTokens.Ink.one, OrreryColorScheme.onSurface)
        assertEquals(OrreryTokens.Line.firm, OrreryColorScheme.outline)
        assertEquals(OrreryTokens.Role.bad, OrreryColorScheme.error)
    }

    @Test
    fun colorSchemeSurfaceRolesAreTheSurfaceTokens() {
        assertEquals(OrreryTokens.Surface.side, OrreryColorScheme.surfaceDim)
        assertEquals(OrreryTokens.Surface.pressed, OrreryColorScheme.surfaceBright)
        assertEquals(OrreryTokens.Surface.side, OrreryColorScheme.surfaceContainerLowest)
        assertEquals(OrreryTokens.Surface.well, OrreryColorScheme.surfaceContainerLow)
        assertEquals(OrreryTokens.Surface.overlay, OrreryColorScheme.surfaceContainer)
        assertEquals(OrreryTokens.Surface.raised, OrreryColorScheme.surfaceContainerHigh)
        assertEquals(OrreryTokens.Surface.pressed, OrreryColorScheme.surfaceContainerHighest)
        assertEquals(OrreryTokens.Surface.raised, OrreryColorScheme.surfaceTint)
        assertEquals(OrreryTokens.Ink.one, OrreryColorScheme.inverseSurface)
        assertEquals(OrreryTokens.Surface.page, OrreryColorScheme.inverseOnSurface)
    }

    @Test
    fun orreryThemeProvidesTokenSchemeToContent() {
        var provided: ColorScheme? = null
        composeRule.setContent {
            OrreryTheme {
                provided = MaterialTheme.colorScheme
            }
        }
        composeRule.waitForIdle()
        assertSame(OrreryColorScheme, provided)
    }

    @Test
    fun orreryThemeDrawsAMaterialButtonControlTouchHigh() {
        var minimum: Dp? = null
        composeRule.setContent {
            OrreryTheme {
                // Measures the slot (touch target) the theme's minimum interactive size gives the Button, which draws its own surface at 40 dp inside that slot.
                Box(Modifier.testTag("control")) {
                    Button(onClick = {}) {
                        Box(Modifier.size(OrreryTokens.Size.icon))
                    }
                }
                minimum = LocalMinimumInteractiveComponentSize.current
            }
        }
        composeRule.waitForIdle()
        assertEquals(OrreryTokens.Size.controlTouch, minimum)
        composeRule.onNodeWithTag("control").assertHeightIsEqualTo(OrreryTokens.Size.controlTouch)
    }

    @Test
    fun typographyIsTheFiveTypeTokens() {
        val typography = OrreryTypography
        assertEquals(OrreryTokens.Type.display, typography.displayLarge)
        assertEquals(OrreryTokens.Type.display, typography.displayMedium)
        assertEquals(OrreryTokens.Type.display, typography.displaySmall)
        assertEquals(OrreryTokens.Type.title, typography.headlineLarge)
        assertEquals(OrreryTokens.Type.title, typography.headlineMedium)
        assertEquals(OrreryTokens.Type.title, typography.headlineSmall)
        assertEquals(OrreryTokens.Type.title, typography.titleLarge)
        assertEquals(OrreryTokens.Type.title, typography.titleMedium)
        assertEquals(OrreryTokens.Type.body, typography.titleSmall)
        assertEquals(OrreryTokens.Type.body, typography.bodyLarge)
        assertEquals(OrreryTokens.Type.body, typography.bodyMedium)
        assertEquals(OrreryTokens.Type.small, typography.bodySmall)
        assertEquals(OrreryTokens.Type.small, typography.labelLarge)
        assertEquals(OrreryTokens.Type.small, typography.labelMedium)
        assertEquals(OrreryTokens.Type.small, typography.labelSmall)
    }

    @Test
    fun labelAndCodeTypeAreOrreryMono() {
        assertEquals(OrreryMono, OrreryTokens.Type.label.fontFamily)
        assertEquals(OrreryMono, OrreryTokens.Type.code.fontFamily)
    }

    @Test
    fun shapesAreTheThreeRadii() {
        assertEquals(RoundedCornerShape(OrreryTokens.Radius.one), OrreryShapes.extraSmall)
        assertEquals(RoundedCornerShape(OrreryTokens.Radius.two), OrreryShapes.small)
        assertEquals(RoundedCornerShape(OrreryTokens.Radius.two), OrreryShapes.medium)
        assertEquals(RoundedCornerShape(OrreryTokens.Radius.three), OrreryShapes.large)
        assertEquals(RoundedCornerShape(OrreryTokens.Radius.three), OrreryShapes.extraLarge)
    }

    @Test
    fun labelCaseFollowsTheLabelCapsFlag() {
        val expected = if (OrreryTokens.Type.labelCaps) "WORKING" else "Working"
        assertEquals(expected, "Working".inLabelCase())
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
    fun interResolvesRegularBoldAndItalicResources() {
        assertEquals(
            FontFamily(
                Font(R.font.inter_regular, FontWeight.Normal, FontStyle.Normal),
                Font(R.font.inter_bold, FontWeight.Bold, FontStyle.Normal),
                Font(R.font.inter_italic, FontWeight.Normal, FontStyle.Italic),
            ),
            OrreryInter,
        )
    }

    @Test
    fun everyTypographyStyleInsideOrreryThemeUsesInter() {
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
            assertEquals(name, OrreryInter, style.fontFamily)
        }
    }
}
