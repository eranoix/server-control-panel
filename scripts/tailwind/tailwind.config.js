module.exports = {
  content: [
    './internal/webassets/web/*.html',
    './internal/webassets/web/vendor/panel/app/[0-9][0-9]-*.js',
  ],
  theme: { extend: {} },
  corePlugins: { preflight: true },
}
