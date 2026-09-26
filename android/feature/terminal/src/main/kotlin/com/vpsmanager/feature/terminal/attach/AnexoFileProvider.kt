package com.vpsmanager.feature.terminal.attach

import androidx.core.content.FileProvider

/**
 * An empty subclass of [FileProvider], and not `androidx.core.content.FileProvider`
 * directly — the difference is mandatory, not cosmetic.
 *
 * Manifest merging identifies each `<provider>` by its `android:name`. With
 * two modules declaring the SAME name (`:feature-whatsapp` already declares a
 * FileProvider for opening downloaded documents), the merger takes them to be
 * the same node and complains that `android:authorities` is "also present"
 * with a different value — the whole `:app` build fails. Giving this provider
 * a class name of its own turns the two into distinct nodes, each with its own
 * authority and its own `file_paths`, with no `tools:replace` at all (which
 * would resolve the error by ERASING one of the two — exactly what is not
 * wanted).
 */
class AnexoFileProvider : FileProvider()
