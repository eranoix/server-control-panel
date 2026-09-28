package dev.servercontrolpanel.lint

import io.gitlab.arturbosch.detekt.api.Config
import io.gitlab.arturbosch.detekt.api.RuleSet
import io.gitlab.arturbosch.detekt.api.RuleSetProvider

class BffLintRuleSetProvider : RuleSetProvider {

    override val ruleSetId: String = "bff-lint"

    override fun instance(config: Config): RuleSet =
        RuleSet(
            ruleSetId,
            listOf(
                BffOnlyNetworkClientRule(config),
            ),
        )
}
