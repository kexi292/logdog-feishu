#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
if [ "$#" -eq 0 ]; then architectures=(amd64 arm64); else architectures=("$@"); fi

for arch in "${architectures[@]}"; do
  case "$arch" in amd64|arm64) ;; *) echo 'Usage: bash script/build-linux.sh [amd64|arm64]' >&2; exit 1 ;; esac
  package_name="logdog-feishu-linux-$arch"
  package_dir="dist/$package_name"
  mkdir -p "$package_dir/licenses"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$package_dir/logdog-feishu" .
  cp deploy/logdog-feishu.service deploy/DEPLOY.md README.md "$package_dir/"
  go_root="$(go env GOROOT)"
  go_license="$go_root/LICENSE"
  if [ ! -f "$go_license" ]; then go_license="$go_root/../LICENSE"; fi
  cp "$go_license" "$package_dir/licenses/Go-LICENSE"
  git describe --always --dirty > "$package_dir/SOURCE.txt"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' . | sort -u |
    while read -r module_name module_dir; do
      [ -n "$module_name" ] || continue
      license_found=false
      for license_file in "$module_dir"/LICENSE* "$module_dir"/COPYING* "$module_dir"/NOTICE*; do
        [ -f "$license_file" ] || continue
        mkdir -p "$package_dir/licenses/$module_name"
        install -m 0644 "$license_file" "$package_dir/licenses/$module_name/$(basename "$license_file")"
        license_found=true
      done
      if [ "$license_found" = false ] && [ -f "$module_dir/README.md" ]; then
        mkdir -p "$package_dir/licenses/$module_name"
        install -m 0644 "$module_dir/README.md" "$package_dir/licenses/$module_name/README.md"
        echo "Preserved README license declaration: $module_name"
      elif [ "$license_found" = false ]; then
        echo "Missing license information: $module_name" >&2
        exit 1
      fi
    done
  COPYFILE_DISABLE=1 tar -C dist -czf "dist/$package_name.tar.gz" "$package_name"
  (
    cd dist
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$package_name.tar.gz" > "$package_name.tar.gz.sha256"
    else
      shasum -a 256 "$package_name.tar.gz" > "$package_name.tar.gz.sha256"
    fi
  )
  echo "Built dist/$package_name.tar.gz"
done
