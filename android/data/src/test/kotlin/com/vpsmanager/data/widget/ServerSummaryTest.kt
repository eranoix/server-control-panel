package com.vpsmanager.data.widget

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What these tests protect: the home screen widget shows a number that can be
 * half an hour old, because Android does not accept updating it any faster
 * than that. The age sentence is what separates "honest summary" from "a lie
 * on the home screen" — and the home screen is seen far more times a day than
 * the app.
 */
class ServerSummaryTest {

    private val now = 1_700_000_000_000L

    /**
     * BEFORE THE FIRST READING the sentence cannot be "now".
     *
     * `measuredAt = 0` is the state of a freshly installed widget, before the app
     * has ever opened. If it said "now" next to three dashes, it would be
     * claiming to have measured what it never measured.
     */
    @Test
    fun `sem leitura nenhuma a frase diz isso, e nao agora`() {
        assertEquals("no reading yet", ageInWords(0L, now))
    }

    @Test
    fun `abaixo de um minuto e agora`() {
        assertEquals("just now", ageInWords(now - 30_000L, now))
    }

    @Test
    fun `minutos ate uma hora`() {
        assertEquals("1 min ago", ageInWords(now - 60_000L, now))
        assertEquals("45 min ago", ageInWords(now - 45 * 60_000L, now))
    }

    /**
     * Half an hour is the widget's REAL refresh interval, so this is the
     * sentence it shows most of the time. It has to be right.
     */
    @Test
    fun `meia hora — o intervalo real do widget — aparece em minutos`() {
        assertEquals("30 min ago", ageInWords(now - 30 * 60_000L, now))
    }

    @Test
    fun `horas depois de uma hora`() {
        assertEquals("1 h ago", ageInWords(now - 60 * 60_000L, now))
        assertEquals("5 h ago", ageInWords(now - 5 * 60 * 60_000L, now))
    }

    /**
     * Past a day the sentence stops counting. The exact number of days changes
     * no decision — what matters is that the data is no longer good.
     */
    @Test
    fun `acima de um dia para de contar`() {
        assertEquals("over a day ago", ageInWords(now - 25L * 60 * 60_000L, now))
        assertEquals("over a day ago", ageInWords(now - 40L * 24 * 60 * 60_000L, now))
    }

    /**
     * A device clock can walk backwards (a time zone adjustment, NTP). A
     * negative difference must not turn into "-3 min ago", which is the most
     * disconcerting sentence possible on an operations dashboard.
     */
    @Test
    fun `relogio para tras nao produz idade negativa`() {
        assertEquals("just now", ageInWords(now + 60_000L, now))
    }

    @Test
    fun `o resumo vazio mostra traco, nunca zero`() {
        val empty = ServerSummary.EMPTY
        assertEquals("—", empty.cpu)
        assertEquals("—", empty.memory)
        assertEquals("—", empty.disk)
        assertEquals(null, empty.alert)
    }
}
