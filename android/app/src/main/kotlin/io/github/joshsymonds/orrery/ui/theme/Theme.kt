package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable

val MochaColorScheme = darkColorScheme(
    background = Mocha.Base,
    onBackground = Mocha.Text,
    surface = Mocha.Surface0,
    onSurface = Mocha.Text,
    surfaceVariant = Mocha.Surface1,
    onSurfaceVariant = Mocha.Subtext0,
    primary = Mocha.Mauve,
    onPrimary = Mocha.Crust,
    outline = Mocha.Overlay0,
    error = Mocha.Red,
    onError = Mocha.Crust,
)

@Composable
fun OrreryTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = MochaColorScheme, content = content)
}
