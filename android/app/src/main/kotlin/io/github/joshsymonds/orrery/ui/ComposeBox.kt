package io.github.joshsymonds.orrery.ui

import android.text.Editable
import android.text.InputType
import android.text.TextWatcher
import android.widget.EditText
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.res.ResourcesCompat
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.OrreryClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val COMPOSE_INPUT_TYPE =
    InputType.TYPE_CLASS_TEXT or
        InputType.TYPE_TEXT_FLAG_MULTI_LINE or
        InputType.TYPE_TEXT_FLAG_AUTO_CORRECT or
        InputType.TYPE_TEXT_FLAG_CAP_SENTENCES

/**
 * Multi-line draft field for one pane. Send posts the whole draft in one
 * call and clears it only after that call succeeds. The field is locked while
 * a send is in flight, so the draft cannot change under the request.
 */
@Composable
fun ComposeBox(paneId: String, client: OrreryClient, modifier: Modifier = Modifier) {
    key(paneId) {
        ComposeDraft(paneId, client, modifier)
    }
}

@Composable
private fun ComposeDraft(paneId: String, client: OrreryClient, modifier: Modifier) {
    var draft by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Column(modifier = modifier) {
        AndroidView(
            modifier = Modifier.fillMaxWidth(),
            factory = { context ->
                EditText(context).apply {
                    inputType = COMPOSE_INPUT_TYPE
                    typeface = ResourcesCompat.getFont(context, R.font.orrery_mono_regular)
                    addTextChangedListener(
                        object : TextWatcher {
                            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit

                            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) = Unit

                            override fun afterTextChanged(s: Editable?) {
                                draft = s.toString()
                            }
                        },
                    )
                }
            },
            update = { view ->
                view.isEnabled = !sending
                if (view.text.toString() != draft) {
                    view.setText(draft)
                }
            },
        )
        if (failed) {
            Text("not sent")
        }
        Button(
            enabled = draft.isNotBlank() && !sending,
            onClick = {
                val text = draft
                sending = true
                failed = false
                scope.launch {
                    val sent = withContext(Dispatchers.IO) {
                        try {
                            client.sendText(paneId, text)
                            true
                        } catch (error: IOException) {
                            false
                        }
                    }
                    sending = false
                    if (sent) {
                        draft = ""
                    } else {
                        failed = true
                    }
                }
            },
        ) {
            Text("Send")
        }
    }
}
