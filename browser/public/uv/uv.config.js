// Override do uv.config.js — caminhos com prefixo /browser/ porque o painel
// (Go) serve este app sob /browser/ na mesma origem.
self.__uv$config = {
  prefix: "/browser/uv/service/",
  bare: "/browser/bare/",
  encodeUrl: Ultraviolet.codec.xor.encode,
  decodeUrl: Ultraviolet.codec.xor.decode,
  handler: "/browser/uv/uv.handler.js",
  client: "/browser/uv/uv.client.js",
  bundle: "/browser/uv/uv.bundle.js",
  config: "/browser/uv/uv.config.js",
  sw: "/browser/uv/uv.sw.js",
};
