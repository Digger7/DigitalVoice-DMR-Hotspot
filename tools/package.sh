#!/usr/bin/env bash
# Build distributable installer packages (filenames carry the version):
#   dist/DigitalVoice-DMRHotspot-<ver>-MacOS-universal.dmg   (.app bundle, arm64+x86_64)
#   dist/DigitalVoice-DMRHotspot-<ver>-Windows-amd64.zip     (binaries + install.bat)
# Version source (first match wins):
#   1. env VERSION=v1.2.3
#   2. VERSION file in the repo root (bump it for each release)
# Env:
#   INCLUDE_VOICES=0   skip the 241 MB piper voices
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST="$ROOT/dist"
BUILD="$DIST/.build"
INCLUDE_VOICES=${INCLUDE_VOICES:-1}

VERSION=${VERSION:-}
if [ -z "$VERSION" ]; then
	VERSION=$(tr -d ' \t\r\n' <"$ROOT/VERSION" 2>/dev/null || true)
fi
[ -n "$VERSION" ] || VERSION="1.0.0"
# Plain number for filenames (strip a leading "v": v1.2.3 -> 1.2.3).
NAME_VERSION=$(printf '%s' "$VERSION" | sed 's/^v//')
case "$NAME_VERSION" in
	[0-9]*) : ;;
	*) printf 'WARNING: VERSION "%s" is not a plain number, package names will look odd\n' "$NAME_VERSION" >&2 ;;
esac
PLIST_VERSION=$(printf '%s' "$NAME_VERSION" | tr -c '0-9.' '.' | sed 's/^[.]*//;s/[.]*$//;s/\.\.*/./g')
[ -n "$PLIST_VERSION" ] || PLIST_VERSION="1.0.0"
SHORT_VERSION=$NAME_VERSION

APP_NAME="DigitalVoice - DMR Hotspot"
PKG_BASE="DigitalVoice-DMRHotspot"
BUNDLE_ID="ru.dmr.hotspot"
EXES="dmr-gui dmr-hotspot dmr-diag dmr-selftest"

SRC=${OPENDMR_SRC:-"$ROOT/.cache/OpenDMR"}
PATCH="$ROOT/tools/opendmr/opendmr-fixes.patch"
REPO=${OPENDMR_REPO:-https://github.com/MW0MWZ/OpenDMR.git}
REV=${OPENDMR_REV:-d28164b39ba4d91ad5948ff22707937f8944f70f}

rm -rf "$BUILD"
mkdir -p "$BUILD" "$DIST"
# Remove pre-versioning artifacts (names without a version number).
rm -f "$DIST/DMRHotspot-MacOS-universal.dmg" "$DIST/DMRHotspot-Windows-amd64.zip" \
	"$DIST/DigitalVoice-DMRHotspot-MacOS-universal.dmg" "$DIST/DigitalVoice-DMRHotspot-Windows-amd64.zip"

log() { printf '\n=== %s\n' "$*"; }

# --- App icon (PNG master -> icns/ico) ----------------------------------------
ICON_PNG="$ROOT/internal/webui/icon.png"
[ -f "$ICON_PNG" ] || go run ./tools/genicon -png "$ICON_PNG"

make_iconset() {
	log "Iconset"
	IS="$BUILD/AppIcon.iconset"
	mkdir -p "$IS"
	while read -r name sz; do
		sips -z "$sz" "$sz" "$ICON_PNG" --out "$IS/$name" >/dev/null
	done <<'EOF'
icon_16x16.png 16
icon_16x16@2x.png 32
icon_32x32.png 32
icon_32x32@2x.png 64
icon_128x128.png 128
icon_128x128@2x.png 256
icon_256x256.png 256
icon_256x256@2x.png 512
icon_512x512.png 512
icon_512x512@2x.png 1024
EOF
	iconutil -c icns "$IS" -o "$BUILD/app.icns"
	log "Iconset OK"
}
make_iconset
go run ./tools/genicon -png 0 -ico "$BUILD/app.ico"

# --- OpenDMR vocoder library -------------------------------------------------
prepare_opendmr() {
	if [ ! -d "$SRC/.git" ]; then
		git clone "$REPO" "$SRC"
	fi
	if ! git -C "$SRC" rev-parse --verify --quiet "$REV" >/dev/null; then
		git -C "$SRC" fetch --depth 1 origin "$REV"
	fi
	git -C "$SRC" checkout --force "$REV"
	git -C "$SRC" clean -fdx
	git -C "$SRC" apply "$PATCH"
}

build_dylib() { # $1 = arch
	log "OpenDMR dylib $1"
	prepare_opendmr
	make -C "$SRC" clean
	make -C "$SRC" -j8 libopendmr.dylib CC="gcc -arch $1" CXX="g++ -arch $1"
	cp "$SRC/libopendmr.dylib" "$BUILD/libopendmr-$1.dylib"
}

build_dylib arm64 >>"$BUILD/dylib.log" 2>&1
build_dylib x86_64 >>"$BUILD/dylib.log" 2>&1
lipo -create "$BUILD/libopendmr-arm64.dylib" "$BUILD/libopendmr-x86_64.dylib" \
	-output "$ROOT/internal/dmr/libopendmr.dylib"
codesign --force --sign - "$ROOT/internal/dmr/libopendmr.dylib"

# --- Go binaries --------------------------------------------------------------
compile() { # $1 = goos $2 = goarch $3 = exe-suffix $4 = extra cgo env (""|cross)
	local goos=$1 goarch=$2 suffix=$3 cross=$4
	local dir="$BUILD/${goos}-${goarch}"
	mkdir -p "$dir"
	for exe in $EXES dmr-gui-launcher; do
		if [ -n "$cross" ]; then
			env CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
				CC="clang -arch x86_64" go build -ldflags "-s -w" -o "$dir/$exe$suffix" "./cmd/$exe"
		else
			env CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
				go build -ldflags "-s -w" -o "$dir/$exe$suffix" "./cmd/$exe"
		fi
	done
}

log "Go darwin/arm64 + darwin/amd64"
compile darwin arm64 "" ""
compile darwin amd64 "" cross
for exe in $EXES dmr-gui-launcher; do
	lipo -create -output "$BUILD/$exe" \
		"$BUILD/darwin-arm64/$exe" "$BUILD/darwin-amd64/$exe"
done

log "Fix dylib rpath"
rpaths() { otool -l "$1" | grep -A2 LC_RPATH | awk '/path/{sub(/^ *path /,""); sub(/ \(offset.*$/,""); print}'; }
for exe in dmr-gui dmr-hotspot dmr-diag dmr-selftest; do
	while read -r p; do
		install_name_tool -delete_rpath "$p" "$BUILD/$exe" 2>/dev/null || true
	done < <(rpaths "$BUILD/$exe")
	install_name_tool -add_rpath @executable_path "$BUILD/$exe" 2>/dev/null || true
	codesign --force --sign - "$BUILD/$exe"
done
codesign --force --sign - "$BUILD/dmr-gui-launcher"

# --- macOS .app ---------------------------------------------------------------
APPROOT="$BUILD/appstage"
APP="$APPROOT/$APP_NAME.app"
log "Assemble $APP_NAME.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/voices/piper"
cp "$BUILD/dmr-gui-launcher" "$APP/Contents/MacOS/DMRHotspot"
chmod +x "$APP/Contents/MacOS/DMRHotspot"
for exe in $EXES; do
	cp "$BUILD/$exe" "$APP/Contents/Resources/$exe"
	chmod +x "$APP/Contents/Resources/$exe"
done
cp "$ROOT/internal/dmr/libopendmr.dylib" "$APP/Contents/Resources/"
cp "$ROOT/config.example.yaml" "$APP/Contents/Resources/"
cp "$BUILD/app.icns" "$APP/Contents/Resources/app.icns"
if [ "$INCLUDE_VOICES" = 1 ]; then
	cp "$ROOT"/voices/piper/*.onnx "$ROOT"/voices/piper/*.onnx.json \
		"$APP/Contents/Resources/voices/piper/"
fi
printf 'APPL????' >"$APP/Contents/PkgInfo"

cat >"$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>$APP_NAME</string>
	<key>CFBundleDisplayName</key><string>$APP_NAME</string>
	<key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
	<key>CFBundleShortVersionString</key><string>$SHORT_VERSION</string>
	<key>CFBundleVersion</key><string>$PLIST_VERSION</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleExecutable</key><string>DMRHotspot</string>
	<key>CFBundleIconFile</key><string>app.icns</string>
	<key>CFBundleDevelopmentRegion</key><string>en</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
</dict>
</plist>
EOF

codesign --force --sign - "$APP"

log "DMG"
DMG_OUT="$DIST/$PKG_BASE-$NAME_VERSION-MacOS-universal.dmg"
rm -f "$DMG_OUT"
ln -sfn /Applications "$APPROOT/Applications"
hdiutil create -volname "$APP_NAME" -srcfolder "$APPROOT" \
	-ov -format UDZO -quiet "$DMG_OUT"

# --- Windows ------------------------------------------------------------------
log "Go windows/amd64"
mkdir -p "$BUILD/win-bin"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" \
	-o "$BUILD/win-bin" ./cmd/dmr-hotspot ./cmd/dmr-gui ./cmd/dmr-diag ./cmd/dmr-selftest

ZIP_ROOT="$BUILD/$PKG_BASE-$NAME_VERSION-win64"
mkdir -p "$ZIP_ROOT/voices/piper"
for exe in $EXES; do
	cp "$BUILD/win-bin/$exe.exe" "$ZIP_ROOT/$exe.exe"
done
cp "$ROOT/config.example.yaml" "$ZIP_ROOT/"
cp "$BUILD/app.ico" "$ZIP_ROOT/app.ico"
cp "$ICON_PNG" "$ZIP_ROOT/icon.png"
if [ "$INCLUDE_VOICES" = 1 ]; then
	cp "$ROOT"/voices/piper/*.onnx "$ROOT"/voices/piper/*.onnx.json \
		"$ZIP_ROOT/voices/piper/"
fi

sed 's/$/\r/' >"$ZIP_ROOT/install.bat" <<'EOF'
@echo off
setlocal EnableExtensions
set "HERE=%~dp0"
if "%HERE:~-1%"=="\" set "HERE=%HERE:~0,-1%"
set "DEST=%LOCALAPPDATA%\Programs\DigitalVoice - DMR Hotspot"
echo Installing DigitalVoice - DMR Hotspot to %DEST% ...
if not exist "%DEST%" mkdir "%DEST%"
robocopy "%HERE%" "%DEST%" /E /NFL /NDL /NJH /NJS /NP /XF install.bat uninstall.bat
if errorlevel 8 goto :fail
powershell -NoProfile -ExecutionPolicy Bypass -Command "$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Desktop')) 'DigitalVoice - DMR Hotspot.lnk'));$s.TargetPath='%DEST%\dmr-gui.exe';$s.WorkingDirectory='%DEST%';$s.IconLocation='%DEST%\app.ico';$s.Save()"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut((Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs\DigitalVoice - DMR Hotspot.lnk'));$s.TargetPath='%DEST%\dmr-gui.exe';$s.WorkingDirectory='%DEST%';$s.IconLocation='%DEST%\app.ico';$s.Save()"
echo.
echo Done. Shortcuts created on Desktop and Start Menu.
choice /t 3 /c YN /d N /m "Launch DigitalVoice - DMR Hotspot now"
if errorlevel 2 goto :eof
start "" "%DEST%\dmr-gui.exe"
exit /b 0
:fail
echo Install failed, robocopy error.
pause
exit /b 1
EOF

sed 's/$/\r/' >"$ZIP_ROOT/uninstall.bat" <<'EOF'
@echo off
setlocal
rd /s /q "%LOCALAPPDATA%\Programs\DigitalVoice - DMR Hotspot" 2>nul
del "%USERPROFILE%\Desktop\DigitalVoice - DMR Hotspot.lnk" 2>nul
del "%APPDATA%\Microsoft\Windows\Start Menu\Programs\DigitalVoice - DMR Hotspot.lnk" 2>nul
echo DigitalVoice - DMR Hotspot removed.
pause
EOF

sed 's/$/\r/' >"$ZIP_ROOT/README.txt" <<EOF
DigitalVoice - DMR Hotspot runtime for Windows amd64 (v$SHORT_VERSION)
========================================================

Composition:
  dmr-gui.exe        web UI + hotspot management (http://127.0.0.1:8080)
  dmr-hotspot.exe    headless hotspot
  dmr-diag.exe       hardware diagnostics (list of COM ports)
  dmr-selftest.exe   self-check program
  app.ico, icon.png  application icon
  voices/piper/*     Russian TTS voices

Install:
  1) Unzip the archive anywhere;
  2) Right-click on install.bat - Run.
     Files will go to %LOCALAPPDATA%\\Programs\\DigitalVoice - DMR Hotspot,
     shortcuts will appear on the desktop and in the "Start" menu.
     Complete removal - uninstall.bat from the same archive.

Important about Windows (current state of the code):
  - Support for COM ports (modem serial) is a stub for darwin/linux:
    RF transmission via MMDVM modem on Windows is NOT YET WORKING.
  - libopendmr (vocoder) is darwin/cgo only: the voice encoder/decoder is unavailable.
  - Agent mode (whisper) requires a Python environment; piper is installed manually.

Everything else (web UI, config editor, BM connection logic without codec)
works as usual. As the desired targets are closed (SerialPort/Windows +
libopendmr/Windows), the binary will be repackaged without code changes.

Notes:
  - config.yaml is created on first launch in the program folder;
    a template with comments is config.example.yaml.
  - No signature: SmartScreen may warn - "More info" -> "Run anyway".
EOF

log "ZIP"
ZIP_OUT="$DIST/$PKG_BASE-$NAME_VERSION-Windows-amd64.zip"
rm -f "$ZIP_OUT"
(cd "$BUILD" && zip -r -q -X "$ZIP_OUT" "$PKG_BASE-$NAME_VERSION-win64")

# --- checksums + cleanup -------------------------------------------------------
log "Checksums"
(cd "$DIST" && shasum -a 256 DigitalVoice-DMRHotspot-*.dmg DigitalVoice-DMRHotspot-*.zip >SHA256SUMS.txt)

rm -rf "$BUILD"
log "Done (v$VERSION)"
ls -lh "$DIST"
