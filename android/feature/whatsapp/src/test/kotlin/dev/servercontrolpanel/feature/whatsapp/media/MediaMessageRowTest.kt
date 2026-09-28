package dev.servercontrolpanel.feature.whatsapp.media

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.media3.exoplayer.ExoPlayer
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.core.model.WhatsAppMedia
import dev.servercontrolpanel.core.model.WhatsAppMessage
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class MediaMessageRowTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun message(type: String, media: WhatsAppMedia?) = WhatsAppMessage(
        id = "1",
        chatJid = "5511999@s.whatsapp.net",
        fromMe = false,
        sender = null,
        text = null,
        type = type,
        ts = 1L,
        ack = 0,
        quotedId = null,
        media = media,
        reactions = emptyList(),
    )

    @Test
    fun `a document message shows its filename and a human-readable size`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        val player = ExoPlayer.Builder(context).build()
        composeRule.setContent {
            MediaMessageRow(
                message = message(
                    type = "document",
                    media = WhatsAppMedia(
                        url = "/media/contract.pdf",
                        mimeType = "application/pdf",
                        filename = "contract.pdf",
                        size = 1_572_864L,
                        duration = null,
                        width = null,
                        height = null,
                    ),
                ),
                serverBaseUrl = "https://servercontrolpanel.example",
                imageLoader = MediaCache.imageLoader(context),
                dataSourceFactory = MediaCache.dataSourceFactory(),
                sharedPlayer = player,
                onOpenImage = {},
                onOpenVideo = {},
            )
        }

        composeRule.onNodeWithText("contract.pdf").assertExists()
        composeRule.onNodeWithText("1.5 MB").assertExists()
        player.release()
    }

    @Test
    fun `a document with no filename falls back to a generic name instead of a blank row`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        val player = ExoPlayer.Builder(context).build()
        composeRule.setContent {
            MediaMessageRow(
                message = message(
                    type = "document",
                    media = WhatsAppMedia(
                        url = "/media/x",
                        mimeType = null,
                        filename = null,
                        size = null,
                        duration = null,
                        width = null,
                        height = null,
                    ),
                ),
                serverBaseUrl = null,
                imageLoader = MediaCache.imageLoader(context),
                dataSourceFactory = MediaCache.dataSourceFactory(),
                sharedPlayer = player,
                onOpenImage = {},
                onOpenVideo = {},
            )
        }

        composeRule.onNodeWithText("file").assertExists()
        player.release()
    }

    @Test
    fun `an unrecognized message type shows the unsupported-media label instead of crashing`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        val player = ExoPlayer.Builder(context).build()
        composeRule.setContent {
            MediaMessageRow(
                message = message(
                    type = "sticker",
                    media = WhatsAppMedia(
                        url = "/media/x.webp",
                        mimeType = "image/webp",
                        filename = "x.webp",
                        size = 1024L,
                        duration = null,
                        width = null,
                        height = null,
                    ),
                ),
                serverBaseUrl = "https://servercontrolpanel.example",
                imageLoader = MediaCache.imageLoader(context),
                dataSourceFactory = MediaCache.dataSourceFactory(),
                sharedPlayer = player,
                onOpenImage = {},
                onOpenVideo = {},
            )
        }

        composeRule.onNodeWithText("[unsupported media]").assertExists()
        player.release()
    }
}
