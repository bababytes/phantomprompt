#!/bin/zsh
# Builds dist/PhantomPrompt.app (universal), a DMG and per-architecture CLI folders.
# Release signing (optional):
#   PHANTOMPROMPT_SIGN_IDENTITY="Developer ID Application: Name (TEAMID)"
#   PHANTOMPROMPT_NOTARY_PROFILE=<profile saved with: xcrun notarytool store-credentials>
# Without them the app is signed ad hoc and works only on this Mac.
set -eu
cd -- "${0:A:h:h}"

version="${PHANTOMPROMPT_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || print 0.2.0-dev)}"
if [[ "${PHANTOMPROMPT_VERSION:-}" == v* && ! "${PHANTOMPROMPT_VERSION}" =~ '^v[0-9]+\.[0-9]+\.[0-9]+$' ]]; then
  print -u2 "PHANTOMPROMPT_VERSION=${PHANTOMPROMPT_VERSION} is not a release tag (vMAJOR.MINOR.PATCH)."
  exit 1
fi
short_version="${version#v}"
short_version="${short_version%%-*}"
[[ "${short_version}" == <->.<->.<-> ]] || short_version="0.2.0"
identity="${PHANTOMPROMPT_SIGN_IDENTITY:--}"
ldflags="-s -w -X main.version=${version}"

toolchain_root="$(go env GOROOT)"
toolchain_license="${toolchain_root}/LICENSE"
if [[ ! -f "${toolchain_license}" ]]; then toolchain_license="${toolchain_root:h}/LICENSE"; fi
license_sources=("${toolchain_license}")
for dependency in github.com/elazarl/goproxy golang.org/x/net golang.org/x/text; do
  dependency_dir="$(go list -m -f '{{.Dir}}' "${dependency}")"
  license_sources+=("${dependency_dir}/LICENSE")
done
if [[ -f node_modules/vue/LICENSE ]]; then license_sources+=(node_modules/vue/LICENSE); fi

sign() {
  if [[ "${identity}" == "-" ]]; then
    codesign --force --options runtime --sign - "$@"
  else
    codesign --force --options runtime --timestamp --sign "${identity}" "$@"
  fi
}

iconset_dir="dist/AppIcon.iconset"
rm -rf "${iconset_dir}"
mkdir -p "${iconset_dir}"
for icon_size in 16 32 128 256 512; do
  sips -z "${icon_size}" "${icon_size}" packaging/macos/AppIcon.png --out "${iconset_dir}/icon_${icon_size}x${icon_size}.png" >/dev/null
  sips -z "$((icon_size * 2))" "$((icon_size * 2))" packaging/macos/AppIcon.png --out "${iconset_dir}/icon_${icon_size}x${icon_size}@2x.png" >/dev/null
done
iconutil -c icns -o dist/AppIcon.icns "${iconset_dir}"
rm -rf "${iconset_dir}"

for target_arch in arm64 amd64; do
  release_dir="dist/PhantomPrompt-macos-${target_arch}"
  rm -rf "${release_dir}"
  mkdir -p "${release_dir}/bin"
  GOOS=darwin GOARCH="${target_arch}" CGO_ENABLED=0 go build -trimpath -ldflags "${ldflags}" -o "${release_dir}/bin/phantomprompt" ./cmd/phantomprompt
  sign "${release_dir}/bin/phantomprompt"
  cp PhantomPrompt.command "${release_dir}/PhantomPrompt.command"
  cp LICENSE "${release_dir}/LICENSE"
  for license_file in "${license_sources[@]}"; do
    print -- "${license_file:h:t}"
    cat -- "${license_file}"
    print
  done > "${release_dir}/LICENSES.txt"
  chmod +x "${release_dir}/PhantomPrompt.command" "${release_dir}/bin/phantomprompt"
  (cd dist && rm -f "PhantomPrompt-macos-${target_arch}.zip" && zip -q -r "PhantomPrompt-macos-${target_arch}.zip" "PhantomPrompt-macos-${target_arch}")
done

app_dir="dist/PhantomPrompt.app"
rm -rf "${app_dir}"
mkdir -p "${app_dir}/Contents/MacOS" "${app_dir}/Contents/Resources"
cp packaging/macos/Info.plist "${app_dir}/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "${short_version}" "${app_dir}/Contents/Info.plist"
plutil -replace CFBundleVersion -string "${short_version}" "${app_dir}/Contents/Info.plist"
plutil -lint -s "${app_dir}/Contents/Info.plist"
lipo -create -output "${app_dir}/Contents/MacOS/phantomprompt" dist/PhantomPrompt-macos-arm64/bin/phantomprompt dist/PhantomPrompt-macos-amd64/bin/phantomprompt
cp dist/AppIcon.icns "${app_dir}/Contents/Resources/AppIcon.icns"
cp LICENSE "${app_dir}/Contents/Resources/LICENSE"
cp dist/PhantomPrompt-macos-arm64/LICENSES.txt "${app_dir}/Contents/Resources/LICENSES.txt"
sign "${app_dir}/Contents/MacOS/phantomprompt"
sign "${app_dir}"
codesign --verify --strict --verbose=1 "${app_dir}"
rm -f dist/AppIcon.icns

dmg="dist/PhantomPrompt-${version}.dmg"
staging="$(mktemp -d)"
ditto "${app_dir}" "${staging}/PhantomPrompt.app"
ln -s /Applications "${staging}/Applications"
rm -f "${dmg}"
hdiutil create -quiet -volname PhantomPrompt -srcfolder "${staging}" -format UDZO "${dmg}"
rm -rf "${staging}"
if [[ "${identity}" != "-" ]]; then
  codesign --force --timestamp --sign "${identity}" "${dmg}"
  if [[ -n "${PHANTOMPROMPT_NOTARY_PROFILE:-}" ]]; then
    xcrun notarytool submit "${dmg}" --keychain-profile "${PHANTOMPROMPT_NOTARY_PROFILE}" --wait
    xcrun stapler staple "${dmg}"
    spctl --assess --type open --context context:primary-signature --verbose "${dmg}"
  fi
fi
(cd dist && shasum -a 256 "${dmg:t}" PhantomPrompt-macos-*.zip > SHA256SUMS)
print "Built ${dmg} (version ${version}, signed with: ${identity})."
if [[ "${identity}" == "-" ]]; then
  print "Ad hoc signature: fine on this Mac; other Macs need a Developer ID signature and notarization."
fi
