package dev.servercontrolpanel.app.licenses

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

private data class OssLicense(
    val name: String,
    val coordinates: String,
    val license: String,
    val sourceUrl: String,
)

private val ossLicenses = listOf(
    OssLicense(
        name = "sora-editor",
        coordinates = "io.github.rosemoe:editor / language-textmate",
        license = "LGPL-2.1-or-later",
        sourceUrl = "https://github.com/Rosemoe/sora-editor",
    ),
    OssLicense(
        name = "HDiffPatch",
        coordinates = "sisong/HDiffPatch v5.1.3 (native, :patch-engine)",
        license = "MIT",
        sourceUrl = "https://github.com/sisong/HDiffPatch",
    ),
    OssLicense(
        name = "zstd",
        coordinates = "sisong/zstd (decompressor bundled in libhpatchz.so)",
        license = "BSD-3-Clause",
        sourceUrl = "https://github.com/sisong/zstd",
    ),
    OssLicense(
        name = "LZMA SDK",
        coordinates = "sisong/lzma (lzma2 decompressor in libhpatchz.so)",
        license = "Public domain",
        sourceUrl = "https://github.com/sisong/lzma",
    ),
    OssLicense(
        name = "xxHash",
        coordinates = "sisong/xxHash (patch format checksum)",
        license = "BSD-2-Clause",
        sourceUrl = "https://github.com/sisong/xxHash",
    ),
    OssLicense(
        name = "libmd5",
        coordinates = "sisong/libmd5 (patch format checksum)",
        license = "zlib/Aladdin",
        sourceUrl = "https://github.com/sisong/libmd5",
    ),
)

@Composable
fun OssLicensesScreen(modifier: Modifier = Modifier) {
    LazyColumn(modifier = modifier.fillMaxSize().padding(16.dp)) {
        items(items = ossLicenses, key = { it.name }) { license ->
            OssLicenseCard(license)
        }
    }
}

@Composable
private fun OssLicenseCard(license: OssLicense) {
    Card(modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(text = license.name, style = MaterialTheme.typography.titleMedium)
            Text(text = license.coordinates, style = MaterialTheme.typography.bodySmall)
            Text(text = "License: ${license.license}", style = MaterialTheme.typography.bodyMedium)
            Text(text = license.sourceUrl, style = MaterialTheme.typography.bodySmall)
        }
    }
}
