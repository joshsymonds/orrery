package io.github.joshsymonds.orrery.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import io.github.joshsymonds.orrery.R

val OrreryMono = FontFamily(
    Font(R.font.orrery_mono_regular, FontWeight.Normal, FontStyle.Normal),
    Font(R.font.orrery_mono_bold, FontWeight.Bold, FontStyle.Normal),
    Font(R.font.orrery_mono_italic, FontWeight.Normal, FontStyle.Italic),
)

val OrreryInter = FontFamily(
    Font(R.font.inter_regular, FontWeight.Normal, FontStyle.Normal),
    Font(R.font.inter_bold, FontWeight.Bold, FontStyle.Normal),
    Font(R.font.inter_italic, FontWeight.Normal, FontStyle.Italic),
)

// Lazy because OrreryTokens.Type reads OrreryMono from this file: built eagerly, the typography could read Type before Type has finished initialising.
val OrreryTypography: Typography by lazy {
    Typography(
        displayLarge = OrreryTokens.Type.display,
        displayMedium = OrreryTokens.Type.display,
        displaySmall = OrreryTokens.Type.display,
        headlineLarge = OrreryTokens.Type.title,
        headlineMedium = OrreryTokens.Type.title,
        headlineSmall = OrreryTokens.Type.title,
        titleLarge = OrreryTokens.Type.title,
        titleMedium = OrreryTokens.Type.title,
        titleSmall = OrreryTokens.Type.body,
        bodyLarge = OrreryTokens.Type.body,
        bodyMedium = OrreryTokens.Type.body,
        bodySmall = OrreryTokens.Type.small,
        labelLarge = OrreryTokens.Type.small,
        labelMedium = OrreryTokens.Type.small,
        labelSmall = OrreryTokens.Type.label,
    )
}

// Compose's TextStyle has no caps, so text drawn in type.label is cased here.
fun String.inLabelCase(): String = if (OrreryTokens.Type.labelCaps) uppercase() else this
