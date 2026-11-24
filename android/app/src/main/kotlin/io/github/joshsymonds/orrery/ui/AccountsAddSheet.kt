package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.SheetValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.password
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import io.github.joshsymonds.orrery.api.LlmWrite
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The plans the sheet offers, each stored exactly as written here. The first is the plan the sheet starts on. */
private val PLANS = listOf("Claude Max", "Claude Pro")

private const val TOKEN_PLACEHOLDER = "paste the setup-token"
private const val HINT = "Run claude setup-token on a machine with a browser and paste what it prints."

/** The reason a refused Add gives when the request never reached the server, so nothing was stored. */
private const val REASON_UNREACHED = "the server could not be reached"

/** The sheet's root, tagged so a test can find it. */
private const val SHEET_TAG = "add-sheet"

/** The line a refused Add shows in the sheet, with the server's reason in the middle. */
private fun refusedLine(reason: String): String = "Not added: $reason Nothing changed."

/**
 * The add sheet the accounts page opens from its +: a name, the plan the account is on, and the setup-token, with
 * Cancel and Add. Add calls [OrreryClient.addAccount] with the anthropic provider, off the main thread. While that
 * call is out the sheet holds: Add and Cancel are off, and a swipe or Back dismisses nothing, so the answer always
 * lands here. A stored account runs [onAdded] and an unanswered write runs [onUnanswered]; each closes the sheet, and
 * the token goes with it. A refused write keeps the sheet open with the server's reason. A write that never reached
 * the server is refused too, with the reason that the server could not be reached. [onDismiss] closes the sheet on
 * Cancel, a swipe or Back.
 *
 * The token is held only in this composition's state, so it is gone once the sheet leaves the composition. It is never
 * logged and is shown only in the masked field.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AccountsAddSheet(
    client: OrreryClient,
    onAdded: () -> Unit,
    onUnanswered: () -> Unit,
    onDismiss: () -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var plan by remember { mutableStateOf(PLANS.first()) }
    var token by remember { mutableStateOf("") }
    var refusal by remember { mutableStateOf<String?>(null) }
    val sendingState = remember { mutableStateOf(false) }
    var sending by sendingState
    // A form opens whole: its Cancel and Add sit at the foot, below where a half-open sheet would stop.
    val sheetState = rememberModalBottomSheetState(
        skipPartiallyExpanded = true,
        confirmValueChange = { target -> target != SheetValue.Hidden || !sendingState.value },
    )
    val scope = rememberCoroutineScope()

    fun add() {
        val sentName = name
        val sentPlan = plan
        val sentToken = token
        sending = true
        refusal = null
        scope.launch {
            val answer = try {
                withContext(Dispatchers.IO) { client.addAccount(sentName, sentPlan, sentToken) }
            } catch (error: IOException) {
                null
            }
            sending = false
            when (answer) {
                is LlmWrite.Done -> onAdded()
                LlmWrite.NoAnswer -> onUnanswered()
                is LlmWrite.Refused -> {
                    refusal = refusedLine(answer.reason)
                }
                null -> {
                    refusal = refusedLine(REASON_UNREACHED)
                }
            }
        }
    }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .testTag(SHEET_TAG)
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
        ) {
            Text("Add account", style = OrreryTokens.Type.title)
            SheetField(
                label = "Name",
                value = name,
                onValueChange = { name = it },
                tag = "add-name",
                placeholder = "",
                masked = false,
            )
            PlanSelect(plan = plan, onPlanChange = { plan = it })
            SheetField(
                label = "Token",
                value = token,
                onValueChange = { token = it },
                tag = "add-token",
                placeholder = TOKEN_PLACEHOLDER,
                masked = true,
            )
            Text(
                HINT,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("add-hint"),
            )
            refusal?.let { InlineNote(it, NoteTone.Error, Modifier.testTag("add-refused")) }
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                TextButton(onClick = onDismiss, enabled = !sending, modifier = Modifier.testTag("add-cancel")) {
                    Text("Cancel")
                }
                TextButton(onClick = { add() }, enabled = !sending, modifier = Modifier.testTag("add-submit")) {
                    Text("Add")
                }
            }
        }
    }
}

/**
 * The plan select: the plan it holds under its label, which opens a menu of [PLANS]. Choosing one closes the menu and
 * calls [onPlanChange] with it, exactly as it is written.
 */
@Composable
private fun PlanSelect(plan: String, onPlanChange: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        Text("Plan".inLabelCase(), color = OrreryTokens.Ink.two, style = OrreryTokens.Type.label)
        Box {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .background(OrreryTokens.Surface.well, shape)
                    .border(OrreryTokens.Size.hairline, OrreryTokens.Line.firm, shape)
                    .clickable { open = true }
                    .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.eight)
                    .testTag("add-plan"),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(plan, style = OrreryTokens.Type.body, color = OrreryTokens.Ink.one, modifier = Modifier.weight(1f))
                Text(
                    "⌄",
                    style = OrreryTokens.Type.body,
                    color = OrreryTokens.Ink.two,
                    modifier = Modifier.clearAndSetSemantics {},
                )
            }
            DropdownMenu(
                expanded = open,
                onDismissRequest = { open = false },
                modifier = Modifier.testTag("add-plan-menu"),
            ) {
                PLANS.forEach { option ->
                    DropdownMenuItem(
                        text = { Text(option) },
                        onClick = {
                            open = false
                            onPlanChange(option)
                        },
                        modifier = Modifier.testTag("add-plan:$option"),
                    )
                }
            }
        }
    }
}

/**
 * A single-line field under its [label], drawn as [Field] draws one. A [masked] field shows its text as dots, marks its
 * node as a password, and turns autocorrect off, so the keyboard offers no suggestions for it. The [placeholder] shows
 * while [value] is empty.
 */
@Composable
private fun SheetField(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
    tag: String,
    placeholder: String,
    masked: Boolean,
) {
    val interactionSource = remember { MutableInteractionSource() }
    val focused by interactionSource.collectIsFocusedAsState()
    val edge = if (focused) OrreryTokens.Role.focus else OrreryTokens.Line.firm
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)

    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        Text(label.inLabelCase(), color = OrreryTokens.Ink.two, style = OrreryTokens.Type.label)
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier
                .fillMaxWidth()
                .background(OrreryTokens.Surface.well, shape)
                .border(OrreryTokens.Size.hairline, edge, shape)
                .semantics { if (masked) password() }
                .testTag(tag),
            textStyle = OrreryTokens.Type.body.copy(color = OrreryTokens.Ink.one),
            cursorBrush = SolidColor(OrreryTokens.Role.focus),
            singleLine = true,
            visualTransformation = if (masked) PasswordVisualTransformation() else VisualTransformation.None,
            keyboardOptions = KeyboardOptions(
                autoCorrectEnabled = !masked,
                keyboardType = if (masked) KeyboardType.Password else KeyboardType.Text,
            ),
            interactionSource = interactionSource,
            decorationBox = { inner ->
                Box(
                    modifier = Modifier.padding(
                        horizontal = OrreryTokens.Space.twelve,
                        vertical = OrreryTokens.Space.eight,
                    ),
                ) {
                    if (value.isEmpty()) {
                        Text(placeholder, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.body)
                    }
                    inner()
                }
            },
        )
    }
}
