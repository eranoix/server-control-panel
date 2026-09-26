package com.vpsmanager.lint

import io.gitlab.arturbosch.detekt.api.CodeSmell
import io.gitlab.arturbosch.detekt.api.Config
import io.gitlab.arturbosch.detekt.api.Debt
import io.gitlab.arturbosch.detekt.api.Entity
import io.gitlab.arturbosch.detekt.api.Issue
import io.gitlab.arturbosch.detekt.api.Rule
import io.gitlab.arturbosch.detekt.api.Severity
import io.gitlab.arturbosch.detekt.api.internal.RequiresTypeResolution
import org.jetbrains.kotlin.psi.KtCallExpression
import org.jetbrains.kotlin.psi.KtTypeReference
import org.jetbrains.kotlin.resolve.BindingContext
import org.jetbrains.kotlin.resolve.descriptorUtil.fqNameOrNull
import org.jetbrains.kotlin.types.KotlinType

// Same allowlist as BffOnlyNetworkPlugin (the lexical half of this gate, in
// :build-logic:convention) -- kept independent on purpose. That plugin reads
// source text; this rule never looks at source text at all, only at what the
// Kotlin compiler resolved an expression's type to. A module built to bypass
// one (string concatenation, a fully-qualified reference with no import)
// still resolves to the same forbidden type here.
private val FORBIDDEN_TYPE_PACKAGES = listOf("okhttp3.", "retrofit2.")

private val EXEMPT_PATH_SEGMENTS = listOf(
    "/data/mobile-api-client/src/",
    "/data/src/",
)

/**
 * True when the file being analyzed lives under one of the two modules
 * allowed to touch OkHttp/Retrofit directly. Detekt only ever configures
 * this rule set for non-exempt modules (see the root build's `subprojects`
 * block), so in normal operation this check never has anything to reject --
 * it exists as defense in depth so the rule stays correct even if detekt is
 * ever applied to :data for an unrelated reason in the future.
 */
private fun isExemptFile(path: String): Boolean {
    val normalized = path.replace('\\', '/')
    return EXEMPT_PATH_SEGMENTS.any { normalized.contains(it) }
}

private fun KotlinType.isForbiddenNetworkType(): Boolean {
    val fqName = this.constructor.declarationDescriptor
        ?.fqNameOrNull()
        ?.asString()
        ?: return false
    return FORBIDDEN_TYPE_PACKAGES.any { fqName.startsWith(it) }
}

/**
 * The lexical gate in :build-logic:convention reads source text; it cannot
 * see a route built by joining two strings together at runtime, or a client
 * type referenced with its full package path and no import statement. This
 * rule closes that gap by reasoning over the type the Kotlin compiler
 * actually resolved an expression to, which does not change no matter how
 * the source text was written.
 *
 * It never inspects string contents at all -- an OkHttp/Retrofit client
 * TYPE appearing outside the BFF modules is itself the violation, regardless
 * of which route it would have called.
 */
@RequiresTypeResolution
class BffOnlyNetworkClientRule(config: Config) : Rule(config) {

    override val issue = Issue(
        id = "BffOnlyNetworkClient",
        severity = Severity.Defect,
        description = "Only :data and :data:mobile-api-client may construct or reference " +
            "okhttp3.*/retrofit2.* types directly -- every other module must go through " +
            ":data's repository abstractions.",
        debt = Debt.TWENTY_MINS,
    )

    override fun visitCallExpression(expression: KtCallExpression) {
        super.visitCallExpression(expression)
        if (isExemptFile(expression.containingKtFile.virtualFilePath)) return

        val resolvedType = bindingContext.getType(expression) ?: return
        if (resolvedType.isForbiddenNetworkType()) {
            report(
                CodeSmell(
                    issue,
                    Entity.from(expression),
                    "This expression resolves to ${resolvedType} -- an okhttp3/retrofit2 " +
                        "type outside the BFF module. Route through :data's repositories " +
                        "instead of constructing an HTTP client here.",
                )
            )
        }
    }

    override fun visitTypeReference(typeReference: KtTypeReference) {
        super.visitTypeReference(typeReference)
        if (isExemptFile(typeReference.containingKtFile.virtualFilePath)) return

        val resolvedType = bindingContext.get(BindingContext.TYPE, typeReference) ?: return
        if (resolvedType.isForbiddenNetworkType()) {
            report(
                CodeSmell(
                    issue,
                    Entity.from(typeReference),
                    "This type reference resolves to ${resolvedType} -- an okhttp3/retrofit2 " +
                        "type outside the BFF module. Route through :data's repositories " +
                        "instead of declaring an HTTP client type here.",
                )
            )
        }
    }
}
