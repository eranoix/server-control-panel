package com.vpsmanager.data.update

import androidx.core.content.FileProvider

/**
 * An empty subclass of [FileProvider], and not `androidx.core.content.FileProvider`
 * directly — the difference is mandatory, not cosmetic.
 *
 * Manifest merging identifies each `<provider>` by its `android:name`. With
 * two modules declaring the SAME name, the merger takes them for the same node
 * and complains that `android:authorities` "is also present" with another
 * value, bringing the whole build down. This project already has two
 * FileProviders (`:feature-whatsapp` and `:feature-terminal`), and this is the
 * third; each with its own class name, its own authority and its own paths file.
 *
 * The suggestion the merger itself offers — `tools:replace="android:authorities"` —
 * fixes the error by DELETING one of the providers. That is exactly what we do
 * not want: what would silently disappear is the ability to attach a file in the
 * terminal or to open WhatsApp media, depending on the merge order.
 */
class AtualizacaoFileProvider : FileProvider()
