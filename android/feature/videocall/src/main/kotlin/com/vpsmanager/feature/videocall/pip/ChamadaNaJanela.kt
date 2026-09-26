package com.vpsmanager.feature.videocall.pip

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.vpsmanager.feature.videocall.CallUiState
import com.vpsmanager.feature.videocall.VideoTile
import org.webrtc.EglBase

/**
 * The call inside the floating window.
 *
 * ## Why it is not the full screen shrunk down
 *
 * In a window of about 200 dp, a 48 dp button covers a quarter of the usable
 * area and nobody hits it; a participant's name turns into a smudge and a grid
 * of four videos turns into four stamps. The right content here is **one** video
 * and **one** status line. Anyone who wants to control the call expands the
 * window — and expanding is one tap.
 *
 * ## What the status line says, and why
 *
 * This window is the only place where a call dropping can be announced while the
 * app is not in front: a notification vanishes among twenty others, whereas the
 * little window sits on top of whatever the person is looking at. That is why it
 * always shows:
 *
 * - **how many** are on the call (if it drops to 0, the call has emptied out and
 *   the person needs to know without opening anything);
 * - **microphone muted**, which is the most expensive mistake on a call and the
 *   one state people change by accident.
 *
 * ## Which video is shown
 *
 * The first REMOTE one, with the local video only as a last resort. In a little
 * window, seeing your own face tells you nothing — seeing whoever is on the
 * other side does. With no remote video yet, the local one beats a black
 * rectangle, because it proves the camera is alive.
 */
@Composable
fun ChamadaNaJanela(
    state: CallUiState.InCall,
    eglBaseContext: EglBase.Context,
    modifier: Modifier = Modifier,
) {
    val remoto = state.remoteTracks.values.firstOrNull { it != null }
    val trilha = remoto ?: state.localTrack

    Box(modifier = modifier.fillMaxSize().background(Color.Black)) {
        VideoTile(
            track = trilha,
            eglBaseContext = eglBaseContext,
            modifier = Modifier.fillMaxSize(),
        )

        Row(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                // A translucent band rather than bare text: a white label over
                // bright video disappears exactly when somebody turns a light
                // on.
                .background(Color.Black.copy(alpha = 0.45f))
                .padding(horizontal = 6.dp, vertical = 3.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (!state.micEnabled) {
                // A red dot plus the word: colour alone serves neither a
                // colour-blind viewer nor a screen reader, and it is the same
                // rule the rest of the app already follows.
                Box(
                    modifier = Modifier
                        .size(6.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.error),
                )
                Text(
                    text = " muted",
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                )
                Text(text = " · ", style = MaterialTheme.typography.labelSmall, color = Color.White)
            }
            Text(
                text = textoDeParticipantes(state.remoteTracks.size),
                style = MaterialTheme.typography.labelSmall,
                color = Color.White,
            )
        }
    }
}

/**
 * The participants sentence.
 *
 * "alone in the room" and not "0 participants": zero is a number the person has
 * to interpret; the sentence is already the conclusion. And it is the state that
 * matters most to catch at a glance in the little window — it means the
 * conversation is over and nobody said so.
 */
internal fun textoDeParticipantes(remotos: Int): String = when (remotos) {
    0 -> "alone in the room"
    1 -> "1 in the call"
    else -> "$remotos in the call"
}
