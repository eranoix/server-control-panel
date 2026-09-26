# Copia adaptada de vendor/HDiffPatch/builds/android_ndk_jni_mk/Application.mk.
#
# Diferencas em relacao ao upstream:
#   - APP_PLATFORM sobe de android-16 para android-34: e o minSdk deste
#     projeto (libs.versions.toml -- frota 100% Android 14+). Deixar 16 nao
#     quebraria (o NDK r27 elevaria para 21 com aviso), so mentiria sobre o
#     piso e deixaria o compilador emitir compatibilidade que ninguem usa.
#   - APP_ABI vem do AGP (abiFilters em build.gradle.kts), nao daqui: com
#     externalNativeBuild o Gradle e quem manda a lista, e ter duas fontes
#     divergentes ja e um bug esperando.
#
# Preservado do upstream, e importante:
#   -Wl,-z,max-page-size=16384 -- Android 15+ pode rodar com paginas de 16 KB;
#   sem este alinhamento a .so simplesmente nao carrega nesses aparelhos.
#   -s / --gc-sections / -flto -- e o que mantem o .so na casa dos 100 KB.

APP_PLATFORM := android-34
APP_CFLAGS += -s -Wno-error=format-security
APP_CFLAGS += -fvisibility=hidden -fvisibility-inlines-hidden
APP_CFLAGS += -ffunction-sections -fdata-sections
APP_LDFLAGS += -s -Wl,--gc-sections,--as-needed
APP_LDFLAGS += -flto -Wl,-z,max-page-size=16384
APP_BUILD_SCRIPT := Android.mk
