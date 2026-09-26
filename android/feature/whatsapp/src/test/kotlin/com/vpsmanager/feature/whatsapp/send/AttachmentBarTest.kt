package com.vpsmanager.feature.whatsapp.send

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [AttachmentBar] under Robolectric -- never composed before this.
 * Only the three affordances render/exist here; actually launching a picker
 * or the mic permission flow needs a real activity-result contract host and
 * is left to a real device.
 */
@RunWith(RobolectricTestRunner::class)
class AttachmentBarTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `enabled bar shows the document, photo-video and mic entry points`() {
        composeRule.setContent {
            AttachmentBar(enabled = true, onAttachmentReady = {})
        }

        composeRule.onNodeWithText("📎").assertExists()
        composeRule.onNodeWithText("🖼").assertExists()
        composeRule.onNodeWithText("🎤").assertExists()
    }

    @Test
    fun `disabled bar still renders every affordance, just non-interactive`() {
        composeRule.setContent {
            AttachmentBar(enabled = false, onAttachmentReady = {})
        }

        composeRule.onNodeWithText("📎").assertExists()
        composeRule.onNodeWithText("🖼").assertExists()
        composeRule.onNodeWithText("🎤").assertExists()
    }
}
