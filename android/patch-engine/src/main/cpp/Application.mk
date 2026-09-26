# Adapted copy of vendor/HDiffPatch/builds/android_ndk_jni_mk/Application.mk.
#
# Differences from upstream:
#   - APP_PLATFORM is android-34, this project's minSdk (libs.versions.toml).
#   - APP_ABI comes from AGP (abiFilters in build.gradle.kts), not from here,
#     so there is a single source for the ABI list.
#
# Kept from upstream, and important:
#   -Wl,-z,max-page-size=16384: Android 15+ may use 16 KB pages, and without
#   this alignment the .so does not load on those devices.
#   -s / --gc-sections / -flto keep the .so around 100 KB.

APP_PLATFORM := android-34
APP_CFLAGS += -s -Wno-error=format-security
APP_CFLAGS += -fvisibility=hidden -fvisibility-inlines-hidden
APP_CFLAGS += -ffunction-sections -fdata-sections
APP_LDFLAGS += -s -Wl,--gc-sections,--as-needed
APP_LDFLAGS += -flto -Wl,-z,max-page-size=16384
APP_BUILD_SCRIPT := Android.mk
