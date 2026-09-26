# Regras de manutencao do R8 — :app
#
# Este arquivo so vale para o buildType `release`, onde `isMinifyEnabled` e
# `isShrinkResources` estao ligados (ver app/build.gradle.kts). Ele NAO e um
# proguard-rules.pro generico copiado da internet: cada regra abaixo foi
# derivada de uma dependencia real deste projeto, e cada uma diz o que
# quebraria EM RUNTIME sem ela — porque e assim que uma regra errada se
# manifesta. O build passa; o app crasha depois, no aparelho do operador.
#
# O que NAO esta aqui, e por que (verificado no configuration.txt que o R8
# escreve em app/build/outputs/mapping/release/ — ele lista TODA regra que o
# R8 recebeu, inclusive as embutidas nos AARs):
#
#   - org.webrtc.**            o AAR io.getstream:stream-webrtc-android traz
#                              `-keep class org.webrtc.** { *; }` no proprio
#                              proguard.txt. Duplicar aqui seria ruido.
#   - okhttp3/okio             okhttp-4.12.0.jar traz META-INF/proguard/okhttp3.pro.
#   - kotlinx.serialization    kotlinx-serialization-core-jvm traz
#                              META-INF/proguard/kotlinx-serialization-common.pro,
#                              que preserva os `Companion` e os `serializer()`.
#                              Os NOMES DE CAMPO nao precisam ser preservados:
#                              o plugin do compilador gera uma classe
#                              `<Modelo>$$serializer` com o SerialDescriptor e
#                              os nomes de wire ja embutidos, entao renomear o
#                              campo Kotlin nao muda o JSON.
#   - Compose, WorkManager,    todos AARs do AndroidX/Google, que embarcam as
#     Credential Manager,      proprias regras via proguard.txt no AAR.
#     CameraX, Media3, Coil,
#     Firebase, Tink
#   - componentes do manifesto o AGP gera automaticamente um `-keep` para toda
#     (Activity/Service/       classe citada no AndroidManifest.xml fundido
#     Receiver/Provider/       (aapt_rules.txt). Isso cobre MainActivity,
#     Application)             ShareTargetActivity, VpsmConnectionService,
#                              CallForegroundService, VpsFirebaseMessagingService,
#                              NotificationActionReceiver, FileProvider e
#                              VpsManagerApplication — todos instanciados pelo
#                              SISTEMA, pelo nome, e todos ja protegidos.
#                              Conferido em mapping.txt: nenhum deles e renomeado.
#   - io.github.rosemoe.**     ja vem de feature/files/consumer-rules.pro
#     (sora-editor)            (obrigacao de LGPL, nao de funcionamento). O
#                              proguard.txt dentro do AAR do sora-editor esta
#                              VAZIO (0 bytes) — a regra do projeto e a unica.
#
# Regra de higiene: nao acrescente nada aqui sem antes confirmar, no
# configuration.txt, que a regra ainda nao existe. Regra redundante e divida:
# ninguem depois sabe se pode remover.


# ---------------------------------------------------------------------------
# JNI — terminal-engine (o risco numero um deste APK)
# ---------------------------------------------------------------------------
# libterminal_engine_jni.so exporta simbolos com o NOME totalmente qualificado
# da classe Java embutido:
#
#   Java_com_vpsmanager_terminalengine_TerminalEngine_nativeResize
#
# A ligacao e feita pelo dalvik por casamento de nome na hora da primeira
# chamada (nao ha RegisterNatives no shim — conferido em
# terminal-engine/src/main/cpp/ghostty_jni.cpp, cujo JNI_OnLoad so guarda o
# JavaVM). Se o R8 renomear a classe `TerminalEngine` para `a.b.c` ou renomear
# `nativeResize` para `a`, o simbolo procurado deixa de existir e a chamada
# estoura `UnsatisfiedLinkError` — nao no boot, mas no instante em que o
# usuario ABRE O TERMINAL, que e o pior lugar possivel para descobrir isso.
#
# Os oito metodos `external` estao declarados no `companion object`, mas com
# `@JvmStatic`: o Kotlin os emite como `private static final native` na classe
# EXTERNA (conferido com javap — o Companion so tem os encaminhadores nao
# nativos). Por isso a regra mira `TerminalEngine`, nao `TerminalEngine$Companion`.
#
# `includedescriptorclasses` mantem tambem os tipos das assinaturas: hoje sao
# todos tipos de plataforma (long/int/byte[]/ByteBuffer), mas se algum dia um
# metodo nativo passar a receber um tipo do app, a regra continua correta
# sozinha em vez de virar uma armadilha silenciosa.
-keepclasseswithmembernames,includedescriptorclasses class com.vpsmanager.terminalengine.TerminalEngine {
    native <methods>;
}

# Rede de seguranca para QUALQUER metodo nativo futuro, em qualquer modulo.
# O arquivo padrao do AGP (proguard-android-optimize.txt) ja traz uma regra
# equivalente, mas aquele arquivo nao esta sob controle deste projeto e o
# unico jeito de descobrir que ele mudou seria um UnsatisfiedLinkError no
# aparelho do operador. Declarar aqui torna a garantia explicita e local.
-keepclasseswithmembernames,includedescriptorclasses class * {
    native <methods>;
}


# ---------------------------------------------------------------------------
# JNI reverso — excecoes que o codigo nativo constroi
# ---------------------------------------------------------------------------
# O shim faz `env->FindClass("java/lang/IllegalStateException")` para reportar
# handle invalido. E classe de plataforma (nunca entra no dex, nunca e
# renomeada), entao nao precisa de `-keep`. Esta anotado aqui para que a
# proxima pessoa que ler o ghostty_jni.cpp e vir o FindClass nao ache que
# faltou uma regra: se um dia o shim passar a lancar uma excecao DO APP,
# ai sim ela precisara de `-keep` explicito, porque a busca e por nome.


# ---------------------------------------------------------------------------
# Diagnostico de crash — sem isso o mapping.txt nao basta
# ---------------------------------------------------------------------------
# SourceFile/LineNumberTable sao os atributos que fazem um stack trace ter
# numero de linha. Sem eles, mesmo com o mapping.txt em maos, o retrace
# devolve `Unknown Source` e um relatorio de crash do operador vira adivinhacao.
# `-renamesourcefileattribute` troca o nome do arquivo por um literal para nao
# vazar a arvore de fontes, mantendo as linhas.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# Assinaturas genericas e anotacoes de tempo de execucao. O
# `kotlinx-serialization-common.pro` ja pede RuntimeVisibleAnnotations, mas
# `Signature` e `InnerClasses` nao: sem eles, um `TypeToken`-like ou qualquer
# leitura de tipo parametrizado em runtime (o Json do kotlinx faz isso ao
# resolver serializers de `List<Foo>`) perde a informacao de tipo e falha na
# desserializacao com um erro que nao aponta para lugar nenhum.
-keepattributes Signature,InnerClasses,EnclosingMethod,*Annotation*
