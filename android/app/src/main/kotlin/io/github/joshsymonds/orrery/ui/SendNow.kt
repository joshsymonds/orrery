package io.github.joshsymonds.orrery.ui

import java.io.IOException

/**
 * Send now: interrupts the turn, then says each text the interrupt returned again, in send order. A say that fails
 * with an IOException stops the re-say, and that text and every later one come back, in order, for the box. An
 * interrupt that fails propagates before anything is said.
 */
fun sendNow(interrupt: () -> List<String>, say: (String) -> Unit): List<String> {
    val texts = interrupt()
    for ((index, text) in texts.withIndex()) {
        try {
            say(text)
        } catch (_: IOException) {
            return texts.subList(index, texts.size).toList()
        }
    }
    return emptyList()
}
