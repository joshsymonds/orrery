package io.github.joshsymonds.orrery.ui

/** A recorded task status the app recognises; any other text is UNKNOWN and is shown as written. */
enum class TaskState { DONE, DISPATCHED, GATED, PENDING, GAP, SPLIT, UNKNOWN }

/** The recorded status words, lower case, each with its state. "integrated" counts as done. */
private val TASK_STATES = mapOf(
    "done" to TaskState.DONE,
    "integrated" to TaskState.DONE,
    "dispatched" to TaskState.DISPATCHED,
    "gated" to TaskState.GATED,
    "pending" to TaskState.PENDING,
    "gap" to TaskState.GAP,
    "split" to TaskState.SPLIT,
)

/**
 * The state of [status], compared without case: done and integrated are DONE; the record's other five words are
 * their own state; anything else, commit-suffixed forms such as "done at 3f9c2e1" included, is UNKNOWN.
 */
fun taskStateOf(status: String): TaskState = TASK_STATES[status.lowercase()] ?: TaskState.UNKNOWN
