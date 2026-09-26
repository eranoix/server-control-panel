package dev.servercontrolpanel.app.update

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.update.UpdateRecovery
import dev.servercontrolpanel.data.update.UpdateState
import dev.servercontrolpanel.data.update.formatDownloadSize
import dev.servercontrolpanel.designsystem.PanelTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The update banner. It must show the size of what is actually downloaded (the patch), not
 * the rebuilt APK, since on a slow connection that decides whether the user updates.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class UpdateBannerTest {

    @get:Rule
    val composeRule = createComposeRule()

    /**
     * Measured sizes: an incremental patch, a full patch and the raw APK. Decimal MB, not MiB,
     * so the number matches what the device shows for the same file.
     */
    @Test
    fun `the size is shown in MB with one decimal and a decimal point`() {
        assertEquals("1.4 MB", formatDownloadSize(1_400_329))
        assertEquals("10.0 MB", formatDownloadSize(10_029_237))
        assertEquals("31.1 MB", formatDownloadSize(31_135_416))
    }

    @Test
    fun `below one megabyte the size switches to KB`() {
        assertEquals("819 KB", formatDownloadSize(819_200))
        assertEquals("512 B", formatDownloadSize(512))
    }

    @Test
    fun `the banner announces the version and the download size`() {
        composeRule.setContent {
            PanelTheme {
                UpdateBanner(
                    state = UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true),
                    onUpdateClick = {},
                    onCancelClick = {},
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Version 0.1.7 available — 1.4 MB").assertIsDisplayed()
        composeRule.onNodeWithText("Update").assertIsDisplayed()
    }

    @Test
    fun `nothing to do draws no banner`() {
        assertNull(bannerContentFor(UpdateState.Idle))
        assertNull(
            "checking is a background routine; flashing 'checking' on every launch is noise",
            bannerContentFor(UpdateState.Checking),
        )
    }

    @Test
    fun `downloading shows progress and allows cancelling`() {
        val content = bannerContentFor(
            UpdateState.Downloading(versionName = "0.1.7", downloadedBytes = 700_000, totalBytes = 1_400_329),
        )

        checkNotNull(content)
        assertEquals(0.5f, content.progress!!, 0.01f)
        assertEquals(listOf(UpdateBannerAction.Cancel), content.actions)
        assertTrue(content.text, content.text.contains("700 KB of 1.4 MB"))
    }

    @Test
    fun `applying and installing show a spinner and no button, since nothing can be cancelled`() {
        val applying = checkNotNull(bannerContentFor(UpdateState.Applying("0.1.7")))
        assertTrue(applying.spinner)
        assertTrue(applying.actions.isEmpty())

        val installing = checkNotNull(bannerContentFor(UpdateState.Installing("0.1.7")))
        assertTrue(installing.spinner)
        assertTrue(installing.actions.isEmpty())
    }

    @Test
    fun `low storage offers to free space and to try again`() {
        val content = checkNotNull(
            bannerContentFor(
                UpdateState.Failed(
                    "Not enough space: free 5.5 MB and try again.",
                    canRetry = true,
                    recovery = UpdateRecovery.FREE_SPACE,
                ),
            ),
        )

        assertEquals(
            listOf(UpdateBannerAction.Recover("Free up space", UpdateRecovery.FREE_SPACE), UpdateBannerAction.Retry),
            content.actions,
        )
        assertTrue(content.text.contains("5.5 MB"))
    }

    @Test
    fun `denied unknown sources offers the shortcut to the toggle`() {
        val content = checkNotNull(
            bannerContentFor(UpdateState.Failed("permission", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES)),
        )

        assertTrue(content.actions.first() is UpdateBannerAction.Recover)
        assertEquals("Allow", content.actions.first().label)
    }

    @Test
    fun `blocked sideload does not offer retry, which would hit the same wall`() {
        val content = checkNotNull(
            bannerContentFor(UpdateState.Failed("blocked", canRetry = false, recovery = UpdateRecovery.USE_BROWSER)),
        )

        assertEquals(listOf(UpdateBannerAction.Recover("How to install", UpdateRecovery.USE_BROWSER)), content.actions)
    }

    @Test
    fun `a failed install leads to diagnostics, where the full system message fits`() {
        val content = checkNotNull(
            bannerContentFor(
                UpdateState.Failed("Installation failed: ...", canRetry = true, recovery = UpdateRecovery.SHOW_DIAGNOSTICS),
            ),
        )

        assertEquals(
            listOf(UpdateBannerAction.Recover("Diagnostics", UpdateRecovery.SHOW_DIAGNOSTICS), UpdateBannerAction.Retry),
            content.actions,
        )
    }

    @Test
    fun `tapping Update starts the download, not the cancellation`() {
        var updated = 0
        var cancelled = 0
        composeRule.setContent {
            PanelTheme {
                UpdateBanner(
                    state = UpdateState.Available("0.1.7", 1_400_329, incremental = true),
                    onUpdateClick = { updated++ },
                    onCancelClick = { cancelled++ },
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Update").performClick()

        assertEquals(1, updated)
        assertEquals(0, cancelled)
    }

    @Test
    fun `tapping Cancel during the download cancels`() {
        var cancelled = 0
        composeRule.setContent {
            PanelTheme {
                UpdateBanner(
                    state = UpdateState.Downloading("0.1.7", 700_000, 1_400_329),
                    onUpdateClick = {},
                    onCancelClick = { cancelled++ },
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Cancel").performClick()

        assertEquals(1, cancelled)
    }

    @Test
    fun `tapping a failure's recovery reports which recovery was requested`() {
        var requested: UpdateRecovery? = null
        composeRule.setContent {
            PanelTheme {
                UpdateBanner(
                    state = UpdateState.Failed("no permission", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES),
                    onUpdateClick = {},
                    onCancelClick = {},
                    onRecoveryClick = { requested = it },
                )
            }
        }

        composeRule.onNodeWithText("Allow").performClick()

        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, requested)
    }

    /** "Try again" takes the same path as "Update"; the coordinator resumes the partial download. */
    @Test
    fun `try again reuses the update path`() {
        var updated = 0
        composeRule.setContent {
            PanelTheme {
                UpdateBanner(
                    state = UpdateState.Failed("Connection failed.", canRetry = true),
                    onUpdateClick = { updated++ },
                    onCancelClick = {},
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Try again").performClick()

        assertEquals(1, updated)
    }
}
