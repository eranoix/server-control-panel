// Override of uv.config.js — paths carry the /browser/ prefix because the panel
// (Go) serves this app under /browser/ on the same origin.
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
