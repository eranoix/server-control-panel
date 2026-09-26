// PCM16 16kHz mono worklet: resamples mic audio to 16kHz, quantizes to Int16
// and posts ~80ms chunks (1280 samples) to the main thread. A worklet, not
// ScriptProcessorNode, because the latter is deprecated and runs on the UI
// thread (glitches).
//
// MessagePort protocol:
//   main → worklet:  { type:'flush' } | { type:'mute', on:bool }
//   worklet → main:  ArrayBuffer<Int16> (1280 samples = 80ms a 16kHz)
class PCM16Worklet extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetSr = 16000;
    this.frameSize = 1280;       // 80ms at 16kHz
    this.buffer = new Float32Array(this.frameSize);
    this.bufferIdx = 0;
    this.muted = false;
    // acc/accN must be instance state: a 128-sample block does not divide by
    // the ratio, so per-call locals would drop the leftover samples every call.
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
    const ch = input[0];
    const srcSr = sampleRate; // global AudioWorkletGlobalScope
    if (this.muted) return true;
    // Context already at 16kHz (the browser resampled with a proper low-pass):
    // pass through untouched, since averaging would add aliasing.
    if (srcSr === this.targetSr) {
      for (let i = 0; i < ch.length; i++) {
        if (this.bufferIdx >= this.frameSize) this._flush();
        this.buffer[this.bufferIdx++] = ch[i] || 0;
      }
      return true;
    }
    const ratio = srcSr / this.targetSr;
    if (ratio < 1.0) {
      for (let i = 0; i < ch.length; i++) {
        if (this.bufferIdx >= this.frameSize) this._flush();
        this.buffer[this.bufferIdx++] = ch[i] || 0;
      }
    } else {
      // The context ignored 16kHz: average-of-N with state kept across
      // process() calls (worse than a real FIR low-pass, better than plain decimation).
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
    const out = new Int16Array(this.frameSize);
    for (let i = 0; i < this.frameSize; i++) {
      const s = Math.max(-1, Math.min(1, this.buffer[i]));
      out[i] = (s < 0 ? s * 0x8000 : s * 0x7FFF) | 0;
    }
    this.port.postMessage(out.buffer, [out.buffer]);
    this.bufferIdx = 0;
  }
}

registerProcessor('panel-pcm16-worklet', PCM16Worklet);
