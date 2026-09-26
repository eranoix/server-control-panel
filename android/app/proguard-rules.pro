# R8 keep rules for :app (release build, where minify and resource shrinking are on).
# Each rule comes from a real dependency and would break at RUNTIME without it: the build
# passes and the app crashes later on the device.
#
# Not here, because they already ship with the dependency (check configuration.txt in
# app/build/outputs/mapping/release/, which lists every rule R8 received):
#   - org.webrtc.**: the stream-webrtc-android AAR keeps it.
#   - okhttp3/okio: okhttp ships META-INF/proguard/okhttp3.pro.
#   - kotlinx.serialization: ships its own rules; field names need no keep because the
#     generated serializer embeds the wire names.
#   - Compose, WorkManager, Credential Manager, CameraX, Media3, Coil, Firebase, Tink.
#   - Manifest components: AGP generates a keep for every class in the merged manifest.
#   - io.github.rosemoe.** (sora-editor): kept by feature/files/consumer-rules.pro.
#
# Do not add a rule without confirming in configuration.txt that it does not exist yet.


# JNI for terminal-engine. libterminal_engine_jni.so exports symbols with the fully
# qualified class name (Java_com_vpsmanager_terminalengine_TerminalEngine_nativeResize)
# and is bound by name on first call (no RegisterNatives). Renaming the class or methods
# causes UnsatisfiedLinkError when the user opens the terminal.
# The `external` methods are @JvmStatic in the companion, so Kotlin emits them on the outer
# class; that is why the rule targets TerminalEngine and not TerminalEngine$Companion.
# includedescriptorclasses also keeps signature types in case an app type is ever passed.
-keepclasseswithmembernames,includedescriptorclasses class com.vpsmanager.terminalengine.TerminalEngine {
    native <methods>;
}

# Safety net for any future native method in any module. The default AGP file has an
# equivalent rule, but it is not under this project's control.
-keepclasseswithmembernames,includedescriptorclasses class * {
    native <methods>;
}


# The native shim uses FindClass("java/lang/IllegalStateException"), a platform class that
# needs no keep. If it ever throws an app exception class, that class needs an explicit keep
# because the lookup is by name.


# SourceFile/LineNumberTable keep line numbers in stack traces so retrace works;
# -renamesourcefileattribute hides the source tree while keeping the lines.
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# Generic signatures and inner classes are needed for runtime reads of parameterized types
# (kotlinx Json resolving serializers for List<Foo>); without them deserialization fails.
-keepattributes Signature,InnerClasses,EnclosingMethod,*Annotation*
