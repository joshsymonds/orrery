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

private val MaterialDefaults = Typography()

val OrreryTypography = Typography(
    displayLarge = MaterialDefaults.displayLarge.copy(fontFamily = OrreryMono),
    displayMedium = MaterialDefaults.displayMedium.copy(fontFamily = OrreryMono),
    displaySmall = MaterialDefaults.displaySmall.copy(fontFamily = OrreryMono),
    headlineLarge = MaterialDefaults.headlineLarge.copy(fontFamily = OrreryMono),
    headlineMedium = MaterialDefaults.headlineMedium.copy(fontFamily = OrreryMono),
    headlineSmall = MaterialDefaults.headlineSmall.copy(fontFamily = OrreryMono),
    titleLarge = MaterialDefaults.titleLarge.copy(fontFamily = OrreryMono),
    titleMedium = MaterialDefaults.titleMedium.copy(fontFamily = OrreryMono),
    titleSmall = MaterialDefaults.titleSmall.copy(fontFamily = OrreryMono),
    bodyLarge = MaterialDefaults.bodyLarge.copy(fontFamily = OrreryMono),
    bodyMedium = MaterialDefaults.bodyMedium.copy(fontFamily = OrreryMono),
    bodySmall = MaterialDefaults.bodySmall.copy(fontFamily = OrreryMono),
    labelLarge = MaterialDefaults.labelLarge.copy(fontFamily = OrreryMono),
    labelMedium = MaterialDefaults.labelMedium.copy(fontFamily = OrreryMono),
    labelSmall = MaterialDefaults.labelSmall.copy(fontFamily = OrreryMono),
)
