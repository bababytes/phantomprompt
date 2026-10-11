#!/bin/zsh
# Builds the Mac App Store edition: dist/mas/PhantomPrompt Lite.app and, when signing identities
# are given, an installer package for App Store Connect.
#   PHANTOMPROMPT_MAS_APP_IDENTITY="Apple Distribution: Name (TEAMID)"
#   PHANTOMPROMPT_MAS_INSTALLER_IDENTITY="3rd Party Mac Developer Installer: Name (TEAMID)"
#   PHANTOMPROMPT_MAS_PROFILE=/path/to/PhantomPrompt_Lite.provisionprofile
#   PHANTOMPROMPT_BUILD_NUMBER=<n>   (CFBundleVersion; must grow with every upload, default: commit count)
# Without them the app is signed ad hoc with the same sandbox entitlements, for local testing.
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
identity="${PHANTOMPROMPT_MAS_APP_IDENTITY:--}"
out="dist/mas"
app="${out}/PhantomPrompt Lite.app"

rm -rf "${out}"
mkdir -p "${app}/Contents/MacOS" "${app}/Contents/Resources"
for arch in arm64 x86_64; do
  swiftc -O -parse-as-library -target "${arch}-apple-macos14.0" -o "${out}/wrapper-${arch}" packaging/mas/PhantomPromptLite.swift
done
lipo -create -output "${app}/Contents/MacOS/PhantomPromptLite" "${out}"/wrapper-*
for arch in arm64 amd64; do
  GOOS=darwin GOARCH="${arch}" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "${out}/engine-${arch}" ./cmd/phantomprompt
done
lipo -create -output "${app}/Contents/MacOS/phantomprompt-engine" "${out}"/engine-*
rm -f "${out}"/wrapper-* "${out}"/engine-*

cp packaging/mas/Info.plist "${app}/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "${short_version}" "${app}/Contents/Info.plist"
plutil -replace CFBundleVersion -string "${PHANTOMPROMPT_BUILD_NUMBER:-$(git rev-list --count HEAD)}" "${app}/Contents/Info.plist"
plutil -lint -s "${app}/Contents/Info.plist"
iconset="${out}/AppIcon.iconset"
mkdir -p "${iconset}"
for size in 16 32 128 256 512; do
  sips -z "${size}" "${size}" packaging/macos/AppIcon.png --out "${iconset}/icon_${size}x${size}.png" >/dev/null
  sips -z "$((size * 2))" "$((size * 2))" packaging/macos/AppIcon.png --out "${iconset}/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns -o "${app}/Contents/Resources/AppIcon.icns" "${iconset}"
rm -rf "${iconset}"
cp packaging/mas/PrivacyInfo.xcprivacy "${app}/Contents/Resources/PrivacyInfo.xcprivacy"
plutil -lint -s "${app}/Contents/Resources/PrivacyInfo.xcprivacy"
if [[ -n "${PHANTOMPROMPT_MAS_PROFILE:-}" ]]; then
  cp "${PHANTOMPROMPT_MAS_PROFILE}" "${app}/Contents/embedded.provisionprofile"
fi

timestamp=()
[[ "${identity}" == "-" ]] || timestamp=(--timestamp)
codesign --force "${timestamp[@]}" --entitlements packaging/mas/Engine.entitlements --sign "${identity}" "${app}/Contents/MacOS/phantomprompt-engine"
codesign --force "${timestamp[@]}" --entitlements packaging/mas/PhantomPromptLite.entitlements --sign "${identity}" "${app}"
codesign --verify --strict --verbose=1 "${app}"

if [[ -n "${PHANTOMPROMPT_MAS_INSTALLER_IDENTITY:-}" ]]; then
  productbuild --component "${app}" /Applications --sign "${PHANTOMPROMPT_MAS_INSTALLER_IDENTITY}" "${out}/PhantomPrompt-Lite-${short_version}.pkg"
  print "Upload ${out}/PhantomPrompt-Lite-${short_version}.pkg with Transporter or: xcrun altool --upload-package"
fi
print "Built ${app} (version ${version}, signed with: ${identity})."
