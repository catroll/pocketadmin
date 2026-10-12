#!/usr/bin/env bash
#
# Vendors the AdminLTE 4 front-end bundle and the minimal plugin set the admin
# panel actually uses into web/static/vendor/.
#
# Versions are pinned to the newest stable release at the time of writing. Check
# them with:
#
#   for p in admin-lte bootstrap @fortawesome/fontawesome-free chart.js; do
#     npm view "$p" dist-tags.latest
#   done
#
# Everything is fetched from the public npm registry (no npm/node required) and
# cached under .cache/thirdparty so the script can be re-run offline.
#
# Each package's own LICENSE file is vendored alongside it. Do not remove that
# step: MIT and CC BY 4.0 both require the notice to travel with the copies.
# When a package ships no license file, a provenance stub is written instead and
# the attribution lives in the repository's NOTICE file.
#
# Usage: bash scripts/vendor-assets.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE="$ROOT/.cache/thirdparty"
OUT="$ROOT/web/static/vendor"

# name | version | tarball url | license member | declared license | author
PKGS=(
  "admin-lte|4.10.0|https://registry.npmjs.org/admin-lte/-/admin-lte-4.10.0.tgz|package/LICENSE|MIT|ColorlibHQ"
  "bootstrap|5.3.8|https://registry.npmjs.org/bootstrap/-/bootstrap-5.3.8.tgz|package/LICENSE|MIT|The Bootstrap Authors"
  "fontawesome-free|7.3.1|https://registry.npmjs.org/@fortawesome/fontawesome-free/-/fontawesome-free-7.3.1.tgz|package/LICENSE.txt|CC BY 4.0 (icons), SIL OFL 1.1 (fonts), MIT (code)|Fonticons, Inc."
  "chart.js|4.5.1|https://registry.npmjs.org/chart.js/-/chart.js-4.5.1.tgz|package/LICENSE.md|MIT|Chart.js Contributors"
)

mkdir -p "$CACHE" "$OUT"

for entry in "${PKGS[@]}"; do
  IFS='|' read -r name version url _ _ _ <<<"$entry"
  tgz="$CACHE/$name-$version.tgz"
  if [[ ! -f "$tgz" ]]; then
    echo "downloading $name@$version"
    curl -fsSL -o "$tgz" "$url"
  fi
done

# vendor_license <tarball> <member> <dest-dir> <declared-license> <author>
#
# Copies the upstream license verbatim when present. When a package ships none
# (OverlayScrollbars did this, for example), writes a provenance stub so the
# absence is recorded rather than silently swallowed.
vendor_license() {
  local tgz="$1" member="$2" dest="$3" license="$4" author="$5"
  if tar tzf "$tgz" "$member" >/dev/null 2>&1; then
    tar xzf "$tgz" -C "$dest" --strip-components=1 "$member"
  else
    echo "warning: $(basename "$tgz") ships no license file; writing provenance stub" >&2
    cat >"$dest/LICENSE-MISSING.txt" <<EOF
$(basename "$tgz") does not include a license file in its npm tarball.

  declared license : $license
  author           : $author
  source           : https://registry.npmjs.org/

See the repository's NOTICE file for the attribution this project relies on.
EOF
  fi
}

# --- AdminLTE 4 core ---------------------------------------------------------
# Bootstrap 5 based, no jQuery. It ships no dist/img/, so the templates use an
# inline icon for the brand mark rather than a bundled logo image.
mkdir -p "$OUT/adminlte/css" "$OUT/adminlte/js"
tar xzf "$CACHE/admin-lte-4.10.0.tgz" -C "$OUT/adminlte" --strip-components=2 \
  package/dist/css/adminlte.min.css \
  package/dist/js/adminlte.min.js
vendor_license "$CACHE/admin-lte-4.10.0.tgz" "package/LICENSE" "$OUT/adminlte" \
  "MIT" "ColorlibHQ"

# --- Bootstrap 5 (the bundled build already contains Popper) -----------------
mkdir -p "$OUT/bootstrap/css" "$OUT/bootstrap/js"
tar xzf "$CACHE/bootstrap-5.3.8.tgz" -C "$OUT/bootstrap" --strip-components=2 \
  package/dist/css/bootstrap.min.css \
  package/dist/js/bootstrap.bundle.min.js
vendor_license "$CACHE/bootstrap-5.3.8.tgz" "package/LICENSE" "$OUT/bootstrap" \
  "MIT" "The Bootstrap Authors"

# --- Font Awesome 7 (css + webfonts) -----------------------------------------
# FA 7 ships woff2 only; sufficient for every browser the panel targets.
mkdir -p "$OUT/fontawesome/css" "$OUT/fontawesome/webfonts"
tar xzf "$CACHE/fontawesome-free-7.3.1.tgz" -C "$OUT/fontawesome" --strip-components=1 \
  package/css/all.min.css
tar xzf "$CACHE/fontawesome-free-7.3.1.tgz" -C "$OUT/fontawesome" --strip-components=1 \
  --wildcards 'package/webfonts/*'
vendor_license "$CACHE/fontawesome-free-7.3.1.tgz" "package/LICENSE.txt" "$OUT/fontawesome" \
  "CC BY 4.0 (icons), SIL OFL 1.1 (fonts), MIT (code)" "Fonticons, Inc."

# --- Chart.js 4 (UMD build; v4 renamed Chart.min.js -> chart.umd.min.js) ------
mkdir -p "$OUT/chartjs"
tar xzf "$CACHE/chart.js-4.5.1.tgz" -C "$OUT/chartjs" --strip-components=2 \
  package/dist/chart.umd.min.js
vendor_license "$CACHE/chart.js-4.5.1.tgz" "package/LICENSE.md" "$OUT/chartjs" \
  "MIT" "Chart.js Contributors"

# --- Deliberately not vendored -----------------------------------------------
# jQuery       AdminLTE 4 dropped it; nothing in the panel needs it.
# Toastr       Last release 2017, jQuery-dependent. Bootstrap 5 toasts instead.
# OverlayScrollbars  AdminLTE 4 scrolls its sidebar natively via .sidebar-wrapper.

echo
echo "vendored into $OUT:"
find "$OUT" -type f | sed "s|$OUT/||" | sort
