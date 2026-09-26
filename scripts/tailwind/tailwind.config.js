// Paths are relative to the repository root, where scripts/build-tailwind.sh
// runs the compiler. Every app module is scanned, not only index.html: some
// build class names as strings for innerHTML, and a class that appears only
// there would be pruned from the CSS with no build error.
module.exports = {
  content: [
    './internal/webassets/web/*.html',
    './internal/webassets/web/vendor/panel/app/[0-9][0-9]-*.js',
  ],
  theme: { extend: {} },
  corePlugins: { preflight: true },
}
