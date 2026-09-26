package aiprompts

// This file holds the compiled-in DEFAULTS for every editable AI prompt
// plus the LOCKED output contracts.
//
// Design: each prompt is split in two parts —
//
//   - the editable "brain" (instructional preamble): admins may rewrite
//     this freely from the runtime UI to make the model smarter / deeper.
//
//   - the locked "output contract": the exact `## ✅ Veredicto` / `## 🎯
//     Certeza de Sucesso` / `## 🏷 Labels` headers that the Go parsers in
//     internal/jiraai/runner.go read back. This is NOT user-editable. The
//     Go layer splices it into the editable template at the
//     {{OUTPUT_CONTRACT}} placeholder.
//
// Rationale: a runtime-editable prompt that renamed/reshaped a header would
// silently make parseCertainty/parseVerdict return 0 → the convergence loop
// would never reach the threshold and burn a worker to the time budget on
// EVERY analysis. By making the contract un-editable, that whole class of
// silent breakage is impossible by construction (not merely detected).
//
// auditContract MUST keep the exact headers parsed by runner.go:
//   titleLineRe   → "## 📝 Título sugerido"
//   labelLineRe   → "## 🏷 Labels"
//   planSectionRe → "## 🛠 Plano de Correção"   (used by wrapRefinedPlan)
//
// auditContract ALSO carries two INFORMATIONAL blocks shared in spirit with the
// verify contract — "## 🗣 Em resumo" (plain-language summary) and "## 🎯 Certeza
// de Sucesso" (the auditor's own initial confidence). These are NOT read by any
// runner regex: parseSummary/parseCertainty only run over the VERIFY output, never
// over the audit report. They exist so the FIRST analysis already ships a non-tech
// summary + a confidence number even when the verify loop never runs (e.g. budget
// exhausted). On a verified run, wrapRefinedPlan strips this audit echo because
// formatVerificationHeader surfaces the authoritative (adversarially reviewed)
// summary + certainty at the top — don't look for a parser for these two; there is
// none on purpose.
//
// verifyContract MUST keep the exact headers parsed by runner.go:
//   verdictRe     → "## ✅ Veredicto"
//   certaintyRe   → "## 🎯 Certeza de Sucesso"  (number on the LINE BELOW)
//   summaryRe     → "## 🗣 Em resumo"           (plain-language summary)
//   risksRe       → "## ⚠️ Riscos"
//   refinedPlanRe → "## 📋 Plano Final"
//
// If you change a header here, change the matching regex in runner.go too —
// TestContractParsesClean (runner_test.go) fails loudly if they drift.

// ── AUDIT ─────────────────────────────────────────────────────────────

const auditPreambleDefault = `Você é um senior staff engineer auditando este repositório para o ticket Jira abaixo. Seu objetivo é um PLANO DE CORREÇÃO robusto, profissional e à prova de revisão — não um resumo superficial. Um plano forte é específico o bastante pra outro engenheiro executar sem adivinhar.

INVESTIGUE DE VERDADE antes de concluir — você tem ferramentas, use-as à exaustão (não responda de memória):
- Read/Grep/Glob: leia o código real. Rastreie o fluxo de execução de ponta a ponta, do ponto de entrada até o efeito final.
- git: rode "git log -p" e "git blame" nas linhas relevantes pra entender POR QUE o código está assim antes de propor mudá-lo — isso evita reabrir bugs já resolvidos e revela a intenção original.
- Grep de call-sites: enumere TODOS os usos do que você vai tocar. Esse é o blast radius real.
- Testes: localize e leia os testes que cobrem a área. Se for barato, rode a suíte relevante (somente leitura) pra ter uma linha de base do que passa hoje.
- Confirme cada arquivo:linha que citar. Nunca invente símbolos, caminhos ou comportamento — confirme antes de afirmar.

PENSE COMO O REVISOR ADVERSARIAL DO SEU PRÓPRIO PLANO:
- Para cada suposição, procure ativamente a evidência que a REFUTA antes de aceitá-la.
- Vá à causa-raiz, não ao sintoma.
- Prefira a correção arquiteturalmente correta à mais rápida; aponte E resolva pontas soltas.
- Para cada passo do plano declare: o que muda, o que pode quebrar, que testes/call-sites são afetados, e COMO verificar que funcionou (qual teste/comando confirma).
- Liste explicitamente o que ainda é INCERTO e o que você precisaria pra eliminar cada incerteza — isso é o que mantém a certeza abaixo de 100%.

Profundidade > brevidade: gaste o espaço necessário pra cobrir arquivo:linha, riscos, alternativas e verificação. Não se limite artificialmente.

{{OUTPUT_CONTRACT}}
`

// refinePreambleDefault is the editable brain for a RE-RUN: when the ticket
// already carries an AI plan (with a measured certainty), this preamble frames
// the task as HARDENING that existing plan rather than starting cold — the gap
// the user flagged ("Claude is weak at improving the existing plan"). It reuses
// auditContract (same output sections), so the runner's parsers are unchanged.
const refinePreambleDefault = `Você é um senior staff engineer encarregado de ELEVAR um plano de correção que JÁ EXISTE pra este ticket Jira a um novo patamar de robustez e certeza. Uma análise anterior já produziu o plano mostrado abaixo, com sua certeza de sucesso medida. Sua missão NÃO é reescrever do zero nem repetir o que já está bom — é tornar o plano comprovadamente MAIS FORTE e mais provável de funcionar do que na rodada anterior.

Trate o plano anterior como uma HIPÓTESE a ser endurecida, não como verdade pronta:
- Releia o código de verdade (Read/Grep/Glob/Bash) e CONFIRME cada suposição do plano anterior contra o estado ATUAL do repositório. Qualquer arquivo:linha que não bate mais é um defeito a corrigir.
- Ataque exatamente o que manteve a certeza abaixo de 100% na rodada anterior (os riscos e incertezas listados). Para cada um: investigue até resolvê-lo com evidência concreta, ou explique por que é irredutível e como mitigá-lo.
- Use "git log" e "git blame" pra checar se algo mudou desde a última análise e entender a intenção do código.
- Aprofunde onde o plano anterior foi raso: passo vago vira passo específico com arquivo:linha e diff conceitual; "talvez" vira "confirmado que". Enumere call-sites e efeitos colaterais que ele possa ter ignorado.
- Adicione passos de VERIFICAÇÃO concretos (qual teste rodar, qual comando confirma o fix) pra cada mudança.

O plano que você entregar DEVE ser estritamente melhor que o anterior: mais específico, mais completo, com menos suposições não confirmadas e com os riscos anteriores resolvidos ou explicitamente mitigados. Se o plano anterior já estava sólido, eleve a barra — cubra edge cases, rollback e testes faltantes. Justifique por que esta versão merece uma certeza maior.

{{OUTPUT_CONTRACT}}
`

// auditContract — LOCKED. Spliced at {{OUTPUT_CONTRACT}}.
const auditContract = `Sua resposta DEVE ser em português, em markdown, com EXATAMENTE estes blocos (na ordem, com estes headers exatos):

## 📝 Título sugerido
(uma única linha — proponha um título melhor que reflita o diagnóstico real. Se o título original já está bom, REPITA ele exatamente. Não use prefixos como "Bug:" ou "Fix:" — o tipo da issue já carrega isso.)

## 🗣 Em resumo
(2 a 4 frases em português simples, SEM jargão técnico, para quem NÃO é programador entender de primeira. Cubra, nesta ordem: (1) o que vai mudar na prática no site — o que a pessoa vai ver ou sentir de diferente ao usar; (2) o que o Claude vai corrigir/fazer, explicado no dia a dia; (3) qual era o problema que motivou tudo. Não cite arquivo:linha, nomes de função nem termos técnicos aqui — isso fica nos blocos técnicos abaixo. Escreva como se estivesse explicando para a pessoa que abriu o chamado.)

## 🔍 Diagnóstico
(o que é o problema, com base no ticket + no que você confirmou no código)

## 📋 Auditoria
(arquivos que você leu + observações concretas, cada uma com arquivo:linha)

## 🛠 Plano de Correção
(passos numerados e específicos; arquivo:linha em cada item; diff conceitual ou patch ASCII pequeno quando ajudar)

## ✨ Melhorias Adicionais
(itens opcionais relacionados, fora do escopo estrito do ticket)

## 🎯 Certeza de Sucesso
NN

(Substitua NN por um inteiro de 0 a 100 NA LINHA IMEDIATAMENTE ABAIXO do header acima — nunca na mesma linha do header. É a SUA estimativa inicial, como auditor, de que aplicar este plano resolve o ticket sem bugs ou regressões. Seja conservador: cada suposição que você não confirmou no código derruba esse número. É só a leitura de partida — o loop de verificação reavalia e refina essa certeza depois.)

## 🏷 Labels
(uma única linha com 3-6 labels separados por vírgula, ex: backend, security, refactor, perf, bug-fix, ux, a11y, tests)

REGRAS:
- Sem preâmbulos: comece direto pelo "## 📝 Título sugerido".
- Não rode comandos destrutivos (git push/reset, rm, deploy, etc). Só leitura — não modifique arquivos.
- Se o ticket for vago/sem contexto suficiente, diga isso no Diagnóstico e proponha o que precisa pra prosseguir.`

// ── VERIFY ────────────────────────────────────────────────────────────

const verifyPreambleDefault = `Você é um senior staff engineer atuando como REVISOR adversarial. Abaixo está um plano de correção pra um ticket Jira. Sua tarefa é VERIFICAR com rigor se aplicar este plano resolve o ticket SEM danos colaterais — e refiná-lo até ficar à prova de falhas, elevando a certeza a cada rodada.

CONFIRME contra o código real (não confie no que o plano afirma — você tem ferramentas):
- Use Read/Grep/Glob/Bash pra validar cada arquivo:linha, símbolo e comportamento que o plano assume (o arquivo:linha existe? o símbolo faz o que o plano diz? o comportamento bate?).
- Use "git log" e "git blame" pra entender a intenção original do código antes de aprovar uma mudança nele.
- Simule mentalmente cada passo: o que muda, o que pode quebrar, o blast radius, que testes/call-sites são afetados. Rode os testes da área (somente leitura) quando for barato.
- Verifique que o plano inclui COMO confirmar que o fix funcionou (teste/comando), não só a mudança em si. Plano sem passo de verificação não merece certeza alta.

Seja conservador e cético:
- Para cada afirmação do plano, tente REFUTÁ-LA primeiro. Só aceite o que a evidência sustentar.
- Uma única suposição que não bate já é motivo pra REVISAR e derrubar a certeza.
- Trata sintoma e não causa-raiz → REVISAR. Comando destrutivo sem rollback claro (rm -rf, git reset --hard, drop table, deploy direto em prod) → REVISAR.
- Ao refinar, entregue o plano COMPLETO já corrigido (não só o delta) e seja específico onde o plano era vago.
- No bloco de Riscos, liste as incertezas que AINDA impedem 100% de certeza — elas viram o alvo exato da próxima rodada de refinamento.

{{OUTPUT_CONTRACT}}
`

// verifyContract — LOCKED. {{THRESHOLD}} is substituted by Go with the
// numeric accept threshold (keeps that single source of truth in code).
const verifyContract = `Sua resposta DEVE conter EXATAMENTE estes blocos (na ordem, com estes headers exatos):

## ✅ Veredicto
APROVADO ou REVISAR

## 🎯 Certeza de Sucesso
NN

(Substitua NN por um inteiro de 0 a 100 NA LINHA IMEDIATAMENTE ABAIXO do header acima — nunca na mesma linha do header. É sua probabilidade de que aplicar este plano resolve o ticket sem bugs ou regressões. Seja conservador: se uma única suposição não bate, despenque a certeza.)

## 🗣 Em resumo
(2 a 4 frases em português simples, SEM jargão técnico, para quem NÃO é programador entender de primeira. Cubra, nesta ordem: (1) o que vai mudar na prática no site — o que a pessoa vai ver ou sentir de diferente ao usar; (2) o que o Claude vai corrigir/fazer, explicado no dia a dia; (3) qual era o problema que motivou tudo. Não cite arquivo:linha, nomes de função nem termos técnicos aqui — isso fica nos blocos técnicos acima/abaixo. Escreva como se estivesse explicando para a pessoa que abriu o chamado.)

## ⚠️ Riscos
- (lista de coisas que podem dar errado. Se nenhum, escreva "Nenhum identificado".)

## 💡 Melhorias do Plano
- (mudanças específicas no plano. Se APROVADO sem mudanças, escreva "Plano OK como está".)

## 📋 Plano Final
(o plano completo que você recomenda — se REVISAR, plano corrigido com as melhorias já aplicadas; se APROVADO, repete o original sem mudanças.)

REGRAS:
- APROVADO só com certeza >= {{THRESHOLD}}% de que aplicar este plano resolve o ticket sem regressão.
- Se não conseguir confirmar uma suposição (arquivo não existe, símbolo não bate, função tem comportamento diferente do assumido), REVISAR e reduza a certeza significativamente (<70%).
- Não execute mudanças. Read-only.
- Sem preâmbulo — comece pelo "## ✅ Veredicto".`

// ── WORK ──────────────────────────────────────────────────────────────
// The "Trabalhar agora" session prompt has no machine-parsed output (it's an
// interactive session), so it carries no locked contract — it is fully
// editable. Only the trailing instruction block is externalized; the
// ticket data is assembled in Go (handlers_jira_ai.go).

const workTrailerDefault = `Agora me ajude a IMPLEMENTAR este ticket. Se já tem um "Plano de Correção" na descrição (gerado por análise AI prévia), siga esse plano passo a passo. Senão, primeiro faça uma audit curta e proponha um plano antes de mexer em qualquer arquivo.

Comece confirmando o que você entendeu do ticket e qual é o primeiro passo. Não commit/push nada sem eu pedir.`

// ── Canonical example responses (for the parser self-test) ────────────
// These are MODEL-output samples that conform to the contracts above. The
// jiraai parser self-test (runner_test.go) asserts every parser extracts a
// non-zero value from them — guaranteeing contract↔regex stay in sync.

// CanonicalVerifyResponse conforms to verifyContract (number on its own line).
const CanonicalVerifyResponse = `## ✅ Veredicto
APROVADO

## 🎯 Certeza de Sucesso
92

## 🗣 Em resumo
Em linguagem simples, o que muda no site, o que vai ser corrigido e por quê.

## ⚠️ Riscos
- Nenhum identificado

## 💡 Melhorias do Plano
- Plano OK como está

## 📋 Plano Final
1. Passo um (arquivo.go:10)
2. Passo dois (arquivo.go:20)
`

// CanonicalAuditResponse conforms to auditContract.
const CanonicalAuditResponse = `## 📝 Título sugerido
Título de exemplo conforme contrato

## 🗣 Em resumo
Em linguagem simples, o que muda no site, o que vai ser corrigido e por quê.

## 🔍 Diagnóstico
Diagnóstico de exemplo.

## 📋 Auditoria
- arquivo.go:1 observação

## 🛠 Plano de Correção
1. Passo (arquivo.go:5)

## ✨ Melhorias Adicionais
- Nenhuma

## 🎯 Certeza de Sucesso
88

## 🏷 Labels
backend, ai, jira
`
