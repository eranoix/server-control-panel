class PCM16Worklet extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetSr = 16000;
    this.frameSize = 1280;
    this.buffer = new Float32Array(this.frameSize);
    this.bufferIdx = 0;
    this.muted = false;
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
    const srcSr = sampleRate;
    if (this.muted) return true;
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
