package com.vpsmanager.feature.jira

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneOffset

class QuandoFoiTest {

    private val agora: OffsetDateTime = OffsetDateTime.of(2026, 9, 9, 12, 0, 0, 0, ZoneOffset.UTC)

    private fun hMenos(horas: Long) = agora.minusHours(horas)
        .format(java.time.format.DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSZ"))

    @Test
    fun `o formato do Jira tem fuso SEM dois-pontos, e e este que precisa funcionar`() {
        // "2026-09-09T09:00:00.000+0000" — time.RFC3339 on its own rejects it.
        assertEquals("3 h ago", quandoFoi("2026-09-09T09:00:00.000+0000", agora))
    }

    @Test
    fun `o formato ISO com dois-pontos tambem passa`() {
        assertEquals("3 h ago", quandoFoi("2026-09-09T09:00:00Z", agora))
    }

    @Test
    fun `a escala vai de minutos a anos`() {
        assertEquals("30 min ago", quandoFoi(agora.minusMinutes(30).toString(), agora))
        assertEquals("5 h ago", quandoFoi(hMenos(5), agora))
        assertEquals("3 d ago", quandoFoi(agora.minusDays(3).toString(), agora))
        assertEquals("2 wk ago", quandoFoi(agora.minusDays(15).toString(), agora))
        assertEquals("3 mo ago", quandoFoi(agora.minusDays(100).toString(), agora))
        assertEquals("2 y ago", quandoFoi(agora.minusDays(800).toString(), agora))
    }

    @Test
    fun `data ilegivel vira vazio, nunca erro — o cartao continua util`() {
        assertEquals("", quandoFoi("ontem de tarde", agora))
        assertEquals("", quandoFoi(null, agora))
        assertEquals("", quandoFoi("", agora))
    }

    @Test
    fun `carimbo no futuro nao vira numero negativo`() {
        // The device's clock may be running behind the server's.
        assertEquals("now", quandoFoi(agora.plusHours(2).toString(), agora))
    }
}

class IniciaisTest {

    @Test
    fun `duas palavras dao duas iniciais`() {
        assertEquals("SR", iniciais("Sam Rivera"))
    }

    @Test
    fun `nome do meio nao entra — o ultimo sobrenome identifica melhor`() {
        assertEquals("SR", iniciais("Sam Lee Rivera"))
    }

    @Test
    fun `uma palavra da uma inicial`() {
        assertEquals("S", iniciais("sam"))
    }

    @Test
    fun `sem nome, um ponto de interrogacao — nunca um circulo vazio`() {
        assertEquals("?", iniciais(null))
        assertEquals("?", iniciais("   "))
    }
}

class VencimentoTest {

    private val hoje: LocalDate = LocalDate.of(2026, 9, 9)

    @Test
    fun `vencido e diferente de vencendo, e e essa diferenca que muda o dia`() {
        assertEquals("venceu", vencimento("2026-09-01", hoje))
        assertEquals("due today", vencimento("2026-09-09", hoje))
        assertEquals("due tomorrow", vencimento("2026-09-10", hoje))
        assertEquals("due in 5 d", vencimento("2026-09-14", hoje))
    }

    @Test
    fun `sem data de vencimento, o cartao nao ganha linha`() {
        assertEquals("", vencimento(null, hoje))
        assertEquals("", vencimento("nunca", hoje))
    }
}
