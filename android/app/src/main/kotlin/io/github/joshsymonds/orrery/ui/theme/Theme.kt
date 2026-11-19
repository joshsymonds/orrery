package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LocalMinimumInteractiveComponentSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider

val OrreryColorScheme = darkColorScheme(
    background = OrreryTokens.Surface.page,
    onBackground = OrreryTokens.Ink.one,
    surface = OrreryTokens.Surface.raised,
    onSurface = OrreryTokens.Ink.one,
    surfaceVariant = OrreryTokens.Surface.pressed,
    onSurfaceVariant = OrreryTokens.Ink.two,
    surfaceDim = OrreryTokens.Surface.side,
    surfaceBright = OrreryTokens.Surface.pressed,
    surfaceContainerLowest = OrreryTokens.Surface.side,
    surfaceContainerLow = OrreryTokens.Surface.well,
    surfaceContainer = OrreryTokens.Surface.overlay,
    surfaceContainerHigh = OrreryTokens.Surface.raised,
    surfaceContainerHighest = OrreryTokens.Surface.pressed,
    surfaceTint = OrreryTokens.Surface.raised,
    inverseSurface = OrreryTokens.Ink.one,
    inverseOnSurface = OrreryTokens.Surface.page,
    primary = OrreryTokens.Role.primary,
    onPrimary = OrreryTokens.Ink.onAccent,
    outline = OrreryTokens.Line.firm,
    outlineVariant = OrreryTokens.Line.soft,
    error = OrreryTokens.Role.bad,
    onError = OrreryTokens.Ink.onAccent,
)

val OrreryShapes = Shapes(
    extraSmall = RoundedCornerShape(OrreryTokens.Radius.one),
    small = RoundedCornerShape(OrreryTokens.Radius.two),
    medium = RoundedCornerShape(OrreryTokens.Radius.two),
    large = RoundedCornerShape(OrreryTokens.Radius.three),
    extraLarge = RoundedCornerShape(OrreryTokens.Radius.three),
)

@Composable
fun OrreryTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = OrreryColorScheme,
        typography = OrreryTypography,
        shapes = OrreryShapes,
        content = {
            CompositionLocalProvider(LocalMinimumInteractiveComponentSize provides OrreryTokens.Size.controlTouch) {
                content()
            }
        },
    )
}
