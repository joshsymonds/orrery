package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import java.time.Instant
import java.time.ZoneId

/**
 * An epic's accepted contract, read-only: its title and approved line, then its sections under the contract's own
 * headings in the contract's order, with the epic's [requirements] in their place. Every text is a plain Text, so
 * nothing on the page can be edited.
 */
@Composable
fun ContractBody(
    contract: EpicContractRow,
    requirements: List<EpicRequirementRow>,
    modifier: Modifier = Modifier,
) {
    val base = if (contract.base.isEmpty()) "" else " · base ${contract.base}"
    val accepted = Instant.ofEpochSecond(contract.accepted).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)
    LazyColumn(
        modifier = modifier.testTag("contract-body"),
        contentPadding = PaddingValues(16.dp),
    ) {
        item { Text(contract.title, style = MaterialTheme.typography.titleLarge) }
        item { Text(contract.approved, style = MaterialTheme.typography.bodySmall) }

        heading(0, "What you asked for")
        item { Text(contract.intent, style = MaterialTheme.typography.bodyMedium) }

        heading(1, "What could go wrong, and how much we care")
        items(contract.failures) { failure ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Text(failure.id, style = MaterialTheme.typography.labelMedium)
                Text(failure.text, style = MaterialTheme.typography.bodyMedium)
                Text(failure.rating, style = MaterialTheme.typography.labelMedium)
                Text(failure.response, style = MaterialTheme.typography.bodySmall)
            }
        }
        item { Text(contract.care, style = MaterialTheme.typography.bodyMedium) }

        heading(2, "Things you did not ask for")
        if (contract.additions.isEmpty()) {
            item { Text("None", style = MaterialTheme.typography.bodyMedium) }
        } else {
            items(contract.additions) { addition ->
                Column(modifier = Modifier.padding(vertical = 4.dp)) {
                    Text(addition.location, style = MaterialTheme.typography.labelMedium)
                    Text(addition.what, style = MaterialTheme.typography.bodyMedium)
                    Text(addition.why, style = MaterialTheme.typography.bodySmall)
                    Text(addition.cost, style = MaterialTheme.typography.bodySmall)
                }
            }
        }

        heading(3, "What will be true when done")
        items(requirements) { requirement ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Text(requirement.id, style = MaterialTheme.typography.labelMedium)
                Text(requirement.text, style = MaterialTheme.typography.bodyMedium)
                Text(requirement.evidence, style = MaterialTheme.typography.bodySmall)
            }
        }

        heading(4, "What I'm assuming")
        items(contract.premises) { premise ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Text(premise.id, style = MaterialTheme.typography.labelMedium)
                Text(premise.text, style = MaterialTheme.typography.bodyMedium)
                Text(premise.ifWrong, style = MaterialTheme.typography.bodySmall)
            }
        }

        heading(5, "What we won't do")
        items(contract.exclusions) { exclusion ->
            Text(exclusion, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(vertical = 4.dp))
        }

        heading(6, "How, and why not the other ways")
        item { Text(contract.approach, style = MaterialTheme.typography.bodyMedium) }
        items(contract.alternatives) { alternative ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Text(alternative.alternative, style = MaterialTheme.typography.bodyMedium)
                Text(alternative.whyNot, style = MaterialTheme.typography.bodySmall)
                Text(alternative.reconsider, style = MaterialTheme.typography.bodySmall)
            }
        }

        heading(7, "What leaves this machine or can't be undone")
        items(contract.releaseSteps) { step ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Text(step.step, style = MaterialTheme.typography.bodyMedium)
                Text(step.target, style = MaterialTheme.typography.bodySmall)
                Text(step.undo, style = MaterialTheme.typography.bodySmall)
            }
        }
        item { Text(contract.releaseThen, style = MaterialTheme.typography.bodyMedium) }

        heading(8, "Checks the machines run")
        item { Text(contract.checks, style = MaterialTheme.typography.bodyMedium) }

        item { Text("accepted $accepted$base", style = MaterialTheme.typography.labelSmall) }
    }
}

private fun LazyListScope.heading(index: Int, text: String) {
    item {
        Text(
            text,
            style = MaterialTheme.typography.titleSmall,
            modifier = Modifier.padding(top = 16.dp).testTag("contract-heading:$index"),
        )
    }
}
