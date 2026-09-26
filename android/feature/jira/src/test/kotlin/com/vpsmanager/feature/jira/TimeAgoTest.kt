package com.vpsmanager.feature.jira

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneOffset

class TimeAgoTest {

    private val now: OffsetDateTime = OffsetDateTime.of(2026, 9, 9, 12, 0, 0, 0, ZoneOffset.UTC)

    private fun hoursAgo(hours: Long) = now.minusHours(hours)
        .format(java.time.format.DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSZ"))

    @Test
    fun `o formato do Jira tem fuso SEM dois-pontos, e e este que precisa funcionar`() {
        // "2026-09-09T09:00:00.000+0000" — time.RFC3339 on its own rejects it.
        assertEquals("3 h ago", timeAgo("2026-09-09T09:00:00.000+0000", now))
    }

    @Test
    fun `o formato ISO com dois-pontos tambem passa`() {
        assertEquals("3 h ago", timeAgo("2026-09-09T09:00:00Z", now))
    }

    @Test
    fun `a escala vai de minutos a anos`() {
        assertEquals("30 min ago", timeAgo(now.minusMinutes(30).toString(), now))
        assertEquals("5 h ago", timeAgo(hoursAgo(5), now))
        assertEquals("3 d ago", timeAgo(now.minusDays(3).toString(), now))
        assertEquals("2 wk ago", timeAgo(now.minusDays(15).toString(), now))
        assertEquals("3 mo ago", timeAgo(now.minusDays(100).toString(), now))
        assertEquals("2 y ago", timeAgo(now.minusDays(800).toString(), now))
    }

    @Test
    fun `data ilegivel vira vazio, nunca erro — o cartao continua util`() {
        assertEquals("", timeAgo("ontem de tarde", now))
        assertEquals("", timeAgo(null, now))
        assertEquals("", timeAgo("", now))
    }

    @Test
    fun `carimbo no futuro nao vira numero negativo`() {
        // The device's clock may be running behind the server's.
        assertEquals("now", timeAgo(now.plusHours(2).toString(), now))
    }
}

class InitialsTest {

    @Test
    fun `duas palavras dao duas iniciais`() {
        assertEquals("SR", initials("Sam Rivera"))
    }

    @Test
    fun `nome do meio nao entra — o ultimo sobrenome identifica melhor`() {
        assertEquals("SR", initials("Sam Lee Rivera"))
    }

    @Test
    fun `uma palavra da uma inicial`() {
        assertEquals("S", initials("sam"))
    }

    @Test
    fun `sem nome, um ponto de interrogacao — nunca um circulo vazio`() {
        assertEquals("?", initials(null))
        assertEquals("?", initials("   "))
    }
}

class DueLabelTest {

    private val today: LocalDate = LocalDate.of(2026, 9, 9)

    @Test
    fun `vencido e diferente de vencendo, e e essa diferenca que muda o dia`() {
        assertEquals("venceu", dueLabel("2026-09-01", today))
        assertEquals("due today", dueLabel("2026-09-09", today))
        assertEquals("due tomorrow", dueLabel("2026-09-10", today))
        assertEquals("due in 5 d", dueLabel("2026-09-14", today))
    }

    @Test
    fun `sem data de vencimento, o cartao nao ganha linha`() {
        assertEquals("", dueLabel(null, today))
        assertEquals("", dueLabel("nunca", today))
    }
}
