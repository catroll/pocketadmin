#!/usr/bin/env bash
#
# Vendors the AdminLTE 3.2.0 front-end bundle and the minimal plugin set the
# admin panel actually uses into web/static/vendor/.
#
# Everything is fetched from the public npm registry (no npm/node required) and
# cached under .cache/thirdparty so the script can be re-run offline.
#
# Usage: bash scripts/vendor-assets.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE="$ROOT/.cache/thirdparty"
OUT="$ROOT/web/static/vendor"

# name|version|tarball-url
PKGS=(
  "admin-lte|3.2.0|https://registry.npmjs.org/admin-lte/-/admin-lte-3.2.0.tgz"
  "jquery|3.7.1|https://registry.npmjs.org/jquery/-/jquery-3.7.1.tgz"
  "bootstrap|4.6.2|https://registry.npmjs.org/bootstrap/-/bootstrap-4.6.2.tgz"
  "fontawesome-free|5.15.4|https://registry.npmjs.org/@fortawesome/fontawesome-free/-/fontawesome-free-5.15.4.tgz"
  "overlayscrollbars|1.13.3|https://registry.npmjs.org/overlayscrollbars/-/overlayscrollbars-1.13.3.tgz"
  "chart.js|2.9.4|https://registry.npmjs.org/chart.js/-/chart.js-2.9.4.tgz"
  "toastr|2.1.4|https://registry.npmjs.org/toastr/-/toastr-2.1.4.tgz"
)

mkdir -p "$CACHE" "$OUT"

for entry in "${PKGS[@]}"; do
  IFS='|' read -r name version url <<<"$entry"
  tgz="$CACHE/$name-$version.tgz"
  if [[ ! -f "$tgz" ]]; then
    echo "downloading $name@$version"
    curl -sSL -o "$tgz" "$url"
  fi
done

# --- AdminLTE core -----------------------------------------------------------
mkdir -p "$OUT/adminlte/css" "$OUT/adminlte/js" "$OUT/adminlte/img"
tar xzf "$CACHE/admin-lte-3.2.0.tgz" -C "$OUT/adminlte" --strip-components=2 \
  package/dist/css/adminlte.min.css \
  package/dist/js/adminlte.min.js \
  package/dist/img/AdminLTELogo.png \
  package/dist/img/user2-160x160.jpg

# --- jQuery ------------------------------------------------------------------
mkdir -p "$OUT/jquery"
tar xzf "$CACHE/jquery-3.7.1.tgz" -C "$OUT/jquery" --strip-components=2 package/dist/jquery.min.js

# --- Bootstrap 4 (bundle already contains Popper) ----------------------------
mkdir -p "$OUT/bootstrap/css" "$OUT/bootstrap/js"
tar xzf "$CACHE/bootstrap-4.6.2.tgz" -C "$OUT/bootstrap" --strip-components=2 \
  package/dist/css/bootstrap.min.css \
  package/dist/js/bootstrap.bundle.min.js

# --- Font Awesome 5 (css + webfonts) -----------------------------------------
mkdir -p "$OUT/fontawesome/css" "$OUT/fontawesome/webfonts"
tar xzf "$CACHE/fontawesome-free-5.15.4.tgz" -C "$OUT/fontawesome" --strip-components=1 \
  package/css/all.min.css
tar xzf "$CACHE/fontawesome-free-5.15.4.tgz" -C "$OUT/fontawesome" --strip-components=1 \
  --wildcards 'package/webfonts/*'

# --- OverlayScrollbars (AdminLTE sidebar scroller) ---------------------------
mkdir -p "$OUT/overlayscrollbars/css" "$OUT/overlayscrollbars/js"
tar xzf "$CACHE/overlayscrollbars-1.13.3.tgz" -C "$OUT/overlayscrollbars" --strip-components=2 \
  package/css/OverlayScrollbars.min.css \
  package/js/jquery.overlayScrollbars.min.js

# --- Chart.js ----------------------------------------------------------------
mkdir -p "$OUT/chartjs"
tar xzf "$CACHE/chart.js-2.9.4.tgz" -C "$OUT/chartjs" --strip-components=2 package/dist/Chart.min.js

# --- Toastr ------------------------------------------------------------------
mkdir -p "$OUT/toastr"
tar xzf "$CACHE/toastr-2.1.4.tgz" -C "$OUT/toastr" --strip-components=2 \
  package/build/toastr.min.css \
  package/build/toastr.min.js

echo
echo "vendored into $OUT:"
find "$OUT" -type f | sed "s|$OUT/||" | sort
