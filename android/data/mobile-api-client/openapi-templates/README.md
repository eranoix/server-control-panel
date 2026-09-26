# Templates sobrescritos do openapi-generator

Este diretório sobrescreve templates do `org.openapi.generator` (fixado em
`7.25.0`, ver `gradle/libs.versions.toml`). O gerador usa os templates
embutidos para tudo que **não** estiver aqui — então só existe arquivo aqui
quando há defeito comprovado no template original.

Ao subir a versão do gerador: reveja cada arquivo daqui, confira se o defeito
foi corrigido upstream e, se foi, **apague a sobrescrita** em vez de carregar
divergência sem motivo. Copiar o template novo e reaplicar a correção à mão é
o pior dos mundos: some o registro de por que ele existe.

## `libraries/jvm-okhttp/api.mustache`

Cópia byte-idêntica do template 7.25.0 **mais uma linha**:
`import kotlinx.serialization.encodeToString`.

**Defeito:** o bloco `{{^multiplatform}}` importa só `SerialName` e
`Serializable`; o bloco `{{#multiplatform}}`, ao lado, faz
`import kotlinx.serialization.*`. Sem a extensão importada, a chamada
`encodeToString<T>(obj)` que o próprio template emite para cada parte de
formulário não-arquivo enxerga apenas a sobrecarga de **dois** parâmetros
(`SerializationStrategy<T>, T`) e não compila.

**Sintoma:** dezenas de
`Argument type mismatch: actual type is 'String', but 'SerializationStrategy<String>' was expected`
em `MobileApi.kt`/`WhatsappApi.kt`, quebrando o cliente gerado inteiro — e
portanto todo módulo Kotlin a jusante. Aparece só quando existe pelo menos um
endpoint `multipart/form-data` no spec.

**Não é questão de versão:** reproduzido isoladamente com
`kotlinx-serialization-json` 1.7.3 e 1.4.1 (idêntico nas duas), e o `master`
não lançado do gerador tem o mesmo bloco. 7.25.0 é a última release publicada.

## `libraries/jvm-okhttp/infrastructure/ApiClient.kt.mustache`

Cópia byte-idêntica do template 7.25.0 **mais um guarda** em `request()`:
`updateAuthParams(requestConfig)` só roda quando
`requestConfig.requiresAuthentication` é `true` (procure por `GUARDA VPSM`).

**Defeito:** o template gera o campo `requiresAuthentication` em cada
`RequestConfig` — derivado do `security` de cada operação no spec — e depois
**nunca o consulta**: `updateAuthParams` é chamado em toda requisição, inclusive
nas operações que declaram explicitamente não ter segurança.

**Sintoma neste projeto:** o BFF declara `bearerAuth` só nas rotas protegidas
(ver `internal/mobilebff/security.go`) e o app espelha o access token da sessão
em `ApiClient.accessToken` (`SessionManager`, para `MediaNetwork` e o WebSocket
do WhatsApp). Sem o guarda, `POST /auth/login`, `/auth/refresh`, `/auth/pair` e
`/auth/passkey/*` — que ficam FORA de `auth.Middleware` porque acontecem antes
de existir sessão — passariam a levar `Authorization: Bearer <token da sessão
anterior>`. Nenhuma delas lê o header no servidor, então não quebra nada; mas é
mandar credencial para quem não pediu e diverge do que o
`AuthTokenInterceptor` já faz (ele pula essas mesmas rotas por sufixo).

**Quem monta o header, afinal:** rota protegida ⇒ o cliente gerado monta a
partir de `ApiClient.accessToken` e o `AuthTokenInterceptor` **sobrescreve**
com o token de `SessionManager` (`Request.header()` substitui, não acumula —
nunca sai duplicado); o interceptor continua sendo a fonte da verdade, porque
só ele sabe renovar em 401 e repetir. Rota pública ⇒ ninguém monta header.

## Defeito CONHECIDO e NÃO corrigido aqui — leia antes de adicionar multipart

O mesmo template força cast não-nulo (`obj as kotlin.String`) em **toda** parte
de formulário não-arquivo, **ignorando a opcionalidade do campo**. Passar `null`
para uma parte opcional lança `ClassCastException` dentro do método gerado, em
vez de omitir a parte.

Hoje isso é contornado no ponto de chamada: `WhatsAppRepository.uploadMedia`
converte `null` para `""` antes de entrar no cliente gerado. É seguro *naquele*
endpoint porque o `FormValue` do Go já devolve `""` para campo nunca enviado —
mesma forma de requisição, não mudança de comportamento.

**Isso não vale automaticamente para um endpoint novo.** Se o seu endpoint
precisar distinguir "campo ausente" de "campo vazio", o contorno de string
vazia está errado e a correção tem que vir para cá — guardando o cast com uma
checagem de nulo e omitindo a parte. Note que mexer nisso muda o comportamento
de fio de **todo** multipart gerado, então mude com teste que prove a diferença.
