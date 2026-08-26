#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mode="${1:-}"
shift || true
source_dir=""
destination=""
open_prs=""
compact=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) source_dir="$2"; shift 2 ;;
    --destination) destination="$2"; shift 2 ;;
    --open-prs) open_prs="$2"; shift 2 ;;
    --compact) compact=1; shift ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ "$mode" != "publish" && "$mode" != "remove" && "$mode" != "prune" ]]; then
  printf 'Usage: %s publish --source DIR --destination NAME | remove --destination NAME | prune --open-prs CSV [--compact]\n' "$0" >&2
  exit 2
fi
if [[ "$mode" == "publish" && (! -d "$source_dir" || -z "$destination") ]]; then
  printf 'publish requires --source DIR and --destination NAME\n' >&2
  exit 2
fi
if [[ "$mode" == "remove" && -z "$destination" ]]; then
  printf 'remove requires --destination NAME\n' >&2
  exit 2
fi

worktree="$(mktemp -d)"
cleanup() {
  git worktree remove --force "$worktree" >/dev/null 2>&1 || true
  rm -rf "$worktree"
}
trap cleanup EXIT

git fetch origin gh-pages >/dev/null 2>&1 || true
if git show-ref --verify --quiet refs/remotes/origin/gh-pages; then
  git worktree add --detach "$worktree" origin/gh-pages >/dev/null
else
  git worktree add --detach "$worktree" HEAD >/dev/null
  (
    cd "$worktree"
    git checkout --orphan swapbook-pages-init >/dev/null
    git rm -rf . >/dev/null 2>&1 || true
  )
fi

case "$mode" in
  publish)
    rm -rf "$worktree/swapbook/$destination"
    mkdir -p "$worktree/swapbook/$destination"
    cp -R "$source_dir"/. "$worktree/swapbook/$destination/"
    rm -f "$worktree/swapbook/$destination/server.log"
    ;;
  remove)
    rm -rf "$worktree/swapbook/$destination"
    ;;
  prune)
    declare -A keep=()
    IFS=',' read -ra ids <<< "$open_prs"
    for id in "${ids[@]}"; do
      [[ -n "$id" ]] && keep["pr-$id"]=1
    done
    if [[ -d "$worktree/swapbook" ]]; then
      for path in "$worktree"/swapbook/pr-*; do
        [[ -d "$path" ]] || continue
        name="$(basename "$path")"
        [[ -n "${keep[$name]:-}" ]] || rm -rf "$path"
      done
    fi
    ;;
esac

touch "$worktree/.nojekyll"
python3 - "$worktree" <<'PY'
import html
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
links = []
base = root / "swapbook"
if base.exists():
    for report in sorted(base.glob("*/report.html")):
        rel = report.relative_to(root).as_posix()
        links.append(f'<li><a href="{html.escape(rel)}">{html.escape(report.parent.name)}</a></li>')
(root / "index.html").write_text(
    '<!doctype html><html lang="en"><head><meta charset="utf-8">'
    '<meta name="viewport" content="width=device-width,initial-scale=1">'
    '<link rel="icon" href="data:,"><title>OpenSandbox UI inspections</title>'
    '<style>body{max-width:60rem;margin:3rem auto;padding:0 1rem;font:16px/1.5 system-ui}'
    'li{margin:.5rem 0}</style></head><body><h1>OpenSandbox UI inspections</h1><ul>'
    + ''.join(links) + '</ul></body></html>'
)
PY

(
  cd "$worktree"
  git add -A
  changed=1
  if git diff --cached --quiet; then
    changed=0
  else
    git -c user.name=github-actions[bot] \
        -c user.email=41898282+github-actions[bot]@users.noreply.github.com \
        commit -m "Publish Swapbook inspection: ${destination:-cleanup}" >/dev/null
  fi

  if [[ "$compact" -eq 1 ]]; then
    tree="$(git write-tree)"
    commit="$(printf 'Compact Swapbook inspection pages\n' | git -c user.name=github-actions[bot] -c user.email=41898282+github-actions[bot]@users.noreply.github.com commit-tree "$tree")"
    git push --force origin "$commit:refs/heads/gh-pages"
  elif [[ "$changed" -eq 0 ]]; then
    printf 'No Pages changes to publish.\n'
  else
    for attempt in 1 2 3 4 5; do
      if git push origin HEAD:gh-pages; then
        break
      fi
      if [[ "$attempt" -eq 5 ]]; then
        printf 'Unable to publish gh-pages after retries.\n' >&2
        exit 1
      fi
      git fetch origin gh-pages
      git rebase origin/gh-pages
      sleep "$attempt"
    done
  fi
)

if [[ -n "${GITHUB_OUTPUT:-}" && "$mode" == "publish" ]]; then
  owner="${GITHUB_REPOSITORY%%/*}"
  repository="${GITHUB_REPOSITORY##*/}"
  owner_lower="${owner,,}"
  pages_base="https://${owner_lower}.github.io/${repository}"
  raw_base="https://raw.githubusercontent.com/${GITHUB_REPOSITORY}/gh-pages"
  {
    echo "pages-url=${pages_base}/swapbook/${destination}/report.html"
    echo "screenshot-url=${raw_base}/swapbook/${destination}/pool-detail-capacity-dashboard/visual.png?run=${GITHUB_RUN_ID:-local}"
    echo "destination=${destination}"
  } >> "$GITHUB_OUTPUT"
fi
