#!/usr/bin/env bash
# Render report.html -> report.pdf (Chrome headless, A4, margins from report.css @page).
#
#   ./build.sh              render PDF, print page count + size + TBD count
#   ./build.sh --preview    additionally rasterise every page to preview/page-N.png (72 dpi)
#   ./build.sh --charts     also run charts.py build (helper charts from data/*.json; the report
#                           itself references the stress worker's figures in charts/stress/)
#
# Idempotent: outputs are removed and rebuilt on every run. Never touches anything
# outside this directory.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HTML="$ROOT/report.html"
PDF="$ROOT/report.pdf"
LOG="$ROOT/build.log"

CHARTS=0
PREVIEW=0
for arg in "$@"; do
  case "$arg" in
    --charts)    CHARTS=1 ;;
    --no-charts) CHARTS=0 ;;
    --preview)   PREVIEW=1 ;;
    -h|--help)   sed -n '2,9p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

[[ -f "$HTML" ]] || { echo "missing $HTML" >&2; exit 1; }

# 1. Optional helper charts (charts.py; not referenced by report.html since the stress figures landed).
if [[ $CHARTS -eq 1 ]]; then
  if python3 -c "import matplotlib" >/dev/null 2>&1; then
    python3 "$ROOT/charts.py" build
  else
    echo "matplotlib not importable; keeping existing charts/*.svg" >&2
  fi
fi

# 2. Locate Chrome.
CHROME=""
for candidate in \
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  "/Applications/Chromium.app/Contents/MacOS/Chromium" \
  google-chrome chromium chromium-browser; do
  if [[ -x "$candidate" ]] || command -v "$candidate" >/dev/null 2>&1; then
    CHROME="$candidate"; break
  fi
done
[[ -n "$CHROME" ]] || { echo "no Chrome/Chromium found; open report.html and Print -> Save as PDF" >&2; exit 1; }

# 3. Render. Page size and margins come from @page in report.css; --no-pdf-header-footer
#    removes Chrome's own date/URL header so the CSS margin boxes are the only footer.
rm -f "$PDF"
"$CHROME" \
  --headless=new \
  --disable-gpu \
  --no-pdf-header-footer \
  --allow-file-access-from-files \
  --run-all-compositor-stages-before-draw \
  --virtual-time-budget=10000 \
  --print-to-pdf="$PDF" \
  "file://$HTML" >"$LOG" 2>&1 || true
[[ -s "$PDF" ]] || { echo "PDF generation failed; see $LOG" >&2; exit 1; }

# 4. Report.
PAGES="?"
if command -v pdfinfo >/dev/null 2>&1; then
  PAGES="$(pdfinfo "$PDF" | awk '/^Pages:/{print $2}')"
elif command -v mdls >/dev/null 2>&1; then
  PAGES="$(mdls -name kMDItemNumberOfPages -raw "$PDF" 2>/dev/null || echo '?')"
fi
SIZE="$(du -h "$PDF" | cut -f1 | tr -d ' ')"
BYTES="$(stat -f %z "$PDF" 2>/dev/null || stat -c %s "$PDF")"
TBD="$( { grep -o 'class="tbd"' "$HTML" || true; } | wc -l | tr -d ' ')"   # grep exits 1 at zero markers
echo "wrote  $PDF"
echo "pages  $PAGES"
echo "size   $SIZE ($BYTES bytes)"
echo "tbd    $TBD markers in report.html"
if command -v pdftotext >/dev/null 2>&1; then
  if ! pdftotext -f 3 -l 3 "$PDF" - 2>/dev/null | head -3 | grep -q "SECTION 1"; then
    echo "WARN   page 3 does not open with section 1: the business page has overflowed" >&2
  fi
fi

# 5. Optional page previews (for eyeballing the layout without opening the PDF).
if [[ $PREVIEW -eq 1 ]]; then
  mkdir -p "$ROOT/preview"
  rm -f "$ROOT/preview"/page-*.png
  if command -v pdftoppm >/dev/null 2>&1; then
    pdftoppm -png -r 72 "$PDF" "$ROOT/preview/page"
  else
    sips -s format png "$PDF" --out "$ROOT/preview/page-1.png" >/dev/null   # first page only
  fi
  echo "previews $(ls "$ROOT/preview" | wc -l | tr -d ' ') png in $ROOT/preview/"
fi
