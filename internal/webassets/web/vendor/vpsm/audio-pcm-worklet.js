// PCM16 16kHz mono worklet — captura áudio do mic em browser, faz
// downsampling de 48kHz → 16kHz e quantiza pra Int16, envia ao main
// thread em chunks de ~80ms (1280 samples).
//
// Por que worklet e não ScriptProcessor:
//   - ScriptProcessorNode é deprecated e roda na UI thread (glitches).
//   - AudioWorklet roda em audio thread real-time (sem jitter).
//
// Resampling: low-pass filter implícito por average-then-decimate é OK pra
// fala humana (banda <8kHz no Nyquist 16k). Whisper foi treinado em 16kHz —
// upsampling de fonte 44.1/48kHz que está aqui é estável.
//
// MessagePort protocol:
//   main → worklet:  { type:'flush' } | { type:'mute', on:bool }
//   worklet → main:  ArrayBuffer<Int16> (1280 samples = 80ms a 16kHz)
class PCM16Worklet extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetSr = 16000;
    this.frameSize = 1280;       // 80ms a 16kHz
    this.buffer = new Float32Array(this.frameSize);
    this.bufferIdx = 0;
    this.muted = false;
    // BUG FIX (Jun 2026): acc/accN agora são INSTANCE state. Antes eram locais
    // do process() — perdiam ~1-3 samples a cada call (block=128, ratio=3 →
    // 128/3=42.67 grupos completos + 2 samples órfãos descartados a cada
    // chamada). Resultado: ~1.5% data loss + phase desalinhado + whisper
    // não conseguia transcrever direito em fala suave.
    this.acc = 0;
    this.accN = 0;
    this.port.onmessage = (e) => {
      const msg = e.data;
      if (msg && msg.type === 'mute') this.muted = !!msg.on;
    };
  }

  process(inputs) {
    const input = inputs[0];
    if (!input || !input[0]) return true;
    const ch = input[0]; // 1 canal
    const srcSr = sampleRate; // global AudioWorkletGlobalScope
    if (this.muted) return true;
    // Quando AudioContext já está em 16kHz (caso ideal — browser fez resampling
    // de alta qualidade com polyphase low-pass), passamos direto sem touch.
    // Antes sempre fazia average-of-N que introduz aliasing — degrade pra Whisper.
    if (srcSr === this.targetSr) {
      for (let i = 0; i < ch.length; i++) {
        if (this.bufferIdx >= this.frameSize) this._flush();
        this.buffer[this.bufferIdx++] = ch[i] || 0;
      }
      return true;
    }
    const ratio = srcSr / this.targetSr;
    if (ratio < 1.0) {
      // Source SR < target — improvável, mas fallback simples.
      for (let i = 0; i < ch.length; i++) {
        if (this.bufferIdx >= this.frameSize) this._flush();
        this.buffer[this.bufferIdx++] = ch[i] || 0;
      }
    } else {
      // Fallback: AudioContext não honrou 16kHz e ficou em 48k. Average-of-N
      // com state preservado entre process() calls. Inferior a um real low-pass
      // FIR mas melhor que decimação sem média.
      for (let i = 0; i < ch.length; i++) {
        this.acc += ch[i];
        this.accN++;
        if (this.accN >= ratio) {
          const avg = this.acc / this.accN;
          if (this.bufferIdx >= this.frameSize) this._flush();
          this.buffer[this.bufferIdx++] = avg;
          this.acc = 0;
          this.accN = 0;
        }
      }
    }
    return true;
  }

  _flush() {
    // Converte Float32 ∈ [-1,1] → Int16 ∈ [-32768, 32767].
    const out = new Int16Array(this.frameSize);
    for (let i = 0; i < this.frameSize; i++) {
      const s = Math.max(-1, Math.min(1, this.buffer[i]));
      out[i] = (s < 0 ? s * 0x8000 : s * 0x7FFF) | 0;
    }
    // Transferência zero-copy.
    this.port.postMessage(out.buffer, [out.buffer]);
    this.bufferIdx = 0;
  }
}

registerProcessor('vpsm-pcm16-worklet', PCM16Worklet);
