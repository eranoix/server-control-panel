package dev.servercontrolpanel.feature.terminal.keys

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue

enum class ModifierArmState {
    OFF,
    ARMED,
    LOCKED,
    ;

    fun next(): ModifierArmState = when (this) {
        OFF -> ARMED
        ARMED -> LOCKED
        LOCKED -> OFF
    }
}

class PendingModifiers {
    var ctrl: ModifierArmState by mutableStateOf(ModifierArmState.OFF)
        private set

    var alt: ModifierArmState by mutableStateOf(ModifierArmState.OFF)
        private set

    fun tapCtrl() {
        ctrl = ctrl.next()
    }

    fun tapAlt() {
        alt = alt.next()
    }

    fun isCtrlPending(): Boolean = ctrl != ModifierArmState.OFF

    fun isAltPending(): Boolean = alt != ModifierArmState.OFF

    fun consumeAfterKeystroke() {
        if (ctrl == ModifierArmState.ARMED) ctrl = ModifierArmState.OFF
        if (alt == ModifierArmState.ARMED) alt = ModifierArmState.OFF
    }
}
