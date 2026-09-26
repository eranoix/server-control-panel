package com.vpsmanager.feature.admin

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.Email
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Warning
import androidx.compose.ui.graphics.vector.ImageVector
import com.vpsmanager.designsystem.VpsmIcons

/**
 * The icon for a section of Administration.
 *
 * ## Why this exists: the group's initial identified nothing
 *
 * The launcher showed the GROUP'S INITIAL in a circle. The owner sent a photo
 * of the result: five Docker sections became five identical **D**s, `Job queue`
 * became an **A** (for Automation) and — worst of all — `System` and
 * `Security` are both **S**, so the two families it matters most to tell apart
 * on a server dashboard ended up with the same symbol.
 *
 * A symbol that does not distinguish is worse than none: it occupies exactly
 * the spot where the eye looks for the difference, and teaches you to ignore
 * that spot.
 *
 * ## How the icon is chosen
 *
 * By the **section id**, which is `group.name` and comes from the server —
 * never by the label, which is text for humans and gets reworded without
 * notice. The lookup is by word inside the id, from the most specific to the
 * most generic: `docker.images` matches "image" before it matches "docker",
 * because an image and a container deserve different symbols.
 *
 * ## The fallback is deliberate
 *
 * A section the server invents tomorrow and this map does not know gets the
 * generic cog — and remains reachable, with its label and group spelled out
 * underneath. It is the same promise server-driven UI makes: **a new screen
 * appears without an app release**. A map that demanded a new entry for every
 * section would break precisely that, and the icon is not worth the price.
 */
internal fun iconeDaSecao(sectionId: String): ImageVector {
    val id = sectionId.lowercase()
    return when {
        // ── Docker: from the most specific to the most generic ───────────
        // Image, container and compose were THREE IDENTICAL LAYERS on screen —
        // the repetition the owner photographed. Now: the image is layers (which
        // is what it is), the container is a box (what runs), and compose is the
        // assembly cog (what orchestrates the two).
        "image" in id -> VpsmIcons.Camadas
        "volume" in id -> VpsmIcons.Disco
        "network" in id || "rede" in id -> VpsmIcons.Troca
        "compose" in id -> Icons.Filled.Build
        "prune" in id || "limpeza" in id -> Icons.Filled.Refresh
        "docker" in id || "container" in id -> VpsmIcons.Caixa

        // ── System and resources ─────────────────────────────────────────
        "metric" in id || "metrica" in id -> VpsmIcons.Velocimetro
        // Processes is NOT the speedometer: measuring the machine and listing
        // what runs on it are different questions, and the same symbol confused
        // them.
        "process" in id || "processo" in id -> VpsmIcons.Cpu
        "memor" in id || "ram" in id -> VpsmIcons.Memoria
        "disk" in id || "disco" in id || "storage" in id -> VpsmIcons.Disco
        "systemd" in id || "unit" in id || "servic" in id -> Icons.Filled.Build
        // A port is where you CONNECT; the network is what PASSES THROUGH. They
        // were the same arrow.
        "port" in id -> VpsmIcons.Tomada
        "histor" in id -> VpsmIcons.Cronometro

        // ── Security ─────────────────────────────────────────────────────
        // THIS was the worst repetition: a padlock on four sections and a person
        // on three. Security is the family where distinguishing matters most, and
        // it was the one that distinguished least. Now each one says what it does:
        //   shield  = blocks what comes from outside  key   = keeps a secret
        //   globe   = resolves names on the network   chart = measures usage
        //   person  = an ACCOUNT                      phone = a DEVICE
        //   monitor = a LIVE session
        "firewall" in id || "ufw" in id -> VpsmIcons.Escudo
        "secret" in id || "vault" in id || "cofre" in id -> VpsmIcons.Chave
        "user" in id || "usuario" in id -> Icons.Filled.Person
        "session" in id || "sessao" in id -> VpsmIcons.SessaoAtiva
        "audit" in id || "auditoria" in id -> Icons.Filled.Warning
        "device" in id || "aparelho" in id -> VpsmIcons.Celular
        "adguard" in id || "dns" in id -> VpsmIcons.Globo
        "netusage" in id || "uso de rede" in id || "consumo" in id -> VpsmIcons.Grafico
        "securit" in id || "seguranca" in id -> Icons.Filled.Lock

        // ── Operations ───────────────────────────────────────────────────
        "deploy" in id || "entrega" in id -> VpsmIcons.Entrega
        "queue" in id || "fila" in id -> Icons.Filled.List
        "schedul" in id || "agenda" in id || "cron" in id -> Icons.Filled.DateRange
        "backup" in id -> VpsmIcons.Disco
        "job" in id -> Icons.Filled.PlayArrow
        "alert" in id || "alerta" in id -> Icons.Filled.Notifications
        "health" in id || "saude" in id -> VpsmIcons.Saude

        // ── Communication and integrations ───────────────────────────────
        "whatsapp" in id || "chat" in id -> VpsmIcons.Chat
        "mail" in id || "email" in id || "gmail" in id -> Icons.Filled.Email
        "jira" in id -> Icons.Filled.Info
        "terminal" in id || "shell" in id -> VpsmIcons.Terminal
        "file" in id || "arquivo" in id -> VpsmIcons.Folder

        // A section this map does not know yet. See the KDoc: the generic one
        // is the price of server-driven UI being able to bring a new screen
        // without a release.
        else -> Icons.Filled.Settings
    }
}
