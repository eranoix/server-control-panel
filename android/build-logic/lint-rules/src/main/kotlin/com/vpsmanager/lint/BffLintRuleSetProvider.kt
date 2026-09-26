package com.vpsmanager.lint

import io.gitlab.arturbosch.detekt.api.Config
import io.gitlab.arturbosch.detekt.api.RuleSet
import io.gitlab.arturbosch.detekt.api.RuleSetProvider

/**
 * Registered via META-INF/services so detekt discovers this rule set on the
 * classpath contributed by the `detektPlugins` dependency -- no reference to
 * this class exists anywhere in the consuming module's own source.
 */
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
