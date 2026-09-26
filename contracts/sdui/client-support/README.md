# contracts/sdui/client-support

Manifestos congelados do vocabulário SDUI, um por versionCode de app Android
**já publicado** no repositório F-Droid próprio.

## Por que isto existe

O app é distribuído sem atualização forçada — um build instalado pode ficar
meses parado enquanto o servidor continua mudando. SDUI (mandar UI nova sem
lançamento de app novo) é exatamente o mecanismo que remove o erro de
compilação que normalmente pegaria uma mudança de contrato incompatível com
um build antigo. Cada arquivo aqui é uma fotografia do que um app já em campo
sabe interpretar; `cmd/sdui-compat check` compara o contrato/fixtures atuais
do servidor contra CADA fotografia e falha o CI se alguma deixar de valer.

Sem estes manifestos, uma mudança que remove um campo obrigatório, muda um
tipo, ou reduz um enum passaria despercebida em CI e só apareceria como tela
quebrada na mão de um usuário com o app desatualizado — o pior lugar possível
para descobrir.

## Quando congelar um manifesto novo

Toda vez que um build do app Android é **publicado** no repositório F-Droid
(pipeline da Fase 12), rode:

```
go run ./cmd/sdui-compat freeze -version <versionCode>
```

usando o `versionCode` exato do build publicado (o mesmo número que vai no
`AndroidManifest.xml`/`build.gradle` daquele build). Isto grava
`android-<versionCode>.json` — o Contract gerado pelo servidor NO MOMENTO do
freeze, ou seja, o vocabulário que aquele build específico foi testado
contra. Faça isto como parte do processo de publicação, não depois.

## Por que é append-only

Um manifesto congelado descreve um app build que já existe no mundo —
sobrescrevê-lo é falsificar histórico: qualquer mudança de servidor feita
depois daquele build ser publicado deixaria de ser checada contra o
vocabulário real que ele conhece. `freeze` se recusa a sobrescrever um
arquivo já existente a menos que `-force` seja passado explicitamente — e
`-force` não deveria ser usado, exceto para corrigir um freeze
comprovadamente errado antes de qualquer outro commit depender dele.

Nunca apague um manifesto congelado depois que um build correspondente foi
publicado, mesmo que aquele build tenha parado de ser distribuído — sem
atualização forçada, não há garantia de que ninguém mais o tem instalado.

## Como ler uma falha de `check`

```
android-42:
  [breaking] table.components[0]: contracts/sdui/fixtures/screens/x.admin.json: componente 0 tem type="table" mas falta o campo obrigatório "rows_source" que o manifesto congelado exige
  -> 1 quebrando, 0 nota(s)

sdui-compat check: FALHOU — pelo menos um manifesto congelado (app já publicado
em campo) não sobrevive à mudança atual do servidor. Corrija o campo/tipo
apontado acima ou reverta a mudança antes de mergear.
```

A primeira linha nomeia: o manifesto congelado afetado (`android-42`, ou
seja, o versionCode publicado), o tipo/objeto de componente, e o
campo/detalhe exato. Duas saídas possíveis:

- **A mudança era mesmo incompatível** — reverta-a, ou proteja-a atrás de um
  `sdui_version` novo que apps antigos ignoram por desenho.
- **A mudança é aceitável e o app antigo nunca vai precisar deste campo** —
  isso não deveria acontecer para uma mudança classificada `breaking` (a
  classificação em `internal/mobilebff/sdui/compat.go` já assume a postura
  conservadora: remoção/enfraquecimento de algo que o app antigo depende é
  sempre quebra). Se a classificação parecer errada, o bug está no
  classificador, não no gate — corrija `Compat`/`FixtureRenderable`, não
  ignore a falha.

Um `[note]` (ex.: `field_added_required`) não falha o CI — é um aviso de que
um campo novo obrigatório foi adicionado ao contrato atual sem existir em
nenhum manifesto congelado; o app antigo nunca vai enviar/depender dele, mas
vale revisar se o servidor lida bem com a ausência dele vindo de um cliente
antigo.

## Limite conhecido: RBAC binário, não por linha

O harness dourado (`internal/mobilebff/sdui/golden.go` +
`golden_test.go`) só distingue dois papéis: admin e não-admin. Ele prova que
uma tela não vaza conteúdo admin-only para um viewer não-admin, mas não
consegue expressar autorização por posse de recurso individual (ex.: "o
usuário X só vê SUAS próprias linhas da tabela Y") — isso exigiria um golden
por identidade de recurso, não por papel, o que este harness não modela.
Telas que precisam desse tipo de filtro precisam de teste de autorização
próprio, adicional a este harness.
