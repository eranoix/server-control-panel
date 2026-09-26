# sora-editor (io.github.rosemoe, LGPL-2.1-or-later) must never be obfuscated,
# inlined or stripped by a consumer's R8 pass. LGPL's linking permission for a
# differently-licensed app depends on the library staying replaceable -- a
# user relinking a modified sora-editor build has to find the same class/
# method surface this app calls into. See OssLicensesScreen for the full
# attribution and source-availability notice.
-keep class io.github.rosemoe.** { *; }
-dontwarn io.github.rosemoe.**
