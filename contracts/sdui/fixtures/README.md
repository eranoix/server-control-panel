# Fixtures douradas do SDUI

Esta pasta é a corpus compartilhada do contrato SDUI. As mesmas fixtures são
consumidas por dois lados independentes:

- **Go** (`internal/mobilebff/sdui/contract_test.go`, `TestFixtureConformance`)
  valida cada fixture contra `contracts/sdui/contract.json` — campos
  obrigatórios presentes, nenhum campo fora do contrato (exceto nas fixtures
  `unknown-*`, que existem justamente para carregar tipos/campos desconhecidos).
  Isso prova conformidade **estrutural**, não que o marshaller do servidor
  realmente emite aquela forma — para isso existe o teste de ida e volta
  abaixo.
- **Go** (`internal/mobilebff/sdui/contract_test.go`,
  `TestFixtureRoundTripMatchesRealMarshaller`) desserializa cada fixture com
  `UnmarshalScreen` (o caminho real de leitura do servidor), reserializa com
  `json.Marshal(*Envelope)` (que invoca o `Screen.MarshalJSON` real, o mesmo
  caminho de produção) e compara o resultado **semanticamente** (JSON
  canonicalizado, não string crua) contra o arquivo original. Uma fixture
  escrita à mão que só "parece" certa mas diverge da saída real (um
  `omitempty` que some um campo, um tipo aninhado que serializa diferente)
  falha aqui mesmo passando em `TestFixtureConformance` — foi exatamente esse
  buraco que uma fixture errada na fase 11 explorou, passando verde num teste
  estrutural enquanto o consumidor real quebrava no primeiro contato com o
  servidor.
  **As fixtures `unknown-*` ficam de fora, por desenho:** elas existem
  especificamente para carregar um `"type"` (ex.: `"gantt"`) ou um campo que
  os 7 tipos Go do servidor não conseguem representar — a tolerância a isso é
  responsabilidade do **cliente** (Kotlin, plano 07-03), não do servidor.
  `UnmarshalScreen` rejeitaria as três com erro de decodificação, então não
  existe "saída real do marshaller" para comparar; não há o que testar de
  ida e volta nelas.
- **Kotlin** (planos 07-03/07-05) faz o parse e renderiza estas mesmas
  fixtures sem nenhum servidor rodando, validando o renderer `:sdui`
  isoladamente e de forma paralela ao trabalho de `internal/mobilebff`.

**Editar uma fixture aqui significa rodar as duas suítes de novo** — a Go
(`go test ./internal/mobilebff/sdui/...`) e, quando plano 07-03/07-05
existirem, a Kotlin. Uma fixture que só passa num lado é pior que nenhuma
fixture: ela promete algo que o outro lado não sustenta.

## O que cada fixture prova

### `all-components.json`
Uma tela (`fixture.all`) com exatamente um componente de cada um dos 7 tipos
do vocabulário, com dados plausíveis do domínio vps-manager: uma tabela de
containers, um formulário de regra de notificação, uma lista de notificações,
o detalhe de um container, uma ação "Deploy", um gráfico de CPU e uma
confirmação destrutiva de matar container. Sustenta a garantia de que o vocabulário
fechado existe e é completo.

O `action_id` do `confirm_destructive` (`container.kill`) é **o mesmo**
`action_id` de uma das `row_actions` da tabela — um `confirm_destructive` não
renderiza nada por si só, ele é indexado por `action_id` e consultado quando
essa ação específica é disparada em outro componente. Este é o exemplo
realista desse vínculo que o teste de indexação de confirmação do renderer
(plano 07-06) precisa.

### `unknown-noncritical.json`
Três componentes: uma `table` conhecida, um componente com `"type": "gantt"`
sem a chave `critical` (default `false`), e uma `action` conhecida. Prova o
Caso 1 de tolerância: os dois componentes conhecidos continuam renderizando
normalmente; só o tipo desconhecido é ignorado.

### `unknown-critical.json`
Uma `action` conhecida mais `{"type": "gantt", "id": "g1", "critical": true}`.
Prova o Caso 2 de tolerância: tipo desconhecido + `critical: true` vira um
placeholder "atualize o app" na posição do componente — nunca um drop
silencioso, nunca um crash.

### `unknown-extra-fields.json`
Uma `table` válida carregando duas chaves extras que o contrato não define no
nível do componente (`sort_default`, `density`) e uma chave extra dentro de
um objeto de coluna. Prova o Caso 4 de tolerância: um servidor mais novo
adicionando campos opcionais não pode quebrar um cliente mais antigo.

### `validation-error.json`
Não é uma tela — é o corpo exato de um 422, no formato
`{"error": "validation_failed", "fields": {"name": ["required"], ...}}`.
Dá ao teste de vínculo erro↔campo do renderer (plano 07-05) uma entrada fixa:
o `form` component mapeia as chaves de `fields` diretamente para as `key`s
dos próprios campos e renderiza os erros inline.

## Requisitos que esta corpus atende

- **Vocabulário fechado de 7 tipos**: `all-components.json`.
- **Tolerância a tipo/campo desconhecido**: as três fixtures
  `unknown-*`, uma por caso de tolerância documentado.
- **Corpus de fixtures douradas testável sem servidor rodando**:
  esta pasta inteira, descoberta automaticamente por `os.ReadDir` em
  `TestFixtureConformance` — uma fixture nova não exige editar o teste.
