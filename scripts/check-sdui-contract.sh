#!/usr/bin/env bash
# check-sdui-contract.sh — o gate que substitui o erro de compilação
# que o SDUI removeu.
#
# O QUE ESTE GATE PROTEGE
# O app é distribuído por um repositório F-Droid próprio, sem atualização
# forçada — um build instalado pode ficar meses parado enquanto o servidor
# continua mudando. O payload SDUI (mandar UI nova sem lançamento de app novo)
# é exatamente o mecanismo que faria uma mudança de contrato quebrar um build
# antigo em produção, na mão do usuário, sem nenhum aviso — porque não existe
# mais um `go build` do lado do cliente para pegar isso antes. Este script é
# esse `go build` reintroduzido em CI.
#
# TRÊS VERIFICAÇÕES, NENHUMA OPCIONAL:
#
#   1. Deriva do contrato committado — `contracts/sdui/contract.json` é a
#      fonte da verdade lida pelos apps já publicados; se o Go que gera o
#      contrato divergiu do arquivo committado sem ninguém rodar
#      `make sdui-contract`, o resto deste script estaria validando um
#      contrato que não é o que o repo diz que é.
#
#   2. `sdui-compat check` — compara o contrato/fixtures ATUAIS contra todo
#      manifesto congelado em contracts/sdui/client-support/ (um manifesto
#      por app build já publicado). Com zero manifestos congelados (antes do
#      plano 07-08 congelar o primeiro), este passo é inerte por desenho —
#      não porque pulou, mas porque não existe ainda nenhum build publicado
#      contra o qual comparar.
#
#   3. Harness dourado de telas — toda tela registrada precisa ter golden
#      admin e não-admin committados e, quando diferem, uma entrada em
#      forbiddenForNonAdmin (ou, se são iguais de propósito, uma entrada em
#      screensWithNoRoleDifference com o motivo). Isto é o que impede uma
#      tela nova de vazar conteúdo admin-only silenciosamente.
#
# Se qualquer uma falhar, o script sai != 0 e a mensagem de falha nomeia o
# manifesto/tela/campo exato — quem vê o CI vermelho não precisa rodar nada
# localmente para saber o que quebrou.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$RAIZ"

echo "=== check-sdui-contract: 1/3 — contrato committado bate com o gerador ==="
go test ./internal/mobilebff/sdui/ -run '^TestContractDriftAgainstCommittedFile$' -v -count=1

echo
echo "=== check-sdui-contract: 2/3 — compatibilidade com manifestos de apps já publicados ==="
go run ./cmd/sdui-compat check

echo
echo "=== check-sdui-contract: 3/3 — harness dourado de telas (omissão de RBAC por tela) ==="
go test ./internal/mobilebff/sdui/ -run '^(TestGoldenScreens|TestGoldenScreens_EmptyRegistryPassesTrivially|TestGoldenScreens_RoleOmissionIsReasoned|TestGoldenScreens_NonAdminNeverContainsForbiddenStrings|TestFixtureConformance|TestFixtureRoundTripMatchesRealMarshaller)$' -v -count=1

echo
echo "check-sdui-contract: OK — contrato committado, manifestos congelados e goldens de tela estão todos consistentes"
