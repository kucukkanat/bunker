#!/bin/sh
# Refresh the vendored front-end libraries in internal/vendorlib/files.
# Versions here must match the Libs table in internal/vendorlib/vendorlib.go.
set -eu
OUT="$(cd "$(dirname "$0")/.." && pwd)/internal/vendorlib/files"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
get() { # pkg@ver src-path dest-dir dest-file
	npm pack -q "$1" >/dev/null
	tgz=$(ls -t ./*.tgz | head -1)
	mkdir -p x && tar xzf "$tgz" -C x
	mkdir -p "$OUT/$3"
	cp "x/package/$2" "$OUT/$3/$4"
	rm -rf x "$tgz"
	echo "  $3/$4"
}
get chart.js@4.6.1 dist/chart.umd.min.js chart.js chart.umd.min.js
get chartjs-adapter-date-fns@3.0.0 dist/chartjs-adapter-date-fns.bundle.min.js chartjs-adapter-date-fns chartjs-adapter-date-fns.bundle.min.js
get alpinejs@3.17.5 dist/cdn.min.js alpinejs cdn.min.js
get react@18.3.1 umd/react.production.min.js react react.production.min.js
get react-dom@18.3.1 umd/react-dom.production.min.js react-dom react-dom.production.min.js
get vue@3.5.44 dist/vue.global.prod.js vue vue.global.prod.js
get d3@7.9.2 dist/d3.min.js d3 d3.min.js
get dayjs@1.11.23 dayjs.min.js dayjs dayjs.min.js
get marked@14.1.4 marked.min.js marked marked.min.js
get @tailwindcss/browser@4.3.4 dist/index.global.js tailwindcss browser.js
get bootstrap@5.3.8 dist/css/bootstrap.min.css bootstrap bootstrap.min.css
get bootstrap@5.3.8 dist/js/bootstrap.bundle.min.js bootstrap bootstrap.bundle.min.js
get htmx.org@2.0.11 dist/htmx.min.js htmx.org htmx.min.js
get lodash@4.18.1 lodash.min.js lodash lodash.min.js
get papaparse@5.7.0 papaparse.min.js papaparse papaparse.min.js
get sortablejs@1.15.7 Sortable.min.js sortablejs Sortable.min.js
get canvas-confetti@1.9.4 dist/confetti.browser.js canvas-confetti confetti.browser.js
get jquery@3.7.1 dist/jquery.min.js jquery jquery.min.js
